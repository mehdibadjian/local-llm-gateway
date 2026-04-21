package memory_test

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/caw/wrapper/internal/adapter"
	"github.com/caw/wrapper/internal/memory"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestHistoryManager(t *testing.T) (*memory.HistoryManager, *memory.SessionStore, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { client.Close() })

	store := memory.NewSessionStore(client)
	hm := memory.NewHistoryManager(store)
	return hm, store, mr
}

func TestHistoryManager_TokenEstimate(t *testing.T) {
	got := memory.EstimateTokens("hello world")
	assert.Equal(t, 2, got, "EstimateTokens should return len(text)/4")
}

func TestHistoryManager_UnderBudget_ReturnsUnchanged(t *testing.T) {
	hm, store, _ := newTestHistoryManager(t)
	ctx := context.Background()
	sessionID := "hm-under-budget"

	msgs := []memory.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
		{Role: "user", Content: "how are you?"},
	}
	for _, m := range msgs {
		require.NoError(t, store.SaveMessage(ctx, sessionID, m))
	}

	generateCalled := false
	backend := &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, _ *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			generateCalled = true
			return &adapter.GenerateResponse{
				Choices: []adapter.Choice{{Message: adapter.Message{Role: "assistant", Content: "summary"}}},
			}, nil
		},
	}

	result, err := hm.LoadAndTrim(ctx, sessionID, backend)
	require.NoError(t, err)
	assert.Len(t, result, 3, "should return all 3 messages unchanged")
	assert.False(t, generateCalled, "backend.Generate must NOT be called when under budget")
}

func TestHistoryManager_OverBudget_Summarises(t *testing.T) {
	hm, store, _ := newTestHistoryManager(t)
	ctx := context.Background()
	sessionID := "hm-over-budget"

	// 8001 chars / 4 = 2000.25 → 2000 tokens, which is NOT > 2000.
	// Need >8000 chars total across role+content. Use 8001+ chars content alone.
	longContent := strings.Repeat("a", 8001)
	msgs := []memory.Message{
		{Role: "user", Content: longContent},
	}
	for _, m := range msgs {
		require.NoError(t, store.SaveMessage(ctx, sessionID, m))
	}

	generateCallCount := 0
	backend := &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, req *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			generateCallCount++
			return &adapter.GenerateResponse{
				Choices: []adapter.Choice{{Message: adapter.Message{Role: "assistant", Content: `{"summary":"test"}`}}},
			}, nil
		},
	}

	result, err := hm.LoadAndTrim(ctx, sessionID, backend)
	require.NoError(t, err)
	assert.Equal(t, 1, generateCallCount, "backend.Generate should be called exactly once")
	require.Len(t, result, 1, "should return a single summary message")
	assert.Equal(t, "system", result[0].Role)
	assert.Equal(t, `{"summary":"test"}`, result[0].Content)
}

func TestHistoryManager_OverBudget_StoresSummary(t *testing.T) {
	hm, store, _ := newTestHistoryManager(t)
	ctx := context.Background()
	sessionID := "hm-stores-summary"

	longContent := strings.Repeat("b", 8001)
	require.NoError(t, store.SaveMessage(ctx, sessionID, memory.Message{Role: "user", Content: longContent}))

	backend := &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, _ *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			return &adapter.GenerateResponse{
				Choices: []adapter.Choice{{Message: adapter.Message{Role: "assistant", Content: `{"key":"value"}`}}},
			}, nil
		},
	}

	_, err := hm.LoadAndTrim(ctx, sessionID, backend)
	require.NoError(t, err)

	// After summarisation, the session must contain exactly one message: the summary.
	stored, err := store.LoadHistory(ctx, sessionID)
	require.NoError(t, err)
	require.Len(t, stored, 1, "session should contain only the summary message after trim")
	assert.Equal(t, "system", stored[0].Role)
	assert.Equal(t, `{"key":"value"}`, stored[0].Content)
}
