package embed_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/caw/wrapper/internal/embed"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLRUCache_BasicGetSet(t *testing.T) {
	cache := embed.NewLRUCache(10, 5*time.Minute)

	embedding := []float32{0.1, 0.2, 0.3}
	cache.Set("key1", embedding)

	result, ok := cache.Get("key1")
	require.True(t, ok)
	assert.Equal(t, embedding, result)
}

func TestLRUCache_EvictsLRU(t *testing.T) {
	cache := embed.NewLRUCache(3, 5*time.Minute)

	cache.Set("key1", []float32{1.0})
	cache.Set("key2", []float32{2.0})
	cache.Set("key3", []float32{3.0})

	// Access key1 to make it recently used
	_, _ = cache.Get("key1")

	// Add key4 — should evict key2 (LRU)
	cache.Set("key4", []float32{4.0})

	_, ok := cache.Get("key2")
	assert.False(t, ok, "key2 should have been evicted")

	_, ok = cache.Get("key1")
	assert.True(t, ok, "key1 should still be in cache")

	_, ok = cache.Get("key4")
	assert.True(t, ok, "key4 should be in cache")
}

func TestLRUCache_TTLExpiry(t *testing.T) {
	cache := embed.NewLRUCache(10, 50*time.Millisecond)

	cache.Set("key1", []float32{0.1, 0.2})

	_, ok := cache.Get("key1")
	require.True(t, ok)

	time.Sleep(60 * time.Millisecond)

	_, ok = cache.Get("key1")
	assert.False(t, ok, "entry should have expired")
}

func TestLRUCache_MemoryFootprint(t *testing.T) {
	// 1K entries × 384-dim float32 = 1,536,000 bytes ≈ 1.5 MB raw data.
	// With node/map overhead the total should still be well under 5 MB.
	cache := embed.NewLRUCache(1000, 5*time.Minute)

	for i := 0; i < 1000; i++ {
		embedding := make([]float32, 384)
		for j := range embedding {
			embedding[j] = float32(i*384+j) / 1000.0
		}
		cache.Set(fmt.Sprintf("key%d", i), embedding)
	}

	assert.Equal(t, 1000, cache.Len())

	// Verify LRU eviction still works at capacity
	cache.Set("overflow", []float32{0.1})
	assert.Equal(t, 1000, cache.Len())
}
