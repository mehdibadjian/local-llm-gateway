package ingest

import "time"

// IngestJob represents a document ingestion task placed on the Redis Stream.
type IngestJob struct {
	DocumentID string    `json:"document_id"`
	Domain     string    `json:"domain"`
	Content    string    `json:"content"`
	Title      string    `json:"title,omitempty"`
	EnqueuedAt time.Time `json:"enqueued_at"`
	RetryCount int       `json:"retry_count"`
}

// IngestStatus tracks the lifecycle of an ingest job stored in Redis.
type IngestStatus struct {
	DocumentID  string    `json:"document_id"`
	Status      string    `json:"status"` // pending/processing/indexed/failed
	ErrorDetail string    `json:"error_detail,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}
