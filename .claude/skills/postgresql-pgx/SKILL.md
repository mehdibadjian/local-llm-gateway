---
name: postgresql-pgx
description: "Best practices for pgx v5 + pgxpool in CAW's document metadata store and RAG FTS pipeline. Covers pgxpool connection pool setup, DDL schema bootstrap, ON CONFLICT upserts for ingest dedup, PostgreSQL FTS (tsvector + GIN) for BM25-style retrieval, two-step document deletion, and PgBouncer deferral threshold. Use when implementing any PostgreSQL interaction — documents, chunks, tools tables, or FTS BM25 queries."
sources:
  - library: jackc/pgx
    context7_id: /jackc/pgx
    snippets: 155
    score: 88.05
  - library: pgx v5 pkg.go.dev
    context7_id: /websites/pkg_go_dev_github_com_jackc_pgx_v5
    snippets: 2149
    score: 90.75
---

# PostgreSQL / pgx Skill — CAW

## Role

You are implementing or reviewing the PostgreSQL data layer of the Capability Amplification Wrapper.
This covers: document metadata, chunks, tools DDL; ingest dedup; FTS BM25 retrieval for the RAG
hybrid search; document deletion safety. Source of truth: `docs/reference/architecture.md §
Deliverable 2 — Data Schema`.

---

## 1 — Connection Pool (pgxpool)

Use `pgxpool` for all database access — never a bare single connection in a goroutine-concurrent
service. PgBouncer is **deferred to Phase 2**, triggered only when
`pg_stat_activity_count > 50` Prometheus alert fires (architecture EG-I).

```go
// internal/store/pg.go
package store

import (
    "context"
    "os"

    "github.com/jackc/pgx/v5/pgxpool"
)

func NewPool(ctx context.Context) (*pgxpool.Pool, error) {
    config, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
    if err != nil {
        return nil, err
    }
    // Pool sizing: start conservative; alert fires at pg_stat_activity_count > 50
    config.MaxConns = 20
    config.MinConns = 2

    // Register tsvector type for FTS scanning (see §5)
    config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
        return registerCustomTypes(ctx, conn)
    }

    return pgxpool.NewWithConfig(ctx, config)
}
```

**Rules:**
- `DATABASE_URL` from env / K8s Secret — never hardcoded.
- `MaxConns` is the knob before PgBouncer. Phase 0 default: 20.
- Add PgBouncer sidecar only when `pg_stat_activity_count > 50` alert fires.

---

## 2 — Schema Bootstrap DDL

Run at service startup (idempotent — uses `IF NOT EXISTS`):

