package ingest_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caw/wrapper/internal/ingest"
	"github.com/caw/wrapper/internal/memory"
)

// mockEmbedClient returns a fixed 384-dim vector.
type mockEmbedClient struct {
	callCount int64
	delay     time.Duration
}

func (m *mockEmbedClient) Embed(ctx context.Context, text string) ([]float32, error) {
	atomic.AddInt64(&m.callCount, 1)
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	vec := make([]float32, 384)
	for i := range vec {
		vec[i] = 0.1
	}
	return vec, nil
}

func (m *mockEmbedClient) HealthCheck(ctx context.Context) error { return nil }

// mockQdrant always succeeds.
type mockQdrant struct {
	mu     sync.Mutex
	points []memory.QdrantPoint
}

func (m *mockQdrant) Upsert(ctx context.Context, domain string, points []memory.QdrantPoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.points = append(m.points, points...)
	return nil
}

// mockPG always succeeds.
type mockPG struct {
	mu     sync.Mutex
	chunks []memory.Chunk
}

func (m *mockPG) UpsertChunk(ctx context.Context, chunk memory.Chunk) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chunks = append(m.chunks, chunk)
	return nil
}

func newWorkerTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	return redis.NewClient(&redis.Options{Addr: mr.Addr()})
}

func TestWorker_SemaphoreLimits4Concurrent(t *testing.T) {
	ec := &mockEmbedClient{delay: 10 * time.Millisecond}
	q := &mockQdrant{}
	pg := &mockPG{}
	rdb := newWorkerTestRedis(t)

	worker := ingest.NewIngestWorker(rdb, ec, q, pg)

	job := ingest.IngestJob{
		DocumentID: "doc-sem",
		Domain:     "general",
		Content:    generateWords(5000), // ~10 chunks
		EnqueuedAt: time.Now(),
	}

	ctx := context.Background()
	err := worker.ProcessJobForTest(ctx, job)
	require.NoError(t, err)

	assert.Equal(t, int64(0), int64(worker.SemaphoreLen()))
	assert.Greater(t, ec.callCount, int64(0))
}

func TestWorker_IncrVersionAfterIngest(t *testing.T) {
	ec := &mockEmbedClient{}
	q := &mockQdrant{}
	pg := &mockPG{}
	rdb := newWorkerTestRedis(t)

	worker := ingest.NewIngestWorker(rdb, ec, q, pg)

	job := ingest.IngestJob{
		DocumentID: "doc-version",
		Domain:     "general",
		Content:    "some content here to process",
		EnqueuedAt: time.Now(),
	}

	ctx := context.Background()
	err := worker.ProcessJobForTest(ctx, job)
	require.NoError(t, err)

	val, err := rdb.Get(ctx, "caw:retrieval:general:version").Result()
	require.NoError(t, err)
	assert.Equal(t, "1", val)
}

func TestWorker_ProcessesChunksAndUpserts(t *testing.T) {
	ec := &mockEmbedClient{}
	q := &mockQdrant{}
	pg := &mockPG{}
	rdb := newWorkerTestRedis(t)

	worker := ingest.NewIngestWorker(rdb, ec, q, pg)

	job := ingest.IngestJob{
		DocumentID: "doc-chunks",
		Domain:     "general",
		Content:    generateWords(600), // ~2 chunks
		EnqueuedAt: time.Now(),
	}

	ctx := context.Background()
	err := worker.ProcessJobForTest(ctx, job)
	require.NoError(t, err)

	assert.Greater(t, len(q.points), 0, "should have upserted Qdrant points")
	assert.Greater(t, len(pg.chunks), 0, "should have upserted PG chunks")
	assert.Equal(t, len(q.points), len(pg.chunks), "Qdrant and PG counts should match")
}

func generateWords(n int) string {
	words := make([]byte, 0, n*5)
	word := []byte("word ")
	for i := 0; i < n; i++ {
		words = append(words, word...)
	}
	return string(words)
}
