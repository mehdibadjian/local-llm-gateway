package rag_test

import (
	"context"
	"testing"
	"time"

	"github.com/caw/wrapper/internal/memory"
	"github.com/caw/wrapper/internal/rag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockEmbedClient struct {
	sleepDur time.Duration
	vec      []float32
}

func (m *mockEmbedClient) Embed(ctx context.Context, text string) ([]float32, error) {
	if m.sleepDur > 0 {
		select {
		case <-time.After(m.sleepDur):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return m.vec, nil
}
func (m *mockEmbedClient) HealthCheck(ctx context.Context) error { return nil }

type mockQdrantSearcher struct {
	sleepDur time.Duration
	results  []memory.QdrantSearchResult
}

func (m *mockQdrantSearcher) Search(ctx context.Context, domain string, vector []float32, topK int) ([]memory.QdrantSearchResult, error) {
	if m.sleepDur > 0 {
		select {
		case <-time.After(m.sleepDur):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return m.results, nil
}

type mockFTSSearcher struct {
	sleepDur time.Duration
	results  []memory.Chunk
}

func (m *mockFTSSearcher) FTSSearch(ctx context.Context, domain, query string, limit int) ([]memory.Chunk, error) {
	if m.sleepDur > 0 {
		select {
		case <-time.After(m.sleepDur):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return m.results, nil
}

func TestHybridRetriever_RunsBothLegsInParallel(t *testing.T) {
	qdrant := &mockQdrantSearcher{
		sleepDur: 100 * time.Millisecond,
		results:  []memory.QdrantSearchResult{{ID: "q1", Score: 0.9, Payload: map[string]interface{}{"content": "ann"}}},
	}
	pg := &mockFTSSearcher{
		sleepDur: 100 * time.Millisecond,
		results:  []memory.Chunk{{ID: "f1", Content: "fts", Domain: "general"}},
	}
	embedClient := &mockEmbedClient{vec: make([]float32, 384)}
	r := rag.NewHybridRetriever(qdrant, pg, embedClient, nil)

	start := time.Now()
	_, err := r.Retrieve(context.Background(), "test query", "general")
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 250*time.Millisecond, "legs should run in parallel")
}

func TestHybridRetriever_ANNTimeout_UsesFTSOnly(t *testing.T) {
	qdrant := &mockQdrantSearcher{sleepDur: 400 * time.Millisecond}
	pg := &mockFTSSearcher{
		results: []memory.Chunk{{ID: "f1", Content: "fts only", Domain: "general"}},
	}
	embedClient := &mockEmbedClient{vec: make([]float32, 384)}
	r := rag.NewHybridRetriever(qdrant, pg, embedClient, nil)

	results, err := r.Retrieve(context.Background(), "test", "general")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "fts", results[0].Source)
	assert.Equal(t, "f1", results[0].ChunkID)
}

func TestHybridRetriever_FTSTimeout_UsesANNOnly(t *testing.T) {
	qdrant := &mockQdrantSearcher{
		results: []memory.QdrantSearchResult{{ID: "q1", Score: 0.9, Payload: map[string]interface{}{"content": "ann only"}}},
	}
	pg := &mockFTSSearcher{sleepDur: 400 * time.Millisecond}
	embedClient := &mockEmbedClient{vec: make([]float32, 384)}
	r := rag.NewHybridRetriever(qdrant, pg, embedClient, nil)

	results, err := r.Retrieve(context.Background(), "test", "general")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "ann", results[0].Source)
	assert.Equal(t, "q1", results[0].ChunkID)
}

func TestHybridRetriever_BothComplete_RRFMerged(t *testing.T) {
	qdrant := &mockQdrantSearcher{
		results: []memory.QdrantSearchResult{
			{ID: "q1", Score: 0.9, Payload: map[string]interface{}{"content": "ann content"}},
		},
	}
	pg := &mockFTSSearcher{
		results: []memory.Chunk{{ID: "f1", Content: "fts content", Domain: "general"}},
	}
	embedClient := &mockEmbedClient{vec: make([]float32, 384)}
	r := rag.NewHybridRetriever(qdrant, pg, embedClient, nil)

	results, err := r.Retrieve(context.Background(), "test", "general")
	require.NoError(t, err)
	require.Len(t, results, 2)

	sources := make(map[string]bool)
	for _, res := range results {
		sources[res.Source] = true
	}
	assert.True(t, sources["ann"], "expected ANN results in merged output")
	assert.True(t, sources["fts"], "expected FTS results in merged output")
}
