package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	ingestStream    = "caw:ingest:stream"
	statusKeyPrefix = "caw:ingest:status:"
	statusTTL       = 24 * time.Hour
)

// Enqueue places an IngestJob onto the Redis Stream and sets its initial status to "pending".
func Enqueue(ctx context.Context, rdb *redis.Client, job IngestJob) error {
	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal job: %w", err)
	}

	_, err = rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: ingestStream,
		Values: map[string]interface{}{"job": string(data)},
	}).Result()
	if err != nil {
		return fmt.Errorf("xadd ingest stream: %w", err)
	}

	status := IngestStatus{
		DocumentID: job.DocumentID,
		Status:     "pending",
		UpdatedAt:  time.Now(),
	}
	statusData, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("marshal status: %w", err)
	}

	key := statusKeyPrefix + job.DocumentID
	if err := rdb.Set(ctx, key, string(statusData), statusTTL).Err(); err != nil {
		return fmt.Errorf("set status: %w", err)
	}
	return nil
}

// GetStatus retrieves the current IngestStatus for a document from Redis.
// Returns an error if the key is not found.
func GetStatus(ctx context.Context, rdb *redis.Client, documentID string) (*IngestStatus, error) {
	key := statusKeyPrefix + documentID
	raw, err := rdb.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, fmt.Errorf("status not found for document %q", documentID)
	}
	if err != nil {
		return nil, fmt.Errorf("get status: %w", err)
	}

	var s IngestStatus
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return nil, fmt.Errorf("unmarshal status: %w", err)
	}
	return &s, nil
}

// updateStatus writes a new IngestStatus to Redis with a 24h TTL.
func updateStatus(ctx context.Context, rdb *redis.Client, docID, status, errDetail string) error {
	s := IngestStatus{
		DocumentID:  docID,
		Status:      status,
		ErrorDetail: errDetail,
		UpdatedAt:   time.Now(),
	}
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal status: %w", err)
	}
	return rdb.Set(ctx, statusKeyPrefix+docID, string(data), statusTTL).Err()
}
