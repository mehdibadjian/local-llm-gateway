package ingest

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/caw/wrapper/internal/embed"
	"github.com/caw/wrapper/internal/memory"
)

const (
	embedConcurrency  = 4
	wordsPerChunk     = 500
	consumerGroup     = "caw-workers"
	versionKeyFmt     = "caw:retrieval:%s:version"
)

// QdrantUpserter is the interface used by IngestWorker to write vectors.
type QdrantUpserter interface {
	Upsert(ctx context.Context, domain string, points []memory.QdrantPoint) error
}

// PGUpserter is the interface used by IngestWorker to write chunk metadata.
type PGUpserter interface {
	UpsertChunk(ctx context.Context, chunk memory.Chunk) error
}

// IngestWorker reads jobs from the Redis Stream, embeds chunks, and writes to Qdrant + PG.
type IngestWorker struct {
	rdb         *redis.Client
	embedClient embed.EmbedClient
	qdrant      QdrantUpserter
	pg          PGUpserter
	semaphore   chan struct{}
}

// NewIngestWorker constructs an IngestWorker with a semaphore capped at EMBED_CONCURRENCY (4).
func NewIngestWorker(rdb *redis.Client, ec embed.EmbedClient, q QdrantUpserter, pg PGUpserter) *IngestWorker {
	return &IngestWorker{
		rdb:         rdb,
		embedClient: ec,
		qdrant:      q,
		pg:          pg,
		semaphore:   make(chan struct{}, embedConcurrency),
	}
}

// ProcessJobForTest is a public alias for processJob, used in tests.
func (w *IngestWorker) ProcessJobForTest(ctx context.Context, job IngestJob) error {
	return w.processJob(ctx, job)
}

// SemaphoreLen returns the current number of held semaphore slots (should be 0 after job).
func (w *IngestWorker) SemaphoreLen() int {
	return len(w.semaphore)
}

// chunkContent splits content into slices of ~500 words.
func (w *IngestWorker) chunkContent(content string) []string {
	words := strings.Fields(content)
	if len(words) == 0 {
		return nil
	}
	var chunks []string
	for i := 0; i < len(words); i += wordsPerChunk {
		end := i + wordsPerChunk
		if end > len(words) {
			end = len(words)
		}
		chunks = append(chunks, strings.Join(words[i:end], " "))
	}
	return chunks
}

// processJob embeds all chunks of a job and upserts them into Qdrant and PG.
func (w *IngestWorker) processJob(ctx context.Context, job IngestJob) error {
	if err := updateStatus(ctx, w.rdb, job.DocumentID, "processing", ""); err != nil {
		return fmt.Errorf("set processing status: %w", err)
	}

	chunks := w.chunkContent(job.Content)
	if len(chunks) == 0 {
		chunks = []string{job.Content}
	}

	type chunkResult struct {
		index  int
		chunk  string
		vector []float32
		err    error
	}

	results := make([]chunkResult, len(chunks))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for i, chunk := range chunks {
		wg.Add(1)
		go func(idx int, text string) {
			defer wg.Done()
			// Acquire semaphore
			w.semaphore <- struct{}{}
			vec, err := w.embedClient.Embed(ctx, text)
			// Release semaphore
			<-w.semaphore

			mu.Lock()
			defer mu.Unlock()
			if err != nil && firstErr == nil {
				firstErr = err
			}
			results[idx] = chunkResult{index: idx, chunk: text, vector: vec, err: err}
		}(i, chunk)
	}
	wg.Wait()

	if firstErr != nil {
		return fmt.Errorf("embed chunk: %w", firstErr)
	}

	// Upsert all chunks sequentially into Qdrant + PG
	for _, r := range results {
		pointID := uuid.New().String()
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(r.chunk)))

		point := memory.QdrantPoint{
			ID:     pointID,
			Vector: r.vector,
			Payload: map[string]interface{}{
				"document_id": job.DocumentID,
				"chunk_index": r.index,
				"domain":      job.Domain,
			},
		}
		if err := w.qdrant.Upsert(ctx, job.Domain, []memory.QdrantPoint{point}); err != nil {
			return fmt.Errorf("qdrant upsert chunk %d: %w", r.index, err)
		}

		pgChunk := memory.Chunk{
			DocumentID:     job.DocumentID,
			ChunkIndex:     r.index,
			Content:        r.chunk,
			ContentHash:    hash,
			QdrantPointID:  pointID,
			Domain:         job.Domain,
			EmbeddingModel: "all-MiniLM-L6-v2",
		}
		if err := w.pg.UpsertChunk(ctx, pgChunk); err != nil {
			return fmt.Errorf("pg upsert chunk %d: %w", r.index, err)
		}
	}

	// Increment retrieval version counter and set indexed status
	versionKey := fmt.Sprintf(versionKeyFmt, job.Domain)
	if err := w.rdb.Incr(ctx, versionKey).Err(); err != nil {
		return fmt.Errorf("incr version: %w", err)
	}

	return updateStatus(ctx, w.rdb, job.DocumentID, "indexed", "")
}

// Run starts the XREADGROUP consumer loop. Blocks until ctx is cancelled.
func (w *IngestWorker) Run(ctx context.Context) error {
	// Create consumer group (ignore BUSYGROUP error)
	err := w.rdb.XGroupCreateMkStream(ctx, ingestStream, consumerGroup, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("create consumer group: %w", err)
	}

	hostname, _ := os.Hostname()
	consumerName := fmt.Sprintf("worker-%s", hostname)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		streams, err := w.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    consumerGroup,
			Consumer: consumerName,
			Streams:  []string{ingestStream, ">"},
			Count:    1,
			Block:    5000,
		}).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}

		for _, stream := range streams {
			for _, msg := range stream.Messages {
				jobData, ok := msg.Values["job"].(string)
				if !ok {
					_ = w.rdb.XAck(ctx, ingestStream, consumerGroup, msg.ID)
					continue
				}

				var job IngestJob
				if err := parseJSON(jobData, &job); err != nil {
					_ = w.rdb.XAck(ctx, ingestStream, consumerGroup, msg.ID)
					continue
				}

				if err := w.processJob(ctx, job); err != nil {
					_ = sendToDLQ(ctx, w.rdb, job, err.Error())
				} else {
					_ = w.rdb.XAck(ctx, ingestStream, consumerGroup, msg.ID)
				}
			}
		}
	}
}
