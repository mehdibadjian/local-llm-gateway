package adapter

import (
	"context"
	"errors"
	"strings"
)

// ErrCircuitOpen is returned when the circuit breaker is in the Open or HalfOpen state
// and rejects the call immediately without hitting the backend.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// Message is a single turn in a conversation.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// GenerateRequest mirrors the OpenAI chat completions request shape.
type GenerateRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Stream      bool      `json:"stream"`
	Format      string    `json:"format,omitempty"`      // "json" for structured output
	Grammar     string    `json:"grammar,omitempty"`     // llama.cpp grammar mode
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

// Choice is one completion alternative in a GenerateResponse.
type Choice struct {
	Index        int      `json:"index"`
	Message      Message  `json:"message"`
	FinishReason string   `json:"finish_reason"`
	Delta        *Message `json:"delta,omitempty"` // populated for streaming chunks
}

// GenerateResponse is the normalised response returned by every adapter.
type GenerateResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
}

// InferenceBackend is the single coupling point between the orchestration engine
// and any inference backend. Swapping backends requires only an env-var change.
type InferenceBackend interface {
	// Generate performs a blocking (non-streaming) inference call.
	// The caller is responsible for passing a context with an appropriate deadline;
	// each adapter additionally enforces a 25s hard limit via context.WithTimeout.
	Generate(ctx context.Context, req *GenerateRequest) (*GenerateResponse, error)

	// Stream returns a channel of partial responses (token-by-token).
	// The channel is closed when generation is complete or the context is cancelled.
	Stream(ctx context.Context, req *GenerateRequest) (<-chan *GenerateResponse, error)

	// HealthCheck verifies the backend process is reachable.
	// It MUST NOT perform a full inference round-trip.
	HealthCheck(ctx context.Context) error
}

// messagesToPrompt collapses a Messages slice into a single prompt string used by
// backends that expect a plain-text prompt (Ollama /api/generate, llama.cpp /completion).
func messagesToPrompt(messages []Message) string {
	var sb strings.Builder
	for _, m := range messages {
		sb.WriteString(m.Role)
		sb.WriteString(": ")
		sb.WriteString(m.Content)
		sb.WriteString("\n")
	}
	return sb.String()
}
