package gateway

import (
	"math"
	"sync"
)

type semanticEntry struct {
	embedding []float32
	response  string
}

// SemanticCache is an in-process, bounded LRU cache of (embedding, response)
// pairs. When the cache is full, the oldest entry (index 0) is evicted.
// All methods are safe for concurrent use.
type SemanticCache struct {
	mu      sync.Mutex
	entries []semanticEntry
	maxSize int
}

// NewSemanticCache returns a SemanticCache capped at maxSize entries.
func NewSemanticCache(maxSize int) *SemanticCache {
	return &SemanticCache{
		entries: make([]semanticEntry, 0, maxSize),
		maxSize: maxSize,
	}
}

// Lookup returns the cached response whose embedding has cosine similarity ≥
// threshold with the query embedding. Returns ("", false) on a miss.
func (c *SemanticCache) Lookup(embedding []float32, threshold float32) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		if CosineSimilarity(embedding, e.embedding) >= threshold {
			return e.response, true
		}
	}
	return "", false
}

// Store adds the (embedding, response) pair. If the cache is at capacity the
// oldest entry is dropped first (FIFO eviction).
func (c *SemanticCache) Store(embedding []float32, response string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.maxSize {
		c.entries = c.entries[1:]
	}
	c.entries = append(c.entries, semanticEntry{embedding: embedding, response: response})
}

// Len returns the current number of entries (exposed for testing).
func (c *SemanticCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// CosineSimilarity returns dot(a, b) / (‖a‖ · ‖b‖).
// Returns 0 if either vector is a zero vector or if the lengths differ.
func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		ai, bi := float64(a[i]), float64(b[i])
		dot += ai * bi
		normA += ai * ai
		normB += bi * bi
	}
	normA = math.Sqrt(normA)
	normB = math.Sqrt(normB)
	if normA == 0 || normB == 0 {
		return 0
	}
	return float32(dot / (normA * normB))
}
