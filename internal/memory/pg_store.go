package memory

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Document is the metadata record for an ingested document.
type Document struct {
	ID          string
	Domain      string
	Title       string
	Content     string
	ContentHash string
	Status      string
}

// Chunk is a sub-section of a document with its own embedding.
type Chunk struct {
	ID             string
	DocumentID     string
	ChunkIndex     int
	Content        string
	ContentHash    string
	QdrantPointID  string
	Domain         string
	EmbeddingModel string
}

// PGStore handles PostgreSQL DDL bootstrap and CRUD operations.
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore returns a PGStore backed by the given connection pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool}
}

// Bootstrap applies all DDL statements idempotently (IF NOT EXISTS).
func (p *PGStore) Bootstrap(ctx context.Context) error {
	stmts := []string{
		DDLDocuments,
		DDLChunks,
		DDLTools,
		DDLIdxChunksDocChunk,
		DDLIdxDocsDomainStatus,
		DDLIdxChunksContentGIN,
	}
	for _, stmt := range stmts {
		if _, err := p.pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("bootstrap DDL: %w", err)
		}
	}
	return nil
}

// UpsertDocument inserts a document row. Duplicate content_hash rows are silently skipped.
// Returns the UUID of the inserted (or existing) document.
func (p *PGStore) UpsertDocument(ctx context.Context, doc Document) (string, error) {
	status := doc.Status
	if status == "" {
		status = "pending"
	}

	var id string
	err := p.pool.QueryRow(ctx, `
		INSERT INTO documents (domain, title, content, content_hash, status)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (content_hash) DO UPDATE SET updated_at = NOW()
		RETURNING id`,
		doc.Domain, doc.Title, doc.Content, doc.ContentHash, status,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("upsert document: %w", err)
	}
	return id, nil
}

// UpsertChunk inserts a chunk row. Duplicate content_hash rows are silently skipped
// via ON CONFLICT DO NOTHING (per-spec deduplication).
func (p *PGStore) UpsertChunk(ctx context.Context, chunk Chunk) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO chunks (document_id, chunk_index, content, content_hash, domain, embedding_model)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (content_hash) DO NOTHING`,
		chunk.DocumentID, chunk.ChunkIndex, chunk.Content,
		chunk.ContentHash, chunk.Domain, chunk.EmbeddingModel,
	)
	if err != nil {
		return fmt.Errorf("upsert chunk: %w", err)
	}
	return nil
}

// FTSSearch performs a BM25-style full-text search on chunks.content for the given domain.
func (p *PGStore) FTSSearch(ctx context.Context, domain, query string, limit int) ([]Chunk, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, document_id, chunk_index, content, content_hash, domain
		FROM chunks
		WHERE domain = $1
		  AND to_tsvector('english', content) @@ plainto_tsquery('english', $2)
		ORDER BY ts_rank(to_tsvector('english', content), plainto_tsquery('english', $2)) DESC
		LIMIT $3`,
		domain, query, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("fts search: %w", err)
	}
	defer rows.Close()

	var chunks []Chunk
	for rows.Next() {
		var c Chunk
		if err := rows.Scan(&c.ID, &c.DocumentID, &c.ChunkIndex, &c.Content, &c.ContentHash, &c.Domain); err != nil {
			return nil, fmt.Errorf("scan chunk: %w", err)
		}
		chunks = append(chunks, c)
	}
	return chunks, rows.Err()
}
