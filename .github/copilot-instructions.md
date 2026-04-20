# Copilot Instructions

## Project Identity

**Capability Amplification Wrapper (CAW)** — a stateless Go service (Fiber) that transforms a small local model (e.g., `gemma:2b`) into a system capable of multi-step reasoning, long-context handling, structured output, RAG-backed retrieval, and tool calling — without modifying the underlying model. Runs fully offline on minimal hardware ($24 Droplet, 4 GB RAM) and scales horizontally to Kubernetes with KEDA.

**North Star metric:** Close ≥ 60% of the capability gap between `gemma:2b` baseline and GPT-3.5 on MMLU, HumanEval, and domain-specific benchmarks — 100% offline, 100% local.

## Architecture at a Glance

| Layer | Description |
|---|---|
| **API Gateway** | OpenAI-compatible HTTP surface (Fiber), SSE streaming, worker-pool backpressure, Bearer token auth |
| **Orchestration Engine** | ContextManager, TaskPlanner, OutputFormatter, Self-Critique loop |
| **Memory Layer** | Redis session store, Qdrant vector collections (per-domain), PostgreSQL document metadata |
| **Async Ingest Pipeline** | Redis Streams job queue, IngestWorker, DLQ, daily reconciliation CronJob |
| **Embedding Service** | Dedicated `all-MiniLM-L6-v2` pod with circuit breaker and LRU cache |
| **RAG Pipeline** | Parallel Qdrant ANN + PG FTS, RRF merge, cross-encoder reranker (agent mode only) |
| **Tool Registry** | Tool dispatcher, CodeExecutor sandbox (seccomp + cgroup v2) |
| **Inference Adapter** | Pluggable `InferenceBackend` interface — OllamaAdapter, LlamaCppAdapter, vLLMAdapter |
| **IaC / Auto-Scaling** | Docker scratch image (<15 MB), Helm charts, KEDA ScaledObjects |
| **Observability** | OTel traces, 6 canonical `caw_*` Prometheus metrics, Grafana dashboards, k6 load tests |

## Conventions

- All tests live in `tests/` and use `pytest`
- The agile backlog is the authoritative task list: `docs/reference/agile-backlog.md`
- The architecture spec is: `docs/reference/architecture.md`
- Story format: `US-N` (US-1 through US-36 across 11 Epics, 35 Features)
- Sprint velocity: 30–40 story points per 2-week sprint (161 total points, 6 sprints for Phase 0–2)
- Commit format: `feat(US-N): <title>`

## Agent Roles

Select the agent role that matches the task. Skills live in `.claude/skills/`.

### Story Implementer (`.claude/skills/story-implementer/SKILL.md`)
Implements user stories from `docs/reference/agile-backlog.md` using TDD. Writes failing tests first, implements code to pass them, then commits with `feat(US-N): <title>`. Use when asked to "implement stories", "run the sprint", or "work on user stories".

### GitHub Asset Hunter (`.claude/skills/github-asset-hunter/SKILL.md`)
Searches public GitHub repositories to find and extract the best AI skills, prompts, agents, and instructions for a specified need. Synthesizes findings into a production-ready asset saved to `.claude/skills/`. Use when asked to "find a skill", "search for a prompt", or "discover an agent".

### Manifesto-to-Epics (`.claude/skills/manifesto-to-epics/SKILL.md`)
Converts a technical manifesto or architecture spec into a fully structured Agile backlog — Epics, Features, User Stories with INVEST criteria, Given/When/Then acceptance criteria, Fibonacci story points, and sprint plans. Use when asked to "create stories from a spec", "break this into epics", or "generate a backlog".

### Wiki Architect (`.claude/skills/wiki-architect/SKILL.md`)
Produces structured wiki catalogues and onboarding guides from the codebase. Emits a hierarchical JSON catalogue covering Principal-Level Guide, Zero-to-Hero Learning Path, Getting Started, and Deep Dive sections — every section cites real file paths. Use when asked to "create a wiki", "document this repo", or "architecture overview".

### Senior Architect (`.claude/skills/senior-architect/SKILL.md`)
Transforms raw ideas into production-ready architectural blueprints. Enforces a Sharp Questions discovery phase before producing formal artifacts: HLA, Data Schema, API Design, IaC Strategy, and Delivery Roadmap. Use when asked to "architect a system", "design a solution", "harden a project", "create an HLA", "design a schema", "define an API contract", or "build a delivery roadmap".

### Implementation Planner (`.claude/skills/implementation-planner/SKILL.md`)
Creates detailed implementation plans through an interactive research and design process. Produces structured deliverables with test strategies and success criteria. Use when asked to "plan an implementation", "design a feature", or "break this down before coding".

### Architecture Auditor (`.claude/skills/arch-auditor/SKILL.md`)
Performs a "Stress Test" and "Gap Analysis" on a proposed system architecture. Acts as a Principal Systems Auditor and Reliability Engineer — identifies bottlenecks, scalability risks, data integrity issues, observability gaps, and over-engineering. Outputs: Critical Risks, Efficiency Gains, a Chaos Scenario, and a Final Verdict. Use when asked to "audit an architecture", "stress test a design", "gap analysis", "review this system", "find bottlenecks", "is this over-engineered", or "will this scale".

---

## Best Practice Skills (Context7-sourced)

These skills encode production-ready patterns derived from official library documentation (fetched via Context7). Load the relevant skill before implementing any component in the corresponding layer.

