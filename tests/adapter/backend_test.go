package adapter_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/caw/wrapper/internal/adapter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInferenceBackendInterface verifies MockInferenceBackend satisfies the interface.
func TestInferenceBackendInterface(t *testing.T) {
	var _ adapter.InferenceBackend = &adapter.MockInferenceBackend{}

	mock := &adapter.MockInferenceBackend{}
	resp, err := mock.Generate(context.Background(), &adapter.GenerateRequest{
		Model:    "test-model",
		Messages: []adapter.Message{{Role: "user", Content: "hello"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "mock response", resp.Choices[0].Message.Content)
}

// TestOllamaAdapter_25sTimeout verifies context cancellation propagates to the HTTP call.
func TestOllamaAdapter_25sTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a backend that is slower than our test deadline (100 ms).
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Setenv("OLLAMA_BASE_URL", server.URL)
	a := adapter.NewOllamaAdapter()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := a.Generate(ctx, &adapter.GenerateRequest{
		Model:    "gemma:2b",
		Messages: []adapter.Message{{Role: "user", Content: "hello"}},
	})

	require.Error(t, err)
	assert.True(t,
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled),
		"expected context cancellation, got: %v", err,
	)
}

// TestCircuitBreaker_OpensAfter3Failures triggers 3 errors and verifies the circuit opens.
func TestCircuitBreaker_OpensAfter3Failures(t *testing.T) {
	cb := adapter.NewCircuitBreaker()

	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordFailure()

	assert.Equal(t, adapter.StateOpen, cb.GetState())
	assert.False(t, cb.Allow())
}

// TestCircuitBreaker_ClosesAfterHalfOpen advances time 30s, probes, verifies circuit closes.
func TestCircuitBreaker_ClosesAfterHalfOpen(t *testing.T) {
	cb := adapter.NewCircuitBreaker()

	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordFailure()
	assert.Equal(t, adapter.StateOpen, cb.GetState())

	// Simulate 31s elapsing.
	cb.NowFn = func() time.Time { return time.Now().Add(31 * time.Second) }

	// First Allow() transitions to HalfOpen and lets the probe through.
	assert.True(t, cb.Allow())
	assert.Equal(t, adapter.StateHalfOpen, cb.GetState())

	// Subsequent calls are still rejected while half-open.
	assert.False(t, cb.Allow())

	// Probe succeeds → circuit closes.
	cb.RecordSuccess()
	assert.Equal(t, adapter.StateClosed, cb.GetState())
	assert.True(t, cb.Allow())
}

// TestCircuitBreaker_FailFastWhenOpen verifies calls return ErrCircuitOpen without hitting the backend.
func TestCircuitBreaker_FailFastWhenOpen(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	t.Setenv("OLLAMA_BASE_URL", server.URL)
	a := adapter.NewOllamaAdapter()

	req := &adapter.GenerateRequest{
		Model:    "gemma:2b",
		Messages: []adapter.Message{{Role: "user", Content: "hello"}},
	}

	// Exhaust the threshold.
	for i := 0; i < 3; i++ {
		_, _ = a.Generate(context.Background(), req)
	}

	_, err := a.Generate(context.Background(), req)
	assert.ErrorIs(t, err, adapter.ErrCircuitOpen)
}

// TestLlamaCppAdapter_GrammarMode verifies the grammar field is forwarded in the request body.
func TestLlamaCppAdapter_GrammarMode(t *testing.T) {
	var captured map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/completion", r.URL.Path)
		_ = json.NewDecoder(r.Body).Decode(&captured)
		_ = json.NewEncoder(w).Encode(map[string]any{"content": "ok"})
	}))
	defer server.Close()

	t.Setenv("LLAMACPP_BASE_URL", server.URL)
	a := adapter.NewLlamaCppAdapter()

	grammar := `root ::= [a-z]+ (" " [a-z]+)*`
	_, err := a.Generate(context.Background(), &adapter.GenerateRequest{
		Model:   "llama3",
		Messages: []adapter.Message{{Role: "user", Content: "hello"}},
		Grammar: grammar,
	})

	require.NoError(t, err)
	assert.Equal(t, grammar, captured["grammar"])
}

// TestVLLMAdapter_OpenAICompat verifies messages are serialised to /v1/chat/completions.
func TestVLLMAdapter_OpenAICompat(t *testing.T) {
	var captured map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		_ = json.NewDecoder(r.Body).Decode(&captured)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "test-id",
			"object": "chat.completion",
			"model":  "mistral-7b",
			"choices": []map[string]any{
				{
					"index":         0,
					"message":       map[string]any{"role": "assistant", "content": "response"},
					"finish_reason": "stop",
				},
			},
		})
	}))
	defer server.Close()

	t.Setenv("VLLM_BASE_URL", server.URL)
	a := adapter.NewVLLMAdapter()

	messages := []adapter.Message{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "Hello"},
	}

	resp, err := a.Generate(context.Background(), &adapter.GenerateRequest{
		Model:    "mistral-7b",
		Messages: messages,
	})

	require.NoError(t, err)
	assert.Equal(t, "test-id", resp.ID)

	// Messages must be passed through directly, not collapsed to a string prompt.
	rawMsgs, ok := captured["messages"].([]any)
	require.True(t, ok, "messages field must be an array")
	require.Len(t, rawMsgs, 2)
	assert.Equal(t, "system", rawMsgs[0].(map[string]any)["role"])
	assert.Equal(t, "You are helpful", rawMsgs[0].(map[string]any)["content"])
}

// TestAdapterFactory_SelectsFromEnv verifies the correct concrete type is returned per env var.
func TestAdapterFactory_SelectsFromEnv(t *testing.T) {
	cases := []struct {
		env      string
		assertFn func(t *testing.T, b adapter.InferenceBackend)
	}{
		{
			env: "",
			assertFn: func(t *testing.T, b adapter.InferenceBackend) {
				_, ok := b.(*adapter.OllamaAdapter)
				assert.True(t, ok, "default should be OllamaAdapter")
			},
		},
		{
			env: "ollama",
			assertFn: func(t *testing.T, b adapter.InferenceBackend) {
				_, ok := b.(*adapter.OllamaAdapter)
				assert.True(t, ok, "ollama env → OllamaAdapter")
			},
		},
		{
			env: "llamacpp",
			assertFn: func(t *testing.T, b adapter.InferenceBackend) {
				_, ok := b.(*adapter.LlamaCppAdapter)
				assert.True(t, ok, "llamacpp env → LlamaCppAdapter")
			},
		},
		{
			env: "vllm",
			assertFn: func(t *testing.T, b adapter.InferenceBackend) {
				_, ok := b.(*adapter.VLLMAdapter)
				assert.True(t, ok, "vllm env → VLLMAdapter")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.env, func(t *testing.T) {
			t.Setenv("INFERENCE_BACKEND", tc.env)
			b, err := adapter.NewBackend()
			require.NoError(t, err)
			tc.assertFn(t, b)
		})
	}
}
