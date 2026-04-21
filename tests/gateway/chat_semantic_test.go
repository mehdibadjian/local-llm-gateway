package gateway_test

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/caw/wrapper/internal/adapter"
	"github.com/caw/wrapper/internal/gateway"
	"github.com/caw/wrapper/internal/memory"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockEmbedClient returns a fixed embedding for every text.
type mockEmbedClient struct {
	embedding []float32
	err       error
}

func (m *mockEmbedClient) Embed(_ context.Context, _ string) ([]float32, error) {
	return m.embedding, m.err
}
func (m *mockEmbedClient) HealthCheck(_ context.Context) error { return nil }

// newTestServerWithEmbed creates a gateway Server and registers the given embed
// client so the semantic cache is active.
func newTestServerWithEmbed(t *testing.T, backend adapter.InferenceBackend, ec *mockEmbedClient) *gateway.Server {
	t.Helper()
	t.Setenv("CAW_API_KEY", "test-key")

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	session := memory.NewSessionStore(rdb)

	srv := gateway.NewServer(backend, rdb, session)
	srv.RegisterEmbedClient(ec)
	return srv
}

// TestChatComplete_SemanticCacheHit_ReturnsHeader verifies that a second request
// with an identical embedding returns the X-CAW-Cache-Hit: semantic header and
// does NOT call backend.Generate a second time.
func TestChatComplete_SemanticCacheHit_ReturnsHeader(t *testing.T) {
	var generateCalls atomic.Int32
	backend := &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, req *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			generateCalls.Add(1)
			return &adapter.GenerateResponse{
				ID:    "id-1",
				Model: req.Model,
				Choices: []adapter.Choice{
					{Index: 0, Message: adapter.Message{Role: "assistant", Content: "hello"}, FinishReason: "stop"},
				},
			}, nil
		},
	}

	// Mock embed client that always returns the same vector.
	ec := &mockEmbedClient{embedding: []float32{1.0, 0.0, 0.0}}
	srv := newTestServerWithEmbed(t, backend, ec)
	app := srv.App()

	body := `{"model":"gemma:2b","messages":[{"role":"user","content":"hello world"}]}`

	// First request: cache miss → calls Generate, populates cache.
	req1 := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("Authorization", authHeader())
	resp1, err := app.Test(req1, 5000)
	require.NoError(t, err)
	require.Equal(t, 200, resp1.StatusCode)
	assert.Equal(t, int32(1), generateCalls.Load(), "first request must call Generate")

	// Second request: same message → cache hit.
	req2 := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", authHeader())
	resp2, err := app.Test(req2, 5000)
	require.NoError(t, err)
	require.Equal(t, 200, resp2.StatusCode)

	assert.Equal(t, "semantic", resp2.Header.Get("X-CAW-Cache-Hit"), "second request must be a semantic cache hit")
	assert.Equal(t, int32(1), generateCalls.Load(), "backend.Generate must NOT be called on cache hit")

	raw, _ := io.ReadAll(resp2.Body)
	assert.Contains(t, string(raw), `"choices"`, "cached response body must be valid JSON")
}

// TestChatComplete_SemanticCacheMiss_CallsBackend verifies that when no cache
// entry is similar enough, backend.Generate is called and no cache header is set.
func TestChatComplete_SemanticCacheMiss_CallsBackend(t *testing.T) {
	var generateCalls atomic.Int32
	backend := &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, req *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			generateCalls.Add(1)
			return &adapter.GenerateResponse{
				ID:    "id-1",
				Model: req.Model,
				Choices: []adapter.Choice{
					{Index: 0, Message: adapter.Message{Role: "assistant", Content: "answer"}, FinishReason: "stop"},
				},
			}, nil
		},
	}

	// Two different embeddings: orthogonal → no cache hit.
	callCount := 0
	embeddings := [][]float32{{1, 0}, {0, 1}}
	ec := &mockEmbedClient{}
	srv := newTestServerWithEmbed(t, backend, ec)
	app := srv.App()

	bodies := []string{
		`{"model":"gemma:2b","messages":[{"role":"user","content":"question A"}]}`,
		`{"model":"gemma:2b","messages":[{"role":"user","content":"question B"}]}`,
	}

	for i, body := range bodies {
		ec.embedding = embeddings[callCount]
		callCount++
		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", authHeader())
		resp, err := app.Test(req, 5000)
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)
		assert.Empty(t, resp.Header.Get("X-CAW-Cache-Hit"), "request %d must not be a cache hit", i)
	}

	assert.Equal(t, int32(2), generateCalls.Load(), "both requests must call backend.Generate")
}
