package memory

// DDL is the canonical schema for CAW's PostgreSQL document metadata store.
// Run Bootstrap() to apply these statements idempotently.
const (
	DDLDocuments = `
CREATE TABLE IF NOT EXISTS documents (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    domain        TEXT        NOT NULL CHECK (domain IN ('general','legal','medical','code')),
    title         TEXT,
    content       TEXT        NOT NULL,
    content_hash  TEXT        NOT NULL,
    status        TEXT        NOT NULL DEFAULT 'pending'
                              CHECK (status IN ('pending','processing','indexed','failed')),
    error_detail  TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(content_hash)
);`

	DDLChunks = `
CREATE TABLE IF NOT EXISTS chunks (
    id               UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id      UUID    NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    chunk_index      INTEGER NOT NULL,
    content          TEXT    NOT NULL,
    content_hash     TEXT    NOT NULL UNIQUE,
    qdrant_point_id  UUID,
    domain           TEXT    NOT NULL,
    embedding_model  TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(document_id, chunk_index)
);`

	DDLTools = `
CREATE TABLE IF NOT EXISTS tools (
    id             UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
    name           TEXT    NOT NULL UNIQUE,
    description    TEXT,
    input_schema   JSONB   NOT NULL,
    executor_type  TEXT    NOT NULL CHECK (executor_type IN ('builtin','subprocess','http')),
    endpoint_url   TEXT,
    enabled        BOOLEAN NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`

	DDLIdxChunksDocChunk = `CREATE INDEX IF NOT EXISTS idx_chunks_document_chunk
    ON chunks(document_id, chunk_index);`

	DDLIdxDocsDomainStatus = `CREATE INDEX IF NOT EXISTS idx_documents_domain_status
    ON documents(domain, status);`

	DDLIdxChunksContentGIN = `CREATE INDEX IF NOT EXISTS idx_chunks_content_gin
    ON chunks USING GIN(to_tsvector('english', content));`
)
