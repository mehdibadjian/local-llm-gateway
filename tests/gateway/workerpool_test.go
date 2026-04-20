package gateway_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/caw/wrapper/internal/adapter"
	"github.com/caw/wrapper/internal/gateway"
	"github.com/caw/wrapper/internal/memory"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkerPool_Acquire_Release(t *testing.T) {
	pool := gateway.NewWorkerPool(2)

	assert.True(t, pool.Acquire())
	assert.True(t, pool.Acquire())

	pool.Release()
	pool.Release()

	// After release the slots should be reclaimable.
	assert.True(t, pool.Acquire())
	pool.Release()
}

func TestWorkerPool_InFlightCount(t *testing.T) {
	pool := gateway.NewWorkerPool(3)

	assert.Equal(t, 0, pool.InFlight())
	pool.Acquire()
	assert.Equal(t, 1, pool.InFlight())
	pool.Acquire()
	assert.Equal(t, 2, pool.InFlight())
	pool.Release()
	assert.Equal(t, 1, pool.InFlight())
	pool.Release()
	assert.Equal(t, 0, pool.InFlight())
}

func TestWorkerPool_PoolSizeFromEnv(t *testing.T) {
	t.Setenv("CAW_API_KEY", "test-key")
	t.Setenv("WORKER_POOL_SIZE", "7")

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	session := memory.NewSessionStore(rdb)
	srv := gateway.NewServer(&adapter.MockInferenceBackend{}, rdb, session)

	assert.Equal(t, 7, srv.Pool().MaxSize())
}

// TestWorkerPool_Returns429WhenFull verifies that when all pool slots are
// occupied a new HTTP request immediately receives HTTP 429.
func TestWorkerPool_Returns429WhenFull(t *testing.T) {
	t.Setenv("CAW_API_KEY", "test-key")
	t.Setenv("WORKER_POOL_SIZE", "1")

	// Channels to synchronize the blocking first request.
	poolAcquired := make(chan struct{}, 1)
	releaseFirst := make(chan struct{})

	mock := &adapter.MockInferenceBackend{
		GenerateFn: func(ctx context.Context, req *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			poolAcquired <- struct{}{} // notify main goroutine
			<-releaseFirst            // block until test releases
			return &adapter.GenerateResponse{
				ID:     "test",
				Object: "chat.completion",
				Model:  req.Model,
				Choices: []adapter.Choice{
					{Index: 0, Message: adapter.Message{Role: "assistant", Content: "hi"}, FinishReason: "stop"},
				},
			}, nil
		},
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	session := memory.NewSessionStore(rdb)
	srv := gateway.NewServer(mock, rdb, session)
	app := srv.App()

	reqBody := `{"model":"gemma:2b","messages":[{"role":"user","content":"hello"}]}`

	// First request occupies the single pool slot and blocks.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", authHeader())
		app.Test(req, -1) //nolint:errcheck
	}()

	// Wait until the first request has acquired the pool slot.
	select {
	case <-poolAcquired:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for first request to acquire pool slot")
	}

	// Second request must be rejected with 429.
	req2 := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(reqBody))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", authHeader())
	resp, err := app.Test(req2, 3000)
	require.NoError(t, err)
	assert.Equal(t, 429, resp.StatusCode)

	// Unblock the first request so the goroutine can finish.
	close(releaseFirst)
	wg.Wait()
}
