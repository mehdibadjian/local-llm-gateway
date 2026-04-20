package ingest

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

const dlqStream = "caw:ingest:dlq"

// SendToDLQ is the exported entry point for the DLQ logic (used by tests and external callers).
func SendToDLQ(ctx context.Context, rdb *redis.Client, job IngestJob, errMsg string) error {
	return sendToDLQ(ctx, rdb, job, errMsg)
}

// sendToDLQ handles retry logic: increments RetryCount, re-enqueues if < 3, else marks failed.
func sendToDLQ(ctx context.Context, rdb *redis.Client, job IngestJob, errMsg string) error {
	job.RetryCount++

	if job.RetryCount >= 3 {
		// Exhausted retries — mark as permanently failed
		return updateStatus(ctx, rdb, job.DocumentID, "failed", errMsg)
	}

	// Re-enqueue onto the DLQ stream for later retry
	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal dlq job: %w", err)
	}

	_, err = rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: dlqStream,
		Values: map[string]interface{}{"job": string(data)},
	}).Result()
	if err != nil {
		return fmt.Errorf("xadd dlq: %w", err)
	}
	return nil
}

// DLQDepth returns the number of messages currently in the DLQ stream.
func DLQDepth(ctx context.Context, rdb *redis.Client) (int64, error) {
	n, err := rdb.XLen(ctx, dlqStream).Result()
	if err != nil {
		return 0, fmt.Errorf("xlen dlq: %w", err)
	}
	return n, nil
}
