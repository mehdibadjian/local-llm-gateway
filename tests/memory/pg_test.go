package memory_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caw/wrapper/internal/memory"
)

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping PostgreSQL integration tests")
	}

	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })

	return pool
}

func contentHash(s string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(s)))
}

func TestPGBootstrap(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	store := memory.NewPGStore(pool)
	err := store.Bootstrap(ctx)
	require.NoError(t, err)

	// Verify tables exist
	tables := []string{"documents", "chunks", "tools"}
	for _, tbl := range tables {
		var exists bool
		err := pool.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)", tbl,
		).Scan(&exists)
		require.NoError(t, err)
		assert.True(t, exists, "table %s must exist after bootstrap", tbl)
	}
}

func TestPGChunkUpsert_DeduplicatesOnConflict(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	store := memory.NewPGStore(pool)
	require.NoError(t, store.Bootstrap(ctx))

	// Insert a document first
	docContent := "unique doc content for dedup test"
	docHash := contentHash(docContent)
	docID, err := store.UpsertDocument(ctx, memory.Document{
		Domain:      "general",
		Title:       "Test Document",
		Content:     docContent,
		ContentHash: docHash,
	})
	require.NoError(t, err)
	require.NotEmpty(t, docID)

	// Insert the same chunk twice
	chunk := memory.Chunk{
		DocumentID:  docID,
		ChunkIndex:  0,
		Content:     "this is a test chunk",
		ContentHash: contentHash("this is a test chunk"),
		Domain:      "general",
	}

	err = store.UpsertChunk(ctx, chunk)
	require.NoError(t, err, "first insert must succeed")

	err = store.UpsertChunk(ctx, chunk)
	require.NoError(t, err, "second insert (duplicate) must succeed silently")

	// Verify only one row
	var count int
	err = pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM chunks WHERE content_hash = $1", chunk.ContentHash,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "duplicate chunk must produce exactly one row")
}

func TestPGFTSQuery(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	store := memory.NewPGStore(pool)
	require.NoError(t, store.Bootstrap(ctx))

	// Insert a document
	docContent := "fts test document"
	docID, err := store.UpsertDocument(ctx, memory.Document{
		Domain:      "general",
		Title:       "FTS Doc",
		Content:     docContent,
		ContentHash: contentHash(docContent + "-fts"),
	})
	require.NoError(t, err)

	// Insert 10 chunks with distinct content
	words := []string{
		"golang programming language goroutines channels",
		"kubernetes container orchestration deployment",
		"redis cache session storage expiration",
		"qdrant vector similarity search embeddings",
		"postgresql database indexing full text search",
		"prometheus metrics monitoring alerting rules",
		"opentelemetry tracing spans instrumentation",
		"fiber http server middleware routing",
		"embedding model sentence transformers minilm",
		"retrieval augmented generation knowledge base",
	}
	for i, w := range words {
		err := store.UpsertChunk(ctx, memory.Chunk{
			DocumentID:  docID,
			ChunkIndex:  i,
			Content:     w,
			ContentHash: contentHash(fmt.Sprintf("fts-chunk-%d-%s", i, w)),
			Domain:      "general",
		})
		require.NoError(t, err)
	}

	// FTS query for "golang"
	results, err := store.FTSSearch(ctx, "general", "golang", 5)
	require.NoError(t, err)
	require.NotEmpty(t, results, "FTS must return results for 'golang'")
	assert.Contains(t, results[0].Content, "golang")
}
