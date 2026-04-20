package embed_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/caw/wrapper/internal/embed"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbedClient_CachesResult(t *testing.T) {
	var callCount int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/embed" {
			atomic.AddInt64(&callCount, 1)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"embedding": []float32{0.1, 0.2, 0.3},
			})
		} else if r.URL.Path == "/health" {
			json.NewEncoder(w).Encode(map[string]string{"status": "SERVING"})
		}
	}))
	defer server.Close()

	client := embed.NewHTTPEmbedClient(server.URL)
	ctx := context.Background()

	result1, err := client.Embed(ctx, "hello world")
	require.NoError(t, err)

	result2, err := client.Embed(ctx, "hello world")
	require.NoError(t, err)

	assert.Equal(t, result1, result2)
	assert.Equal(t, int64(1), atomic.LoadInt64(&callCount), "should only call HTTP service once for the same text")
}

func TestEmbedClient_CacheMiss_CallsService(t *testing.T) {
	var callCount int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/embed" {
			atomic.AddInt64(&callCount, 1)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"embedding": []float32{0.1, 0.2, 0.3},
			})
		}
	}))
	defer server.Close()

	client := embed.NewHTTPEmbedClient(server.URL)
	ctx := context.Background()

	_, err := client.Embed(ctx, "text one")
	require.NoError(t, err)

	_, err = client.Embed(ctx, "text two")
	require.NoError(t, err)

	assert.Equal(t, int64(2), atomic.LoadInt64(&callCount), "should call service for each unique text")
}

func TestEmbedClient_CircuitOpen_SkipsService(t *testing.T) {
	var callCount int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/embed" {
			atomic.AddInt64(&callCount, 1)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	client := embed.NewHTTPEmbedClient(server.URL)
	ctx := context.Background()

	// Trip the circuit with 3 failures (each unique text to avoid cache)
	for i := 0; i < 3; i++ {
		_, _ = client.Embed(ctx, fmt.Sprintf("unique text %d", i))
	}

	atomic.StoreInt64(&callCount, 0)

	_, err := client.Embed(ctx, "another unique text")
	assert.ErrorIs(t, err, embed.ErrCircuitOpen)
	assert.Equal(t, int64(0), atomic.LoadInt64(&callCount), "circuit open: no HTTP call should be made")
}
