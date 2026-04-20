package ingest_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caw/wrapper/internal/ingest"
)

func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	return redis.NewClient(&redis.Options{Addr: mr.Addr()})
}

func TestEnqueue_AddsToStream(t *testing.T) {
	rdb := newTestRedis(t)
	ctx := context.Background()

	job := ingest.IngestJob{
		DocumentID: "doc-1",
		Domain:     "general",
		Content:    "hello world",
		EnqueuedAt: time.Now(),
	}

	err := ingest.Enqueue(ctx, rdb, job)
	require.NoError(t, err)

	length, err := rdb.XLen(ctx, "caw:ingest:stream").Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), length)
}

func TestEnqueue_SetsInitialStatus(t *testing.T) {
	rdb := newTestRedis(t)
	ctx := context.Background()

	job := ingest.IngestJob{
		DocumentID: "doc-2",
		Domain:     "general",
		Content:    "hello",
		EnqueuedAt: time.Now(),
	}

	err := ingest.Enqueue(ctx, rdb, job)
	require.NoError(t, err)

	status, err := ingest.GetStatus(ctx, rdb, "doc-2")
	require.NoError(t, err)
	assert.Equal(t, "pending", status.Status)
	assert.Equal(t, "doc-2", status.DocumentID)
}

func TestEnqueue_CompletesQuickly(t *testing.T) {
	rdb := newTestRedis(t)
	ctx := context.Background()

	job := ingest.IngestJob{
		DocumentID: "doc-3",
		Domain:     "general",
		Content:    "test",
		EnqueuedAt: time.Now(),
	}

	start := time.Now()
	err := ingest.Enqueue(ctx, rdb, job)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 50*time.Millisecond, "enqueue should complete within 50ms")
}

func TestGetStatus_ReflectsCurrentState(t *testing.T) {
	rdb := newTestRedis(t)
	ctx := context.Background()

	status := ingest.IngestStatus{
		DocumentID: "doc-4",
		Status:     "processing",
		UpdatedAt:  time.Now(),
	}
	data, _ := json.Marshal(status)
	err := rdb.Set(ctx, "caw:ingest:status:doc-4", string(data), 24*time.Hour).Err()
	require.NoError(t, err)

	got, err := ingest.GetStatus(ctx, rdb, "doc-4")
	require.NoError(t, err)
	assert.Equal(t, "processing", got.Status)
}

func TestGetStatus_NotFound(t *testing.T) {
	rdb := newTestRedis(t)
	ctx := context.Background()

	_, err := ingest.GetStatus(ctx, rdb, "nonexistent")
	assert.Error(t, err)
}
