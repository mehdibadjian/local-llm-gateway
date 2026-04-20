package rag_test

import (
	"testing"

	"github.com/caw/wrapper/internal/rag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReranker_SkippedForInteractiveTurn(t *testing.T) {
	results := []rag.RetrievalResult{
		{ChunkID: "a", Score: 0.5},
		{ChunkID: "b", Score: 0.9},
		{ChunkID: "c", Score: 0.3},
	}
	original := make([]rag.RetrievalResult, len(results))
	copy(original, results)
	cfg := rag.RerankerConfig{AgentMode: false, IsAsync: false}
	got := rag.Rerank(results, cfg)
	require.Len(t, got, 3)
	for i := range original {
		assert.Equal(t, original[i].ChunkID, got[i].ChunkID)
	}
}

func TestReranker_AppliedForAgentMode(t *testing.T) {
	results := []rag.RetrievalResult{
		{ChunkID: "a", Score: 0.5},
		{ChunkID: "b", Score: 0.9},
		{ChunkID: "c", Score: 0.3},
	}
	cfg := rag.RerankerConfig{AgentMode: true, IsAsync: false}
	got := rag.Rerank(results, cfg)
	require.Len(t, got, 3)
	assert.Equal(t, "b", got[0].ChunkID)
	assert.Equal(t, "a", got[1].ChunkID)
	assert.Equal(t, "c", got[2].ChunkID)
}

func TestReranker_AppliedForAsyncTask(t *testing.T) {
	results := []rag.RetrievalResult{
		{ChunkID: "x", Score: 0.1},
		{ChunkID: "y", Score: 0.7},
	}
	cfg := rag.RerankerConfig{AgentMode: false, IsAsync: true}
	got := rag.Rerank(results, cfg)
	require.Len(t, got, 2)
	assert.Equal(t, "y", got[0].ChunkID)
	assert.Equal(t, "x", got[1].ChunkID)
}
