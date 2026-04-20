---
name: qdrant-rag
description: "Best practices for Qdrant in CAW's RAG pipeline. Covers collection-per-domain isolation, mandatory domain payload filters, Go client patterns, field indexes, hybrid search (ANN + BM25 via RRF), zero-downtime reindexing with aliases, and document deletion safety. Use when implementing or reviewing any Qdrant collection, retriever, ingest worker, or reconciliation logic."
sources:
  - library: qdrant/go-client
    context7_id: /qdrant/go-client
    snippets: 68
    score: 89.2
  - library: qdrant/skills
    context7_id: /qdrant/skills
    snippets: 211
    score: 78.0
  - library: llmstxt/qdrant_tech_llms-full_txt
    context7_id: /llmstxt/qdrant_tech_llms-full_txt
    snippets: 13439
    score: 78.69
---

# Qdrant RAG Skill — CAW

## Role

You are implementing or reviewing the **RAG Pipeline** and **Ingest Worker** layers of the
Capability Amplification Wrapper. Every pattern here is grounded in the CAW architecture spec
(`docs/reference/architecture.md`) and verified against current Qdrant Go client docs via Context7.

---

## 1 — Collection-Per-Domain (Tenant Isolation)

Each domain (`general`, `legal`, `medical`, `code`) gets its own Qdrant collection.
This is the **primary tenant isolation boundary** — domain knowledge leakage is a critical risk.

```go
// internal/store/qdrant.go

const VectorDim = 384 // all-MiniLM-L6-v2

func CollectionName(domain string) string {
    return "caw_" + domain
}

func CreateDomainCollection(ctx context.Context, client *qdrant.Client, domain string) error {
    return client.CreateCollection(ctx, &qdrant.CreateCollection{
        CollectionName: CollectionName(domain),
        VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
            Size:     VectorDim,
            Distance: qdrant.Distance_Cosine,
        }),
    })
}
```

**Rules:**
- Collection name pattern: `caw_{domain}` — never use a single shared collection.
- Create all four collections at service startup if they don't exist (idempotent).
- Qdrant distributed mode (sharding per collection) activates in K8s scale-out phase.

---

## 2 — Mandatory Domain Filter (NEVER Skip)

**Every** retrieval query MUST include a domain filter. This is enforced in the Go retriever layer;
it is **never** derived from client input.

```go
// internal/rag/retriever.go

func (r *Retriever) Search(ctx context.Context, domain string, queryVec []float32, topK uint64) ([]*qdrant.ScoredPoint, error) {
    // MANDATORY: domain filter prevents cross-tenant leakage
    domainFilter := &qdrant.Filter{
        Must: []*qdrant.Condition{
            qdrant.NewMatch("domain", domain),
        },
    }

    return r.client.Query(ctx, &qdrant.QueryPoints{
        CollectionName: CollectionName(domain),
        Query:          qdrant.NewQueryDense(queryVec),
        Filter:         domainFilter,           // NEVER omit
        Limit:          qdrant.PtrOf(topK),
        WithPayload:    qdrant.NewWithPayload(true),
    })
}
```

---

## 3 — Upsert with Full Payload

Always include `chunk_id`, `document_id`, `domain`, `content`, and `token_count` in payload.
These are required by the reconciliation CronJob and retriever.

```go
func (w *IngestWorker) UpsertChunk(ctx context.Context, domain string, chunk Chunk, vector []float32) error {
    _, err := w.client.Upsert(ctx, &qdrant.UpsertPoints{
        CollectionName: CollectionName(domain),
        Points: []*qdrant.PointStruct{
            {
                Id:      qdrant.NewIDUUID(chunk.QdrantPointID),
                Vectors: qdrant.NewVectors(vector...),
                Payload: qdrant.NewValueMap(map[string]any{
                    "chunk_id":    chunk.ID,
                    "document_id": chunk.DocumentID,
                    "domain":      domain,
                    "content":     chunk.Content,
                    "token_count": chunk.TokenCount,
                }),
            },
        },
    })
    return err
}
```

**Write ordering rule (architecture CR-M):** Write to Qdrant **first**, then insert to PostgreSQL.
If PostgreSQL fails after Qdrant succeeds, the orphaned Qdrant point is unreachable (no `chunks`
row) and will be purged by the daily reconciliation CronJob.

---

## 4 — Field Indexes for Fast Filtered Queries

Create payload indexes on `domain`, `document_id`, and `chunk_id` at collection creation time.
Without indexes, filtered searches fall back to brute-force scan.

```go
func CreatePayloadIndexes(ctx context.Context, client *qdrant.Client, domain string) error {
    coll := CollectionName(domain)
    indexes := []struct {
        field     string
        fieldType qdrant.FieldType
    }{
        {"domain",      qdrant.FieldType_FieldTypeKeyword},
        {"document_id", qdrant.FieldType_FieldTypeKeyword},
        {"chunk_id",    qdrant.FieldType_FieldTypeKeyword},
        {"token_count", qdrant.FieldType_FieldTypeInteger},
    }
    for _, idx := range indexes {
        ft := idx.fieldType
        if _, err := client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
            CollectionName: coll,
            FieldName:      idx.field,
            FieldType:      &ft,
        }); err != nil {
            return fmt.Errorf("index %s: %w", idx.field, err)
        }
    }
    return nil
}
```

