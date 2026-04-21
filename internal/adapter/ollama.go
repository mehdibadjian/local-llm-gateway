package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
)

// OllamaAdapter calls Ollama's /api/generate endpoint.
// Base URL is read from OLLAMA_BASE_URL (default: http://localhost:11434).
type OllamaAdapter struct {
	baseURL string
	client  *http.Client
	cb      *CircuitBreaker
}

// NewOllamaAdapter constructs an OllamaAdapter from environment configuration.
func NewOllamaAdapter() *OllamaAdapter {
	baseURL := os.Getenv("OLLAMA_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return &OllamaAdapter{
		baseURL: baseURL,
		client:  &http.Client{},
		cb:      NewCircuitBreaker(),
	}
}

// Generate performs a blocking inference call with a 25 s hard deadline.
func (a *OllamaAdapter) Generate(ctx context.Context, req *GenerateRequest) (*GenerateResponse, error) {
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

// Stream returns a channel of partial GenerateResponse values (NDJSON tokens).
func (a *OllamaAdapter) Stream(ctx context.Context, req *GenerateRequest) (<-chan *GenerateResponse, error) {
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

// HealthCheck verifies Ollama is reachable via GET /api/tags (no inference round-trip).
func (a *OllamaAdapter) HealthCheck(ctx context.Context) error {
	hCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(hCtx, http.MethodGet, a.baseURL+"/api/tags", nil)
	if err != nil {
		return err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama unhealthy: status %d", resp.StatusCode)
	}
	return nil
}

func (a *OllamaAdapter) doGenerate(ctx context.Context, req *GenerateRequest) (*GenerateResponse, error) {
	numPredict := req.MaxTokens
	if numPredict <= 0 {
		numPredict = -1 // -1 = generate until EOS in Ollama
	}
	body, err := json.Marshal(map[string]any{
		"model":  req.Model,
		"prompt": messagesToPrompt(req.Messages),
		"stream": false,
		"format": req.Format,
		"options": map[string]any{
			"num_predict": numPredict,
			"temperature": req.Temperature,
		},
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.baseURL+"/api/generate", bytes.NewReader(body))
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
		return nil, fmt.Errorf("ollama: unexpected status %d", resp.StatusCode)
	}

	var result struct {
		Response string `json:"response"`
		Model    string `json:"model"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &GenerateResponse{
		ID:     uuid.New().String(),
		Object: "chat.completion",
		Model:  result.Model,
		Choices: []Choice{
			{
				Index:        0,
				Message:      Message{Role: "assistant", Content: result.Response},
				FinishReason: "stop",
			},
		},
	}, nil
}

func (a *OllamaAdapter) doStream(ctx context.Context, cancel context.CancelFunc, req *GenerateRequest) (<-chan *GenerateResponse, error) {
	body, err := json.Marshal(map[string]any{
		"model":  req.Model,
		"prompt": messagesToPrompt(req.Messages),
		"stream": true,
		"format": req.Format,
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.baseURL+"/api/generate", bytes.NewReader(body))
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
		return nil, fmt.Errorf("ollama: unexpected status %d", resp.StatusCode)
	}

	ch := make(chan *GenerateResponse, 32)
	go func() {
		defer cancel()
		defer close(ch)
		defer resp.Body.Close()

		dec := json.NewDecoder(resp.Body)
		for {
			var chunk struct {
				Response string `json:"response"`
				Model    string `json:"model"`
				Done     bool   `json:"done"`
			}
			if err := dec.Decode(&chunk); err != nil {
				return
			}
			if chunk.Response != "" {
				ch <- &GenerateResponse{
					ID:     uuid.New().String(),
					Object: "chat.completion.chunk",
					Model:  chunk.Model,
					Choices: []Choice{
						{
							Index: 0,
							Delta: &Message{Role: "assistant", Content: chunk.Response},
						},
					},
				}
			}
			if chunk.Done {
				return
			}
		}
	}()

	return ch, nil
}
