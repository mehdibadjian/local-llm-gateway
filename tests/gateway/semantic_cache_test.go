package gateway_test

import (
	"testing"

	"github.com/caw/wrapper/internal/gateway"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── SemanticCache unit tests ─────────────────────────────────────────────────

func TestSemanticCache_Miss_EmptyCache(t *testing.T) {
	c := gateway.NewSemanticCache(256)
	resp, ok := c.Lookup([]float32{1, 0, 0}, 0.95)
	assert.False(t, ok)
	assert.Equal(t, "", resp)
}

func TestSemanticCache_Hit_IdenticalEmbedding(t *testing.T) {
	c := gateway.NewSemanticCache(256)
	emb := []float32{0.6, 0.8}
	c.Store(emb, "cached answer")

	resp, ok := c.Lookup(emb, 0.95)
	require.True(t, ok)
	assert.Equal(t, "cached answer", resp)
}

func TestSemanticCache_Miss_LowSimilarity(t *testing.T) {
	c := gateway.NewSemanticCache(256)
	c.Store([]float32{1, 0}, "stored")

	// Orthogonal vector → similarity 0.0
	resp, ok := c.Lookup([]float32{0, 1}, 0.95)
	assert.False(t, ok)
	assert.Equal(t, "", resp)
}

func TestSemanticCache_Hit_HighSimilarity(t *testing.T) {
	c := gateway.NewSemanticCache(256)
	// Slightly perturbed vector, still very similar to [1, 0]
	c.Store([]float32{1.0, 0.0}, "stored")

	// Near-identical: sim ≈ 0.9999
	resp, ok := c.Lookup([]float32{0.9999, 0.01}, 0.95)
	require.True(t, ok)
	assert.Equal(t, "stored", resp)
}

func TestSemanticCache_Eviction_BoundedToMaxSize(t *testing.T) {
	const maxSize = 256
	c := gateway.NewSemanticCache(maxSize)

	for i := 0; i < maxSize+1; i++ {
		c.Store([]float32{float32(i), 0}, "r")
	}
	assert.Equal(t, maxSize, c.Len())
}

// ── CosineSimilarity unit tests ───────────────────────────────────────────────

func TestCosineSimilarity_IdenticalVectors(t *testing.T) {
	sim := gateway.CosineSimilarity([]float32{1, 0}, []float32{1, 0})
	assert.InDelta(t, 1.0, sim, 1e-6)
}

func TestCosineSimilarity_OrthogonalVectors(t *testing.T) {
	sim := gateway.CosineSimilarity([]float32{1, 0}, []float32{0, 1})
	assert.InDelta(t, 0.0, sim, 1e-6)
}

func TestCosineSimilarity_ZeroVector(t *testing.T) {
	sim := gateway.CosineSimilarity([]float32{0, 0}, []float32{1, 0})
	assert.Equal(t, float32(0.0), sim)
}
