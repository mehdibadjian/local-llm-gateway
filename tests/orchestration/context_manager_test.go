package orchestration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/caw/wrapper/internal/adapter"
	"github.com/caw/wrapper/internal/memory"
	"github.com/caw/wrapper/internal/orchestration"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupMiniredis starts a miniredis server and returns a go-redis client connected to it.
func setupMiniredis(t *testing.T) (*miniredis.Miniredis, redis.UniversalClient) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return mr, rdb
}

// seedHistory populates session history in miniredis with n messages, each with
// `wordsPerMsg` words so that total tokens = n * wordsPerMsg.
func seedHistory(t *testing.T, mr *miniredis.Miniredis, rdb redis.UniversalClient, sessionID string, n, wordsPerMsg int) {
	t.Helper()
	listKey := fmt.Sprintf("caw:session:%s:messages", sessionID)
	word := strings.Repeat("word ", wordsPerMsg)
	for i := 0; i < n; i++ {
		msg := memory.Message{Role: "user", Content: strings.TrimSpace(word)}
		data, err := json.Marshal(msg)
		require.NoError(t, err)
		require.NoError(t, rdb.RPush(context.Background(), listKey, data).Err())
	}
	_ = mr // kept for symmetry; used in fast-forward helpers
}

// noopBackend returns a mock backend that echoes a fixed response.
func noopBackend(content string) *adapter.MockInferenceBackend {
	return &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, req *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			return &adapter.GenerateResponse{
				Choices: []adapter.Choice{{Message: adapter.Message{Role: "assistant", Content: content}}},
			}, nil
		},
	}
}

// --- Tests ---

func TestContextManager_LoadsBelowThreshold_NoCompression(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	// Seed 10 messages × 10 words = 100 tokens (well below 3000).
	seedHistory(t, mr, rdb, "sess-1", 10, 10)

	store := memory.NewSessionStore(rdb)
	var generateCalls int32
	backend := &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, req *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			atomic.AddInt32(&generateCalls, 1)
			return &adapter.GenerateResponse{
				Choices: []adapter.Choice{{Message: adapter.Message{Role: "assistant", Content: "summary"}}},
			}, nil
		},
	}
	cm := orchestration.NewContextManager(store, rdb, backend)

	msgs, err := cm.LoadAndManage(context.Background(), "sess-1")

	require.NoError(t, err)
	assert.Len(t, msgs, 10)
	assert.Equal(t, int32(0), generateCalls, "backend must not be called below threshold")
}

func TestContextManager_HardTruncate_ReducesToTarget(t *testing.T) {
	// Build 100 messages × 50 words = 5000 tokens.
	messages := make([]adapter.Message, 100)
	for i := range messages {
		messages[i] = adapter.Message{Role: "user", Content: strings.Repeat("word ", 50)}
	}

	result := orchestration.HardTruncate(messages, orchestration.CompressionTarget)

	total := 0
	for _, m := range result {
		total += len(strings.Fields(m.Content))
	}
	assert.LessOrEqual(t, total, orchestration.CompressionTarget,
		"hard truncation must bring token count to ≤ %d, got %d", orchestration.CompressionTarget, total)
}

func TestContextManager_CompressLock_OnlyOneWinner(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	// Seed 100 messages × 31 words = 3100 tokens (above 3000 threshold).
	seedHistory(t, mr, rdb, "sess-2", 100, 31)

	store := memory.NewSessionStore(rdb)

	var generateCalls int32
	backend := &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, req *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			atomic.AddInt32(&generateCalls, 1)
			return &adapter.GenerateResponse{
				Choices: []adapter.Choice{{Message: adapter.Message{Role: "assistant", Content: "compressed summary"}}},
			}, nil
		},
	}
	cm := orchestration.NewContextManager(store, rdb, backend)

	var wg sync.WaitGroup
	wg.Add(2)
	errs := make([]error, 2)

	for i := 0; i < 2; i++ {
		i := i
		go func() {
			defer wg.Done()
			_, errs[i] = cm.LoadAndManage(context.Background(), "sess-2")
		}()
	}
	wg.Wait()

	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	assert.Equal(t, int32(1), generateCalls,
		"exactly one goroutine must win the compression lock; got %d compress calls", generateCalls)
}

func TestContextManager_LockExpiry_LoserTruncates(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	// Seed 110 messages × 31 words = 3410 tokens.
	seedHistory(t, mr, rdb, "sess-3", 110, 31)

	store := memory.NewSessionStore(rdb)

	// Pre-set the lock so our goroutine is always the loser.
	lockKey := "caw:compression:sess-3"
	require.NoError(t, rdb.Set(context.Background(), lockKey, "1", orchestration.LockTTL).Err())
	// Fast-forward miniredis time so the lock has already expired after LockWaitTimeout.
	mr.FastForward(orchestration.LockTTL + orchestration.LockWaitTimeout)

	cm := orchestration.NewContextManager(store, rdb, noopBackend("summary"))

	msgs, err := cm.LoadAndManage(context.Background(), "sess-3")

	require.NoError(t, err)
	total := 0
	for _, m := range msgs {
		total += len(strings.Fields(m.Content))
	}
	assert.LessOrEqual(t, total, orchestration.CompressionTarget,
		"loser must hard-truncate to ≤ %d tokens after lock expiry, got %d", orchestration.CompressionTarget, total)
}

func TestContextManager_CompressedSession_BelowThreshold(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	// Start above threshold.
	seedHistory(t, mr, rdb, "sess-4", 100, 31)

	store := memory.NewSessionStore(rdb)
	backend := noopBackend("this is a short summary")
	cm := orchestration.NewContextManager(store, rdb, backend)

	// First call: compresses.
	_, err := cm.LoadAndManage(context.Background(), "sess-4")
	require.NoError(t, err)

	// Second call: should reload the compressed history (4 tokens) — below threshold.
	msgs, err := cm.LoadAndManage(context.Background(), "sess-4")
	require.NoError(t, err)

	total := 0
	for _, m := range msgs {
		total += len(strings.Fields(m.Content))
	}
	assert.Less(t, total, orchestration.CompressionThreshold,
		"after compression, second load should be below threshold (%d), got %d", orchestration.CompressionThreshold, total)
}