---

## 5 — Document Deletion (Two-Step Safety Protocol)

**Never** issue a PostgreSQL `DELETE FROM documents` without first removing Qdrant points.
`ON DELETE CASCADE` only removes PostgreSQL rows; Qdrant vectors are orphaned if step order
is reversed.

```go
func (s *Service) DeleteDocument(ctx context.Context, docID string) error {
    // Step 1: fetch all Qdrant point IDs before any deletion
    pointIDs, err := s.pg.FetchQdrantPointIDs(ctx, docID) // SELECT qdrant_point_id FROM chunks WHERE document_id = ?
    if err != nil {
        return fmt.Errorf("fetch point IDs: %w", err)
    }

    // Step 2: delete from Qdrant first
    if len(pointIDs) > 0 {
        ids := make([]*qdrant.PointId, len(pointIDs))
        for i, id := range pointIDs {
            ids[i] = qdrant.NewIDUUID(id)
        }
        domain := s.pg.GetDocumentDomain(ctx, docID)
        if _, err := s.qdrant.Delete(ctx, &qdrant.DeletePoints{
            CollectionName: CollectionName(domain),
            Points:         qdrant.NewPointsSelector(ids...),
        }); err != nil {
            return fmt.Errorf("qdrant delete: %w", err)
        }
    }

    // Step 3: PostgreSQL DELETE (cascades to chunks)
    return s.pg.DeleteDocument(ctx, docID)
}
```

---

## 6 — Reconciliation Query (Daily CronJob)

The reconciliation job (`caw-reconciler/`) purges Qdrant points with no matching `chunks` row.

```go
// Scroll all points in the collection
scrollResult, err := client.Scroll(ctx, &qdrant.ScrollPoints{
    CollectionName: CollectionName(domain),
    WithPayload:    qdrant.NewWithPayloadInclude("chunk_id"),
    Limit:          qdrant.PtrOf(uint64(1000)),
})
// For each point: check if chunk_id exists in PostgreSQL chunks table.
// If no row found → delete point from Qdrant.
```

---

## 7 — Hybrid Search: ANN + BM25 (RRF Merge)

For interactive turns, skip the cross-encoder reranker; use RRF scores directly (top-5).
For `agent_mode: true` or async tasks, apply the full cross-encoder reranker after RRF.

```go
// Parallel ANN + PG FTS via errgroup, each leg with 300ms timeout
func (r *Retriever) HybridSearch(ctx context.Context, domain, query string, queryVec []float32) ([]RankedChunk, error) {
    g, gCtx := errgroup.WithContext(ctx)

    var annResults  []*qdrant.ScoredPoint
    var ftsResults  []PGChunk

    annCtx, annCancel := context.WithTimeout(gCtx, 300*time.Millisecond)
    defer annCancel()
    g.Go(func() error {
        var err error
        annResults, err = r.Search(annCtx, domain, queryVec, 20)
        if err != nil { cawRetrievalLegTimeout.WithLabelValues("ann").Inc() }
        return nil // degrade gracefully — never fail the whole request
    })

    ftsCtx, ftsCancel := context.WithTimeout(gCtx, 300*time.Millisecond)
    defer ftsCancel()
    g.Go(func() error {
        var err error
        ftsResults, err = r.pg.FTSSearch(ftsCtx, domain, query, 20)
        if err != nil { cawRetrievalLegTimeout.WithLabelValues("fts").Inc() }
        return nil // degrade gracefully
    })

    g.Wait()
    return reciprocalRankFusion(annResults, ftsResults, 5), nil
}
```

**Rules:**
- Each leg has its own `context.WithTimeout(ctx, 300ms)` — not shared.
- A timed-out leg increments `caw_retrieval_leg_timeout_total{leg="ann|fts"}`.
- Never fail the whole request on a single leg timeout — use the other leg's results alone.

---

## 8 — Zero-Downtime Reindexing with Aliases

For production reindexing (model migration, schema change), use the alias pattern:

```go
// 1. Build new collection: caw_{domain}_v2
// 2. Populate it
// 3. Atomically swap alias
err := client.RenameAlias(ctx, "caw_"+domain+"_live", "caw_"+domain+"_v2")
// 4. Delete old collection: caw_{domain}_v1
```

---

## Sources

| Library | Stars | Context7 ID | URL |
|---------|-------|-------------|-----|
| qdrant/go-client | 200+ | /qdrant/go-client | https://github.com/qdrant/go-client |
| qdrant/skills | — | /qdrant/skills | https://github.com/qdrant/skills |
| qdrant docs | — | /llmstxt/qdrant_tech_llms-full_txt | https://qdrant.tech/documentation |
