package ingest_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caw/wrapper/internal/ingest"
)

func TestDLQ_MovesToDLQAfter3Failures(t *testing.T) {
	rdb := newTestRedis(t)
	ctx := context.Background()

	job := ingest.IngestJob{
		DocumentID: "doc-dlq",
		Domain:     "general",
		Content:    "test content",
		EnqueuedAt: time.Now(),
		RetryCount: 0,
	}

	// First failure — re-enqueue
	err := ingest.SendToDLQ(ctx, rdb, job, "embed error 1")
	require.NoError(t, err)

	depth, err := ingest.DLQDepth(ctx, rdb)
	require.NoError(t, err)
	assert.Equal(t, int64(1), depth)

	// Read the job back and increment retry
	job.RetryCount = 1
	err = ingest.SendToDLQ(ctx, rdb, job, "embed error 2")
	require.NoError(t, err)

	depth, err = ingest.DLQDepth(ctx, rdb)
	require.NoError(t, err)
	assert.Equal(t, int64(2), depth)

	// Third failure — should NOT re-enqueue, should set failed status
	job.RetryCount = 2
	err = ingest.SendToDLQ(ctx, rdb, job, "embed error 3")
	require.NoError(t, err)

	// DLQ depth should still be 2 (no new message on 3rd failure)
	depth, err = ingest.DLQDepth(ctx, rdb)
	require.NoError(t, err)
	assert.Equal(t, int64(2), depth)
}

func TestDLQ_SetsFailedStatus(t *testing.T) {
	rdb := newTestRedis(t)
	ctx := context.Background()

	job := ingest.IngestJob{
		DocumentID: "doc-failed",
		Domain:     "general",
		Content:    "test",
		EnqueuedAt: time.Now(),
		RetryCount: 2, // already at max
	}

	// First enqueue to set pending status
	require.NoError(t, ingest.Enqueue(ctx, rdb, job))

	// Simulate 3rd failure
	err := ingest.SendToDLQ(ctx, rdb, job, "final error message")
	require.NoError(t, err)

	status, err := ingest.GetStatus(ctx, rdb, "doc-failed")
	require.NoError(t, err)
	assert.Equal(t, "failed", status.Status)
	assert.Equal(t, "final error message", status.ErrorDetail)
}

func TestDLQ_DLQDepthReported(t *testing.T) {
	rdb := newTestRedis(t)
	ctx := context.Background()

	// Initially empty
	depth, err := ingest.DLQDepth(ctx, rdb)
	require.NoError(t, err)
	assert.Equal(t, int64(0), depth)

	// Add some entries
	for i := 0; i < 5; i++ {
		job := ingest.IngestJob{
			DocumentID: "doc-" + string(rune('a'+i)),
			Domain:     "general",
			Content:    "test",
			EnqueuedAt: time.Now(),
			RetryCount: 0,
		}
		require.NoError(t, ingest.SendToDLQ(ctx, rdb, job, "error"))
	}

	depth, err = ingest.DLQDepth(ctx, rdb)
	require.NoError(t, err)
	assert.Equal(t, int64(5), depth)
}
