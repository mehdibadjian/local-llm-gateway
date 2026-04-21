package adapter

// BitNetAdapter calls a bitnet.cpp server's /completion endpoint.
// bitnet.cpp exposes a llama.cpp-compatible API, so the request/response shape
// is identical to LlamaCppAdapter.
//
// Required server launch:
//
//	./server -m BitNet-b1.58-2B-4T.gguf -q i2_s --port 8080
//
// Environment variables:
//   - BITNET_BASE_URL   — server base URL (default: http://localhost:8080)
//   - BITNET_MODEL_QUANT — must be "i2_s" (ternary quantization); any other value
//     causes NewBitNetAdapter to return an error.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

// BitNetAdapter wraps a bitnet.cpp HTTP server.
type BitNetAdapter struct {
	baseURL string
	client  *http.Client
	cb      *CircuitBreaker
}

// NewBitNetAdapter constructs a BitNetAdapter from environment configuration.
// Returns an error if BITNET_MODEL_QUANT is set to anything other than "i2_s".
func NewBitNetAdapter() (*BitNetAdapter, error) {
	quant := os.Getenv("BITNET_MODEL_QUANT")
	if quant != "" && quant != "i2_s" {
		return nil, fmt.Errorf("bitnet: unsupported quantization %q — model must use i2_s ternary quantization", quant)
	}

	baseURL := os.Getenv("BITNET_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}

	return &BitNetAdapter{
		baseURL: baseURL,
		client:  &http.Client{},
		cb:      NewCircuitBreaker(),
	}, nil
}

// Generate performs a blocking inference call with a 25 s hard deadline.
func (a *BitNetAdapter) Generate(ctx context.Context, req *GenerateRequest) (*GenerateResponse, error) {
	if !a.cb.Allow() {
		return nil, ErrCircuitOpen
	}

	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	resp, err := a.doGenerate(ctx, req)
	if err != nil {
		a.cb.RecordFailure()
		return nil, err
	}
	a.cb.RecordSuccess()
	return resp, nil
}

// Stream returns a channel of partial GenerateResponse values parsed from SSE lines.
func (a *BitNetAdapter) Stream(ctx context.Context, req *GenerateRequest) (<-chan *GenerateResponse, error) {
	if !a.cb.Allow() {
		return nil, ErrCircuitOpen
	}

	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)

	streamReq := *req
	streamReq.Stream = true

	ch, err := a.doStream(ctx, cancel, &streamReq)
	if err != nil {
		cancel()
		a.cb.RecordFailure()
		return nil, err
	}
	a.cb.RecordSuccess()
	return ch, nil
}

// HealthCheck verifies the bitnet.cpp server is reachable via GET /health.
func (a *BitNetAdapter) HealthCheck(ctx context.Context) error {
	hCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(hCtx, http.MethodGet, a.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bitnet: unhealthy status %d", resp.StatusCode)
	}
	return nil
}

func (a *BitNetAdapter) doGenerate(ctx context.Context, req *GenerateRequest) (*GenerateResponse, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 512
	}

	type msgBody struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	msgs := make([]msgBody, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = msgBody{Role: m.Role, Content: m.Content}
	}

	body, err := json.Marshal(map[string]any{
		"model":       req.Model,
		"messages":    msgs,
		"max_tokens":  maxTokens,
		"stream":      false,
		"temperature": req.Temperature,
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bitnet: unexpected status %d", resp.StatusCode)
	}

	var result struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("bitnet: empty choices in response")
	}

	id := result.ID
	if id == "" {
		id = uuid.New().String()
	}
	return &GenerateResponse{
		ID:     id,
		Object: "chat.completion",
		Model:  req.Model,
		Choices: []Choice{
			{
				Index:        0,
				Message:      Message{Role: "assistant", Content: result.Choices[0].Message.Content},
				FinishReason: result.Choices[0].FinishReason,
			},
		},
	}, nil
}

func (a *BitNetAdapter) doStream(ctx context.Context, cancel context.CancelFunc, req *GenerateRequest) (<-chan *GenerateResponse, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 512
	}

	type msgBody struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	msgs := make([]msgBody, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = msgBody{Role: m.Role, Content: m.Content}
	}

	body, err := json.Marshal(map[string]any{
		"model":      req.Model,
		"messages":   msgs,
		"max_tokens": maxTokens,
		"stream":     true,
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("bitnet: unexpected status %d", resp.StatusCode)
	}

	ch := make(chan *GenerateResponse, 32)
	go func() {
		defer cancel()
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := line[6:]
			if strings.TrimSpace(payload) == "[DONE]" {
				return
			}
			var chunk struct {
				ID      string `json:"id"`
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
					FinishReason *string `json:"finish_reason"`
				} `json:"choices"`
			}
			if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
				continue
			}
			if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
				ch <- &GenerateResponse{
					ID:     chunk.ID,
					Object: "chat.completion.chunk",
					Model:  req.Model,
					Choices: []Choice{
						{
							Index: 0,
							Delta: &Message{Role: "assistant", Content: chunk.Choices[0].Delta.Content},
						},
					},
				}
			}
			if len(chunk.Choices) > 0 && chunk.Choices[0].FinishReason != nil {
				return
			}
		}
	}()

	return ch, nil
}
