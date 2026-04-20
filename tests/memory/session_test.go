package memory_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caw/wrapper/internal/memory"
)

func newTestStore(t *testing.T) (*memory.SessionStore, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { client.Close() })

	return memory.NewSessionStore(client), mr
}

func TestSessionSaveAndLoad(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	sessionID := "test-session-1"

	msgs := []memory.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
		{Role: "user", Content: "how are you"},
	}

	for _, m := range msgs {
		err := store.SaveMessage(ctx, sessionID, m)
		require.NoError(t, err)
	}

	loaded, err := store.LoadHistory(ctx, sessionID)
	require.NoError(t, err)
	require.Len(t, loaded, len(msgs))
	for i, m := range msgs {
		assert.Equal(t, m.Role, loaded[i].Role)
		assert.Equal(t, m.Content, loaded[i].Content)
	}
}

func TestSessionListCap(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	sessionID := "test-cap-session"

	// Save 201 messages
	for i := 0; i < 201; i++ {
		err := store.SaveMessage(ctx, sessionID, memory.Message{
			Role:    "user",
			Content: fmt.Sprintf("message %d", i),
		})
		require.NoError(t, err)
	}

	loaded, err := store.LoadHistory(ctx, sessionID)
	require.NoError(t, err)
	assert.Len(t, loaded, 200, "list must never exceed 200 entries")

	// Verify the oldest message (index 0) was trimmed — first retained should be msg #1
	assert.Equal(t, "message 1", loaded[0].Content, "oldest message (0) should be trimmed")
}

func TestSessionTTLSliding(t *testing.T) {
	store, mr := newTestStore(t)
	ctx := context.Background()
	sessionID := "test-ttl-session"

	// Save a message to establish the session
	err := store.SaveMessage(ctx, sessionID, memory.Message{Role: "user", Content: "hello"})
	require.NoError(t, err)

	// Fast-forward time by 1 hour in miniredis
	mr.FastForward(1 * time.Hour)

	// Touch the session to reset TTL
	err = store.TouchSession(ctx, sessionID)
	require.NoError(t, err)

	// After touch, TTL should be reset to ~24h (allow small delta)
	listKey := fmt.Sprintf("caw:session:%s:messages", sessionID)
	ttl := mr.TTL(listKey)
	assert.Greater(t, ttl, 23*time.Hour, "TTL should be reset to ~24h after touch")
}

func TestSessionDelete(t *testing.T) {
	store, mr := newTestStore(t)
	ctx := context.Background()
	sessionID := "test-delete-session"

	err := store.SaveMessage(ctx, sessionID, memory.Message{Role: "user", Content: "hello"})
	require.NoError(t, err)

	// Verify keys exist before deletion
	listKey := fmt.Sprintf("caw:session:%s:messages", sessionID)
	metaKey := fmt.Sprintf("caw:session:%s:meta", sessionID)
	assert.True(t, mr.Exists(listKey), "list key should exist before delete")
	assert.True(t, mr.Exists(metaKey), "meta key should exist before delete")

	err = store.DeleteSession(ctx, sessionID)
	require.NoError(t, err)

	assert.False(t, mr.Exists(listKey), "list key should be gone after delete")
	assert.False(t, mr.Exists(metaKey), "meta key should be gone after delete")
}
