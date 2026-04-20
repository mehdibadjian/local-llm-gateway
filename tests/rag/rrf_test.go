package rag_test

import (
	"fmt"
	"testing"

	"github.com/caw/wrapper/internal/rag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRRFMerge_CombinesScores(t *testing.T) {
	ann := []rag.RetrievalResult{
		{ChunkID: "A", Content: "a", Score: 0.9, Source: "ann"},
		{ChunkID: "B", Content: "b", Score: 0.8, Source: "ann"},
		{ChunkID: "C", Content: "c", Score: 0.7, Source: "ann"},
	}
	fts := []rag.RetrievalResult{
		{ChunkID: "B", Content: "b", Score: 0.5, Source: "fts"},
		{ChunkID: "D", Content: "d", Score: 0.4, Source: "fts"},
		{ChunkID: "E", Content: "e", Score: 0.3, Source: "fts"},
	}
	merged := rag.RRFMerge(ann, fts, 10)
	require.NotEmpty(t, merged)
	// B appears in both lists (rank 2 in ANN, rank 1 in FTS) — should rank first
	assert.Equal(t, "B", merged[0].ChunkID)
	// Verify B's score = 1/(60+2) + 1/(60+1)
	expectedB := 1.0/62.0 + 1.0/61.0
	assert.InDelta(t, expectedB, merged[0].Score, 1e-9)
}

func TestRRFMerge_TopKSelection(t *testing.T) {
	ann := make([]rag.RetrievalResult, 10)
	for i := range ann {
		ann[i] = rag.RetrievalResult{ChunkID: fmt.Sprintf("a%d", i), Score: float64(10-i) * 0.1}
	}
	merged := rag.RRFMerge(ann, nil, 5)
	assert.Len(t, merged, 5)
}

func TestRRFMerge_SingleSource(t *testing.T) {
	ann := []rag.RetrievalResult{
		{ChunkID: "X", Content: "x", Score: 0.9, Source: "ann"},
		{ChunkID: "Y", Content: "y", Score: 0.8, Source: "ann"},
	}
	merged := rag.RRFMerge(ann, nil, 5)
	require.Len(t, merged, 2)
	assert.Equal(t, "X", merged[0].ChunkID)
	// X rank 1: 1/(60+1) = 1/61
	assert.InDelta(t, 1.0/61.0, merged[0].Score, 1e-9)
}
