package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ToolRecord mirrors the tools table row for pg_store CRUD.
type ToolRecord struct {
	ID           string
	Name         string
	Description  string
	InputSchema  json.RawMessage
	ExecutorType string
	EndpointURL  string
	Enabled      bool
	CreatedAt    time.Time
}

// HasQdrantPoint returns true if any chunk row has the given qdrant_point_id.
func (p *PGStore) HasQdrantPoint(ctx context.Context, qdrantPointID string) (bool, error) {
	var exists bool
	err := p.pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM chunks WHERE qdrant_point_id = $1)",
		qdrantPointID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("has qdrant point: %w", err)
	}
	return exists, nil
}

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

// ListTools returns all rows from the tools table.
func (p *PGStore) ListTools(ctx context.Context) ([]ToolRecord, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, name, description, input_schema, executor_type,
		       COALESCE(endpoint_url,''), enabled, created_at
		FROM tools ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}
	defer rows.Close()

	var out []ToolRecord
	for rows.Next() {
		var t ToolRecord
		if err := rows.Scan(&t.ID, &t.Name, &t.Description, &t.InputSchema,
			&t.ExecutorType, &t.EndpointURL, &t.Enabled, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan tool: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTool returns the tool with the given name, or an error if not found.
func (p *PGStore) GetTool(ctx context.Context, name string) (*ToolRecord, error) {
	var t ToolRecord
	err := p.pool.QueryRow(ctx, `
		SELECT id, name, description, input_schema, executor_type,
		       COALESCE(endpoint_url,''), enabled, created_at
		FROM tools WHERE name = $1`, name,
	).Scan(&t.ID, &t.Name, &t.Description, &t.InputSchema,
		&t.ExecutorType, &t.EndpointURL, &t.Enabled, &t.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get tool %q: %w", name, err)
	}
	return &t, nil
}

// CreateTool inserts a new tool row and returns the created record.
func (p *PGStore) CreateTool(ctx context.Context, t ToolRecord) (*ToolRecord, error) {
	schema := t.InputSchema
	if schema == nil {
		schema = json.RawMessage(`{}`)
	}
	var endpointURL *string
	if t.EndpointURL != "" {
		endpointURL = &t.EndpointURL
	}

	err := p.pool.QueryRow(ctx, `
		INSERT INTO tools (name, description, input_schema, executor_type, endpoint_url, enabled)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, name, description, input_schema, executor_type,
		          COALESCE(endpoint_url,''), enabled, created_at`,
		t.Name, t.Description, schema, t.ExecutorType, endpointURL, t.Enabled,
	).Scan(&t.ID, &t.Name, &t.Description, &t.InputSchema,
		&t.ExecutorType, &t.EndpointURL, &t.Enabled, &t.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create tool: %w", err)
	}
	return &t, nil
}
