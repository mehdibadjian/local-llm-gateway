package rag_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/caw/wrapper/internal/rag"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetrievalCache_HitAvoidsDeps(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	cache := rag.NewRetrievalCache(rdb)
	ctx := context.Background()

	results := []rag.RetrievalResult{
		{ChunkID: "c1", Content: "cached content", Score: 0.9, Source: "ann", Domain: "general"},
	}
	err := cache.Set(ctx, "general", "my query", results)
	require.NoError(t, err)

	got, hit, err := cache.Get(ctx, "general", "my query")
	require.NoError(t, err)
	require.True(t, hit)
	require.Len(t, got, 1)
	assert.Equal(t, results[0].ChunkID, got[0].ChunkID)
	assert.Equal(t, results[0].Content, got[0].Content)
}

func TestRetrievalCache_VersionInvalidates(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	cache := rag.NewRetrievalCache(rdb)
	ctx := context.Background()

	results := []rag.RetrievalResult{
		{ChunkID: "c1", Content: "cached", Score: 0.9, Source: "ann", Domain: "general"},
	}
	err := cache.Set(ctx, "general", "my query", results)
	require.NoError(t, err)

	_, hit, err := cache.Get(ctx, "general", "my query")
	require.NoError(t, err)
	require.True(t, hit)

	// Increment version (as ingest would)
	rdb.Incr(ctx, "caw:retrieval:general:version")

	// Now cache should miss (version in key changed)
	_, hit, err = cache.Get(ctx, "general", "my query")
	require.NoError(t, err)
	assert.False(t, hit)
}

func TestRetrievalCache_TTLExpiry(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	cache := rag.NewRetrievalCache(rdb)
	ctx := context.Background()

	results := []rag.RetrievalResult{
		{ChunkID: "c1", Content: "test", Score: 0.9, Source: "ann", Domain: "general"},
	}
	err := cache.Set(ctx, "general", "test query", results)
	require.NoError(t, err)

	_, hit, err := cache.Get(ctx, "general", "test query")
	require.NoError(t, err)
	require.True(t, hit)

	// Fast-forward past TTL
	mr.FastForward(61 * time.Second)

	_, hit, err = cache.Get(ctx, "general", "test query")
	require.NoError(t, err)
	assert.False(t, hit)
}

func TestRetrievalCache_NeverUsesScanDel(t *testing.T) {
	source, err := os.ReadFile("../../internal/rag/cache.go")
	require.NoError(t, err)
	content := string(source)
	assert.False(t, strings.Contains(content, ".Scan("), "cache.go must not use SCAN")
	assert.False(t, strings.Contains(content, ".Del("), "cache.go must not use DEL")
}