```go
// internal/store/migrate.go

const schemaDDL = `
CREATE TABLE IF NOT EXISTS documents (
    id            TEXT PRIMARY KEY,
    domain        TEXT        NOT NULL,
    source_path   TEXT        NOT NULL,
    content_hash  TEXT        NOT NULL UNIQUE,
    indexed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    status        TEXT        NOT NULL DEFAULT 'pending'
                              CHECK(status IN ('pending','processing','indexed','failed')),
    error_detail  TEXT,
    retry_count   INTEGER     NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS chunks (
    id              TEXT PRIMARY KEY,
    document_id     TEXT        NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    chunk_index     INTEGER     NOT NULL,
    content         TEXT        NOT NULL,
    qdrant_point_id TEXT        NOT NULL,
    token_count     INTEGER     NOT NULL,
    metadata        JSONB,
    -- FTS column: pre-computed tsvector for BM25 retrieval
    content_tsv     TSVECTOR GENERATED ALWAYS AS (to_tsvector('english', content)) STORED
);

CREATE TABLE IF NOT EXISTS tools (
    id             TEXT PRIMARY KEY,
    name           TEXT    NOT NULL UNIQUE,
    description    TEXT    NOT NULL,
    input_schema   JSONB   NOT NULL,
    executor_type  TEXT    NOT NULL CHECK(executor_type IN ('builtin','subprocess','http')),
    enabled        BOOLEAN NOT NULL DEFAULT true
);

-- Indexes from architecture spec
CREATE INDEX IF NOT EXISTS idx_chunks_doc_order   ON chunks (document_id, chunk_index);
CREATE INDEX IF NOT EXISTS idx_docs_domain_status ON documents (domain, status);
-- GIN index for FTS BM25 retrieval (architecture: "GIN on content")
CREATE INDEX IF NOT EXISTS idx_chunks_fts         ON chunks USING GIN (content_tsv);
`

func Bootstrap(ctx context.Context, pool *pgxpool.Pool) error {
    _, err := pool.Exec(ctx, schemaDDL)
    return err
}
```

---

## 3 — Ingest Dedup: ON CONFLICT Upsert (Architecture CR-G)

All document inserts MUST use `ON CONFLICT (content_hash) DO NOTHING`. Application-level
pre-checks are insufficient under concurrent ingest workers (TOCTOU race).

```go
// internal/store/document.go

// UpsertDocument inserts a document, ignoring duplicates (concurrent-safe).
// Returns the resolved ID (existing or newly inserted).
func (s *Store) UpsertDocument(ctx context.Context, doc Document) (string, error) {
    var resolvedID string
    err := s.pool.QueryRow(ctx, `
        INSERT INTO documents (id, domain, source_path, content_hash, status)
        VALUES (@id, @domain, @source_path, @content_hash, 'pending')
        ON CONFLICT (content_hash) DO NOTHING
        RETURNING id
    `, pgx.NamedArgs{
        "id":           doc.ID,
        "domain":       doc.Domain,
        "source_path":  doc.SourcePath,
        "content_hash": doc.ContentHash,
    }).Scan(&resolvedID)

    if errors.Is(err, pgx.ErrNoRows) {
        // Duplicate — fetch existing ID
        err = s.pool.QueryRow(ctx,
            "SELECT id FROM documents WHERE content_hash = $1", doc.ContentHash,
        ).Scan(&resolvedID)
    }
    return resolvedID, err
}
```

---

## 4 — Chunk Insert (Batch)

Insert chunks in a single batch to reduce round-trips. Qdrant write MUST succeed before this
call (architecture write-ordering rule CR-M).

```go
func (s *Store) InsertChunks(ctx context.Context, chunks []Chunk) error {
    batch := &pgx.Batch{}
    for _, c := range chunks {
        batch.Queue(`
            INSERT INTO chunks (id, document_id, chunk_index, content, qdrant_point_id, token_count, metadata)
            VALUES (@id, @document_id, @chunk_index, @content, @qdrant_point_id, @token_count, @metadata)
        `, pgx.NamedArgs{
            "id":              c.ID,
            "document_id":     c.DocumentID,
            "chunk_index":     c.ChunkIndex,
            "content":         c.Content,
            "qdrant_point_id": c.QdrantPointID,
            "token_count":     c.TokenCount,
            "metadata":        c.Metadata,
        })
    }
    br := s.pool.SendBatch(ctx, batch)
    defer br.Close()
    for range chunks {
        if _, err := br.Exec(); err != nil {
            return fmt.Errorf("insert chunk: %w", err)
        }
    }
    return nil
}
```

---

## 5 — FTS BM25 Retrieval (PostgreSQL ts_rank)

The FTS leg of the hybrid retriever. Runs in parallel with Qdrant ANN via `errgroup` with a
300 ms timeout (architecture EG-G). Uses `content_tsv` GIN-indexed generated column.

```go
// internal/rag/pg_fts.go

type FTSResult struct {
    ChunkID     string
    DocumentID  string
    Content     string
    TokenCount  int
    Rank        float64
}

func (s *Store) FTSSearch(ctx context.Context, domain, query string, limit int) ([]FTSResult, error) {
    // plainto_tsquery is injection-safe — never fmt.Sprintf the query string
    rows, err := s.pool.Query(ctx, `
        SELECT
            c.id,
            c.document_id,
            c.content,
            c.token_count,
            ts_rank(c.content_tsv, plainto_tsquery('english', $1)) AS rank
        FROM chunks c
        JOIN documents d ON d.id = c.document_id
        WHERE d.domain = $2
          AND d.status = 'indexed'
          AND c.content_tsv @@ plainto_tsquery('english', $1)
        ORDER BY rank DESC
        LIMIT $3
    `, query, domain, limit)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    return pgx.CollectRows(rows, pgx.RowToStructByName[FTSResult])
}
```

**Security:** Never interpolate `query` or `domain` into the SQL string — always use positional
parameters (`$1`, `$2`). `plainto_tsquery` handles input sanitisation internally.

---

## 6 — Fetch Qdrant Point IDs (Pre-Deletion Step)

Called before the Qdrant delete step in the two-step document deletion protocol (architecture CR-Q).

```go
func (s *Store) FetchQdrantPointIDs(ctx context.Context, docID string) ([]string, error) {
    rows, err := s.pool.Query(ctx,
        "SELECT qdrant_point_id FROM chunks WHERE document_id = $1", docID)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    return pgx.CollectRows(rows, pgx.RowTo[string])
}
```

---

## 7 — Document Status Update (Ingest Worker)

```go
func (s *Store) SetDocumentStatus(ctx context.Context, id, status, errorDetail string, retryCount int) error {
    _, err := s.pool.Exec(ctx, `
        UPDATE documents
        SET status = $1, error_detail = $2, retry_count = $3
        WHERE id = $4
    `, status, errorDetail, retryCount, id)
    return err
}
```

**DLQ rules (architecture CR-J):** When `retry_count >= 3`, set `status = 'failed'`. The ingest
worker must NOT re-enqueue after exhaustion.

---

## 8 — Domain Filter on All Queries

Every query touching `documents` or `chunks` MUST include `d.domain = $N`. This mirrors the
Qdrant mandatory domain filter requirement and prevents cross-tenant data leakage.

```go
// ✅ Correct — domain parameter bound
pool.Query(ctx, "SELECT ... FROM chunks c JOIN documents d ON d.id = c.document_id WHERE d.domain = $1 ...", domain)

// ❌ Wrong — domain derived from client input directly
pool.Query(ctx, fmt.Sprintf("... WHERE d.domain = '%s'", userInputDomain)) // SQL injection risk
```

---

## Sources

| Library | Stars | Context7 ID | URL |
|---------|-------|-------------|-----|
| jackc/pgx | 11k+ | /jackc/pgx | https://github.com/jackc/pgx |
| pgx v5 docs | — | /websites/pkg_go_dev_github_com_jackc_pgx_v5 | https://pkg.go.dev/github.com/jackc/pgx/v5 |
