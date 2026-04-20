package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// VLLMAdapter calls vLLM's OpenAI-compatible /v1/chat/completions endpoint.
// Base URL is read from VLLM_BASE_URL (default: http://localhost:8000).
type VLLMAdapter struct {
	baseURL string
	client  *http.Client
	cb      *CircuitBreaker
}

// NewVLLMAdapter constructs a VLLMAdapter from environment configuration.
func NewVLLMAdapter() *VLLMAdapter {
	baseURL := os.Getenv("VLLM_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8000"
	}
	return &VLLMAdapter{
		baseURL: baseURL,
		client:  &http.Client{},
		cb:      NewCircuitBreaker(),
	}
}

// Generate performs a blocking inference call with a 25 s hard deadline.
// Messages are forwarded directly without conversion (OpenAI-compatible format).
func (a *VLLMAdapter) Generate(ctx context.Context, req *GenerateRequest) (*GenerateResponse, error) {
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

// Stream delegates to Generate and wraps the single response in a closed channel.
// Full SSE streaming is deferred to Phase 2.
func (a *VLLMAdapter) Stream(ctx context.Context, req *GenerateRequest) (<-chan *GenerateResponse, error) {
	if !a.cb.Allow() {
		return nil, ErrCircuitOpen
	}

	innerCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	resp, err := a.doGenerate(innerCtx, req)
	if err != nil {
		a.cb.RecordFailure()
		return nil, err
	}
	a.cb.RecordSuccess()

	ch := make(chan *GenerateResponse, 1)
	ch <- resp
	close(ch)
	return ch, nil
}

// HealthCheck verifies the vLLM server is reachable via GET /health.
func (a *VLLMAdapter) HealthCheck(ctx context.Context) error {
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
		return fmt.Errorf("vllm unhealthy: status %d", resp.StatusCode)
	}
	return nil
}

// vllmChatRequest is the OpenAI-compatible request body sent to /v1/chat/completions.
type vllmChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Stream      bool      `json:"stream"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

// vllmChatResponse mirrors the OpenAI chat completions response shape.
type vllmChatResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int     `json:"index"`
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
}

func (a *VLLMAdapter) doGenerate(ctx context.Context, req *GenerateRequest) (*GenerateResponse, error) {
	chatReq := vllmChatRequest{
		Model:       req.Model,
		Messages:    req.Messages,
		Stream:      false,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}

	body, err := json.Marshal(chatReq)
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
		return nil, fmt.Errorf("vllm: unexpected status %d", resp.StatusCode)
	}

	var result vllmChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	genResp := &GenerateResponse{
		ID:      result.ID,
		Object:  result.Object,
		Model:   result.Model,
		Choices: make([]Choice, len(result.Choices)),
	}
	for i, c := range result.Choices {
		genResp.Choices[i] = Choice{
			Index:        c.Index,
			Message:      c.Message,
			FinishReason: c.FinishReason,
		}
	}
	return genResp, nil
}
