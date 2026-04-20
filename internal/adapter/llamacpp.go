package adapter

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

// LlamaCppAdapter calls a llama.cpp HTTP server's /completion endpoint.
// Base URL is read from LLAMACPP_BASE_URL (default: http://localhost:8080).
type LlamaCppAdapter struct {
	baseURL string
	client  *http.Client
	cb      *CircuitBreaker
}

// NewLlamaCppAdapter constructs a LlamaCppAdapter from environment configuration.
func NewLlamaCppAdapter() *LlamaCppAdapter {
	baseURL := os.Getenv("LLAMACPP_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	return &LlamaCppAdapter{
		baseURL: baseURL,
		client:  &http.Client{},
		cb:      NewCircuitBreaker(),
	}
}

// Generate performs a blocking inference call with a 25 s hard deadline.
func (a *LlamaCppAdapter) Generate(ctx context.Context, req *GenerateRequest) (*GenerateResponse, error) {
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
func (a *LlamaCppAdapter) Stream(ctx context.Context, req *GenerateRequest) (<-chan *GenerateResponse, error) {
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

// HealthCheck verifies the llama.cpp server is reachable via GET /health.
func (a *LlamaCppAdapter) HealthCheck(ctx context.Context) error {
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
		return fmt.Errorf("llamacpp unhealthy: status %d", resp.StatusCode)
	}
	return nil
}

func (a *LlamaCppAdapter) doGenerate(ctx context.Context, req *GenerateRequest) (*GenerateResponse, error) {
	body, err := json.Marshal(map[string]any{
		"prompt":      messagesToPrompt(req.Messages),
		"n_predict":   req.MaxTokens,
		"stream":      false,
		"grammar":     req.Grammar,
		"temperature": req.Temperature,
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.baseURL+"/completion", bytes.NewReader(body))
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
		return nil, fmt.Errorf("llamacpp: unexpected status %d", resp.StatusCode)
	}

	var result struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &GenerateResponse{
		ID:     uuid.New().String(),
		Object: "chat.completion",
		Model:  req.Model,
		Choices: []Choice{
			{
				Index:        0,
				Message:      Message{Role: "assistant", Content: result.Content},
				FinishReason: "stop",
			},
		},
	}, nil
}

func (a *LlamaCppAdapter) doStream(ctx context.Context, cancel context.CancelFunc, req *GenerateRequest) (<-chan *GenerateResponse, error) {
	body, err := json.Marshal(map[string]any{
		"prompt":  messagesToPrompt(req.Messages),
		"stream":  true,
		"grammar": req.Grammar,
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.baseURL+"/completion", bytes.NewReader(body))
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
		return nil, fmt.Errorf("llamacpp: unexpected status %d", resp.StatusCode)
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
			var chunk struct {
				Content string `json:"content"`
				Stop    bool   `json:"stop"`
			}
			if err := json.Unmarshal([]byte(line[6:]), &chunk); err != nil {
				continue
			}
			if chunk.Content != "" {
				ch <- &GenerateResponse{
					ID:     uuid.New().String(),
					Object: "chat.completion.chunk",
					Model:  req.Model,
					Choices: []Choice{
						{
							Index: 0,
							Delta: &Message{Role: "assistant", Content: chunk.Content},
						},
					},
				}
			}
			if chunk.Stop {
				return
			}
		}
	}()

	return ch, nil
}