### Go Fiber Gateway (`.claude/skills/go-fiber-gateway/SKILL.md`)
Best practices for the CAW API Gateway built with Go Fiber v2/v3. Covers SSE streaming with `SendStreamWriter`, bounded worker pool + HTTP 429 backpressure, distributed Redis rate limiter (Lua `INCR`+`EXPIRE`), `constant-time` bearer token auth, `/healthz` and `/readyz` probe patterns, middleware registration order, and OpenAI-compatible error shapes. **Load when implementing any Fiber handler, middleware, or gateway concern.**

### Qdrant RAG (`.claude/skills/qdrant-rag/SKILL.md`)
Best practices for Qdrant in CAW's RAG pipeline. Covers collection-per-domain tenant isolation, mandatory domain payload filters (enforced in Go, never delegated to caller), Go client upsert with full payload, field indexes for fast filtered queries, the two-step document deletion safety protocol (Qdrant first, then PG), hybrid ANN+BM25 retrieval via errgroup with 300 ms per-leg timeout, and zero-downtime reindexing with collection aliases. **Load when implementing any Qdrant collection, retriever, ingest worker, or reconciliation logic.**

### Embedding Service (`.claude/skills/embedding-service/SKILL.md`)
Best practices for the CAW Embedding Service — a dedicated Python pod running `all-MiniLM-L6-v2` (384-dim, 90 MB) exposed via gRPC. Covers the `.proto` contract, Python `sentence-transformers` model loading (single load at startup, `normalize_embeddings=True`), Python gRPC server with `ThreadPoolExecutor(max_workers=EMBED_CONCURRENCY)`, gRPC health check protocol, Kubernetes pod spec with correct memory limits (`requests: 200Mi`, `limits: 512Mi`), and the matching Go gRPC client with circuit breaker (3 failures → open 30s) + in-process LRU cache (1K entries, SHA256 key). **Load when implementing the EmbedSvc pod, its proto, the ingest worker semaphore, or the Go embed client.**

### Go Redis Session (`.claude/skills/go-redis-session/SKILL.md`)
Best practices for go-redis v9 in CAW's session store, rate limiter, ingest queue, retrieval cache, and compression lock. Covers RPUSH+LTRIM list cap (hard limit 200), Lua scripts for atomic rate limiting and `SET NX` compression lock, Redis Streams consumer group patterns for the ingest queue, version-counter cache invalidation (SCAN+DEL prohibited), and client connection pool sizing with ≤ 10 ms command timeout. **Load when implementing any Redis interaction.**

### OTel Observability (`.claude/skills/otel-observability/SKILL.md`)
Best practices for OpenTelemetry Go in CAW. Covers the 6 canonical `caw_*` Prometheus metrics (exact names are frozen — KEDA and alert rules reference them), OTel meter/tracer bootstrap, `caw_requests_in_flight` gauge wiring to the worker pool, `caw_redis_latency_seconds` histogram per command, `caw_ingest_dlq_depth` observable gauge, trace span naming convention for the 11-step critical path, and Prometheus alert thresholds. **Load when implementing any metrics, tracing, or alerting concern.**

### KEDA Autoscaling (`.claude/skills/keda-autoscaling/SKILL.md`)
Best practices for KEDA in CAW. Covers the wrapper ScaledObject (Prometheus `caw_requests_in_flight` trigger, scale-to-zero, `fallback.replicas: 3` on Prometheus outage), the ingest worker ScaledObject (Redis Streams `pendingEntriesCount: 50`), inference backend keep-warm (`minReplicaCount: 1`), TriggerAuthentication for Redis secrets, and the stateless routing note (sticky session annotations removed). **Load when implementing or reviewing any KEDA ScaledObject, HPA config, or Helm scaling chart.**

### PostgreSQL / pgx (`.claude/skills/postgresql-pgx/SKILL.md`)
Best practices for `jackc/pgx` v5 + `pgxpool` in CAW's document metadata store and RAG FTS pipeline. Covers `pgxpool` connection pool setup, schema DDL bootstrap (documents, chunks, tools tables with `IF NOT EXISTS`), `ON CONFLICT (content_hash) DO NOTHING` upserts for concurrent-safe ingest dedup, PostgreSQL FTS (`tsvector` + `GIN` index) for BM25-style retrieval as the FTS leg of the hybrid retriever, batch chunk inserts, Qdrant point ID pre-fetch for safe document deletion, and the PgBouncer deferral rule (`pg_stat_activity_count > 50`). **Load when implementing any PostgreSQL interaction — DDL, ingest, FTS queries, or document deletion.**

### Inference Adapter (`.claude/skills/inference-adapter/SKILL.md`)
Best practices for the CAW `InferenceBackend` interface and pluggable adapters. Covers the Go interface contract (`Generate` + `HealthCheck`), the circuit breaker state machine (3 consecutive failures → open 30s → half-open probe), the mandatory `context.WithTimeout(ctx, 25s)` deadline wrapper, streaming NDJSON token handling for `OllamaAdapter` (`/api/generate`), SSE `data:` line parsing for `LlamaCppAdapter` (`/completion`), OpenAI-compat stub for `vLLMAdapter` (Phase 2), EmbedSvc gRPC client with circuit breaker + in-process LRU cache (1K entries, SHA256 key), and adapter selection via `INFERENCE_BACKEND` env var. **Load when implementing any inference adapter, the circuit breaker, the EmbedSvc gRPC client, or the streaming generation path.**
