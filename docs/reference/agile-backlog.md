# CAW — Agile Backlog

**Project:** Capability Amplification Wrapper (CAW)
**Generated from:** `docs/reference/architecture.md` v1.1
**Date:** 2026-04-20
**Sprint velocity:** 30–40 points per 2-week sprint
**Total story points:** 161
**Sprints:** 6 (Phase 0–2) + open Phase 3 backlog

---

## Epics

| ID | Title | Description |
|---|---|---|
| EP-1 | API Gateway — Request Ingress & Auth | OpenAI-compatible HTTP surface, streaming transport, auth, rate limiting, and backpressure |
| EP-2 | Orchestration Engine — Multi-Step Request Coordination | ContextManager, TaskPlanner, OutputFormatter, and Self-Critique loop |
| EP-3 | Memory Layer — Session, Vector & Metadata Stores | Redis session store, Qdrant vector collections, PostgreSQL document metadata |
| EP-4 | Async Ingest Pipeline — Document Ingestion & Indexing | Redis Streams job queue, IngestWorker, DLQ, reconciliation CronJob |
| EP-5 | Embedding Service — Dedicated Embedding Pod | all-MiniLM-L6-v2 pod, circuit breaker, LRU embedding cache |
| EP-6 | RAG Pipeline — Hybrid Retrieval & Context Injection | Parallel Qdrant ANN + PG FTS retrieval, retrieval cache, RRF merge, Reranker |
| EP-7 | Tool Registry — Sandboxed Tool Execution | Tool dispatcher, CodeExecutor sandbox, tool registration API |
| EP-8 | Inference Adapter Layer — Pluggable Backend Abstraction | InferenceBackend interface, OllamaAdapter, LlamaCppAdapter, vLLMAdapter |
| EP-9 | IaC & Auto-Scaling — Kubernetes Deployment | Docker, Helm charts, Terraform modules, KEDA ScaledObjects |
| EP-10 | Observability — Traces, Metrics & Alerting | OTel traces, 6 canonical Prometheus metrics, Grafana dashboards, k6 load tests |
| EP-11 | Security — Auth, Rate Limiting & Domain Isolation | API key auth, distributed rate limiter, Qdrant domain filter, tool sandboxing |

---

## Features

### EP-1 — API Gateway

| ID | Feature | Description |
|---|---|---|
| F-1 | OpenAI-Compatible Chat Endpoint | POST /v1/chat/completions accepting OpenAI-format request/response bodies |
| F-2 | SSE Streaming Transport | Token-by-token SSE streaming on the chat endpoint |
| F-3 | Worker Pool Backpressure | Bounded goroutine worker pool returning HTTP 429 when at capacity |
| F-4 | Health & Readiness Probes | /healthz liveness + /readyz readiness checking inference backend health endpoint only |
| F-5 | Document Ingest Endpoint | POST /v1/documents returning HTTP 202; session management DELETE /v1/sessions/{id} |

### EP-2 — Orchestration Engine

| ID | Feature | Description |
|---|---|---|
| F-6 | Context Manager with Atomic Compression | Sliding-window history load; SET NX atomic compression lock; loser truncation fallback |
| F-7 | Task Planner — Intent Classification | Classify requests into simple-generate / structured-output / agent-loop / rag-query |
| F-8 | Output Formatter — Grammar-Constrained JSON | Grammar-constrained primary path; ≤1 correction-prompt retry fallback |
| F-9 | Self-Critique Loop | Conditional critique pass: opt-in, legal/medical auto, side-effect tool calls, RAG-degraded |

### EP-3 — Memory Layer

| ID | Feature | Description |
|---|---|---|
| F-10 | Redis Session Store | Session Hash + message List (RPUSH+LTRIM pipeline) with TTL and noeviction config |
| F-11 | Qdrant Vector Store | Per-domain collections with mandatory domain filter on all queries |
| F-12 | PostgreSQL Metadata Store | Documents/chunks/tools DDL with full index set and upsert dedup semantics |

### EP-4 — Async Ingest Pipeline

| ID | Feature | Description |
|---|---|---|
| F-13 | Document Enqueue via Redis Streams | POST /v1/documents → Redis Streams job → HTTP 202; status polling endpoint |
| F-14 | IngestWorker — Chunk, Embed, Index | Semaphore-gated EmbedSvc calls; Qdrant-first write ordering; ON CONFLICT dedup |
| F-15 | Dead-Letter Queue & Retry Bounds | max_retry_count=3; terminal failed status; caw_ingest_dlq_depth alert at depth > 10 |
| F-16 | Reconciliation CronJob | Daily CronJob purging orphaned Qdrant points with no matching chunks row |

### EP-5 — Embedding Service

| ID | Feature | Description |
|---|---|---|
| F-17 | EmbedSvc Deployment & Resources | Dedicated pod; resources.requests.memory: 200Mi / limits.memory: 512Mi; restart alert |
| F-18 | EmbedSvc Circuit Breaker | 3 failures → open 30s → RAG-degraded mode; x-caw-rag-degraded response header |
| F-19 | Embedding LRU Cache | In-process SHA256-keyed LRU (TTL 5min, 1K entries) eliminating repeated gRPC round-trips |

### EP-6 — RAG Pipeline

| ID | Feature | Description |
|---|---|---|
| F-20 | Hybrid Retriever with Per-Leg Timeout | Qdrant ANN + PG FTS via errgroup; 300ms per-leg timeout; single-source degraded mode |
| F-21 | Retrieval Cache with Version Invalidation | Redis cache keyed by (domain, query_hash, version); INCR version on ingest completion |
| F-22 | Reranker — Agent Mode Gate | Cross-encoder reranker applied only for agent_mode=true and async tasks |

### EP-7 — Tool Registry

| ID | Feature | Description |
|---|---|---|
| F-23 | Tool Registry & Dispatcher | GET/POST /v1/tools; in-process tool router dispatching to registered executors |
| F-24 | Code Executor Sandbox (MVP) | seccomp + Linux namespaces + cgroup v2 (256MB / 0.5 CPU) via cgexec per subprocess |

### EP-8 — Inference Adapter Layer

| ID | Feature | Description |
|---|---|---|
| F-25 | InferenceBackend Interface + OllamaAdapter | Go interface contract; OllamaAdapter with 25s context deadline and circuit breaker |
| F-26 | LlamaCppAdapter | HTTP server-mode adapter for llama.cpp; interchangeable with OllamaAdapter |
| F-27 | vLLMAdapter | OpenAI-compatible adapter for GPU-accelerated vLLM deployments |

### EP-9 — IaC & Auto-Scaling

| ID | Feature | Description |
|---|---|---|
| F-28 | Docker Image & docker-compose | Scratch image (<15MB); docker-compose covering all services for local dev |
| F-29 | Helm Charts | Full chart set: wrapper, inference, embed, ingest, reconciler, redis, postgresql, qdrant, keda |
| F-30 | KEDA ScaledObjects | Wrapper scaler (Prometheus trigger, fallback=3); ingest scaler (Redis Streams lag) |

### EP-10 — Observability

| ID | Feature | Description |
|---|---|---|
| F-31 | Canonical Prometheus Metrics | All 6 caw_* metrics exported with exact canonical names matching KEDA triggers |
| F-32 | Grafana Dashboards & Alert Rules | Dashboards + alerts for all documented thresholds; Loki log aggregation |
| F-33 | k6 Load Test Suite | 50 concurrent users; sub-3s P95 first-token; validates SLA gates before release |

### EP-11 — Security

| ID | Feature | Description |
|---|---|---|
| F-34 | API Key Authentication | Bearer token auth enforced at gateway; key stored in K8s Secret |
| F-35 | Distributed Rate Limiter | Redis INCR counter; 60 req/min per API key; enforced before worker pool |

---

## User Stories

### EP-1 — API Gateway

---

```json
{
  "id": "US-1",
  "feature_id": "F-1",
  "title": "As a client developer, I want a POST /v1/chat/completions endpoint that accepts OpenAI-format requests, so that existing OpenAI SDK clients work without modification.",
  "acceptance_criteria": [
    "Given a valid OpenAI-format chat request When I POST to /v1/chat/completions Then I receive a response with the same schema as the OpenAI Chat API",
    "Given a request with stream=false When the response is generated Then the full completion is returned in a single JSON body",
    "Given a request with an unsupported model name When the adapter cannot resolve it Then HTTP 400 is returned with a descriptive error message",
    "Given a request missing the required 'messages' field When validated at the gateway Then HTTP 422 is returned before entering the worker pool"
  ],
  "story_points": 5,
  "priority": "Highest",
  "labels": ["gateway", "api", "phase-0"]
}
```

---

```json
{
  "id": "US-2",
  "feature_id": "F-2",
  "title": "As a client developer, I want token-by-token SSE streaming on /v1/chat/completions, so that I can display progressive responses and achieve sub-3s perceived latency.",
  "acceptance_criteria": [
    "Given a request with stream=true When the backend begins generating Then the first SSE token is delivered to the client within 1 second",
    "Given a streaming response in progress When the inference backend times out Then a final SSE error event is emitted and the stream is closed cleanly",
    "Given a client that disconnects mid-stream When the connection is dropped Then the wrapper cancels the context and releases the worker pool slot",
    "Given a streaming response When all tokens are delivered Then the final [DONE] SSE event is sent per OpenAI spec"
  ],
  "story_points": 5,
  "priority": "Highest",
  "labels": ["gateway", "streaming", "phase-1"]
}
```

---

```json
{
  "id": "US-3",
  "feature_id": "F-3",
  "title": "As an ops engineer, I want a bounded goroutine worker pool at the gateway, so that the wrapper degrades gracefully under overload rather than exhausting memory.",
  "acceptance_criteria": [
    "Given the worker pool is at maximum depth When a new request arrives Then HTTP 429 is returned immediately without entering the queue",
    "Given the pool depth is configurable via environment variable When the service starts Then the pool uses the configured value",
    "Given 429 responses are being generated When checked in Prometheus Then caw_requests_in_flight does not exceed the configured pool depth",
    "Given a k6 load test exceeding pool capacity When run against the service Then P100 of 429 responses arrive within 50ms (no queuing delay)"
  ],
  "story_points": 3,
  "priority": "Highest",
  "labels": ["gateway", "reliability", "phase-0"]
}
```

---

```json
{
  "id": "US-4",
  "feature_id": "F-4",
  "title": "As a Kubernetes operator, I want /healthz liveness and /readyz readiness probes where /readyz checks only the inference backend health endpoint, so that pod startup does not block scale-out.",
  "acceptance_criteria": [
    "Given the wrapper service has started When GET /healthz is called Then HTTP 200 is returned immediately with no external dependencies checked",
    "Given the inference backend health endpoint returns 200 When GET /readyz is called Then HTTP 200 is returned and the pod is marked ready",
    "Given the inference backend health endpoint is unreachable When GET /readyz is called Then HTTP 503 is returned and the pod remains unready",
    "Given /readyz does NOT perform a full inference round-trip When measured Then readiness check completes in under 200ms"
  ],
  "story_points": 3,
  "priority": "High",
  "labels": ["gateway", "kubernetes", "phase-0"]
}
```

---

```json
{
  "id": "US-5",
  "feature_id": "F-5",
  "title": "As a content administrator, I want POST /v1/documents to accept a document and return HTTP 202, and DELETE /v1/sessions/{id} to clear session memory, so that I can manage content and sessions via the API.",
  "acceptance_criteria": [
    "Given a valid document POST request When submitted Then HTTP 202 is returned with a document_id in the response body",
    "Given an enqueued document When GET /v1/documents/{id}/status is called Then the current status (pending/processing/indexed/failed) is returned",
    "Given an existing session When DELETE /v1/sessions/{id} is called Then all Redis keys for that session are deleted and HTTP 204 is returned",
    "Given a POST /v1/documents request with missing required field 'domain' When validated Then HTTP 422 is returned"
  ],
  "story_points": 3,
  "priority": "High",
  "labels": ["gateway", "api", "phase-1"]
}
```

---

### EP-2 — Orchestration Engine

---

```json
{
  "id": "US-6",
  "feature_id": "F-6",
  "title": "As the system, I want ContextManager to load conversation history from Redis and atomically compress it at 3K tokens using a SET NX lock, so that no request proceeds with a context that would overflow gemma:2b's 8K window.",
  "acceptance_criteria": [
    "Given a session with < 3K tokens When a request is processed Then history is loaded from Redis without compression",
    "Given a session with > 3K tokens When a compression lock is acquired Then the winning goroutine compresses and writes back atomically",
    "Given two concurrent requests on the same session When both detect > 3K tokens Then only one acquires the lock; losers wait ≤500ms then hard-truncate to 2.5K tokens",
    "Given the compression lock TTL of 2s expires without release When a loser times out Then hard-truncation is applied — the request never proceeds with uncompressed 3K+ context",
    "Given a compressed session When the next request loads history Then token_count is ≤ 2.5K"
  ],
  "story_points": 8,
  "priority": "Highest",
  "labels": ["orchestration", "context", "phase-2"]
}
```

---

```json
{
  "id": "US-7",
  "feature_id": "F-7",
  "title": "As the system, I want TaskPlanner to classify each request into simple-generate | structured-output | agent-loop | rag-query, so that the correct processing pipeline is selected without evaluating all paths.",
  "acceptance_criteria": [
    "Given a plain chat message with no tools and no response_format When classified Then intent is simple-generate",
    "Given a request with response_format.type=json_object When classified Then intent is structured-output and grammar-constrained generation is used",
    "Given a request with agent_mode=true When classified Then intent is agent-loop and the ReAct pipeline is entered",
    "Given a request with rag_enabled=true and a domain set When classified Then intent is rag-query and the RAG pipeline is entered",
    "Given an unclassifiable request When classification fails Then intent defaults to simple-generate and an x-caw-intent-fallback header is set"
  ],
  "story_points": 5,
  "priority": "High",
  "labels": ["orchestration", "planner", "phase-2"]
}
```

---

```json
{
  "id": "US-8",
  "feature_id": "F-8",
  "title": "As a developer integrating structured output, I want grammar-constrained JSON generation as the primary path with at most 1 correction-prompt retry, so that JSON schema validity is ≥85% on first attempt.",
  "acceptance_criteria": [
    "Given a structured-output request When sent to Ollama Then format:json or llama.cpp grammar mode is used for generation",
    "Given grammar-constrained output that fails JSON validation When detected Then exactly one correction-prompt retry is issued",
    "Given a second failure after the correction retry When detected Then HTTP 200 is returned with x-caw-format-error: true header and best-effort output",
    "Given 100 structured-output requests When measured Then ≥85% produce valid JSON on the first attempt without retry",
    "Given the retry path When traced in OTel Then it appears as a distinct span tagged format_retry=true"
  ],
  "story_points": 5,
  "priority": "High",
  "labels": ["orchestration", "output", "phase-1"]
}
```

---

```json
{
  "id": "US-9",
  "feature_id": "F-9",
  "title": "As a legal/medical domain user, I want the self-critique pass automatically triggered for high-stakes conditions, so that ungrounded or side-effect-producing responses always receive a verification step.",
  "acceptance_criteria": [
    "Given a request with x-caw-options.critique=true When processed Then a self-critique pass is applied regardless of domain",
    "Given a request with domain=legal or domain=medical When processed Then self-critique is applied automatically without opt-in",
    "Given a tool call that wrote external state When detected Then self-critique is applied before streaming the response",
    "Given x-caw-rag-degraded=true AND domain=legal or medical When detected Then self-critique is applied automatically",
    "Given self-critique is triggered When measured via OTel Then the critique span adds < 800ms overhead"
  ],
  "story_points": 5,
  "priority": "High",
  "labels": ["orchestration", "safety", "phase-2"]
}
```

---

### EP-3 — Memory Layer

---

```json
{
  "id": "US-10",
  "feature_id": "F-10",
  "title": "As a user, I want my conversation history persisted in Redis with TTL-based sliding window expiry, so that my session survives wrapper pod restarts and scales across pods.",
  "acceptance_criteria": [
    "Given a session is created When any session key is accessed Then the TTL is reset to 24h (sliding window)",
    "Given a new message is written When the RPUSH is executed Then LTRIM 0 199 is issued in the same pipeline — list never exceeds 200 entries",
    "Given a wrapper pod is restarted When the next request arrives Then conversation history is loaded from Redis without loss",
    "Given two wrapper pods handling the same session_id When both load history Then both read identical state from the shared Redis instance"
  ],
  "story_points": 3,
  "priority": "Highest",
  "labels": ["memory", "redis", "phase-0"]
}
```

---

```json
{
  "id": "US-11",
  "feature_id": "F-10",
  "title": "As a platform engineer, I want Redis configured with maxmemory-policy noeviction and a memory alert at 80%, so that active session keys are never silently destroyed under memory pressure.",
  "acceptance_criteria": [
    "Given Redis is running When maxmemory is reached Then new write commands return OOM errors instead of evicting keys",
    "Given maxmemory-policy is noeviction When verified Then redis-cli CONFIG GET maxmemory-policy returns noeviction",
    "Given redis_memory_used_bytes / redis_memory_max_bytes > 0.80 When this threshold is crossed Then a Prometheus alert fires",
    "Given the noeviction policy is in place When session keys exist Then no session key is removed before its TTL expires"
  ],
  "story_points": 3,
  "priority": "Highest",
  "labels": ["memory", "redis", "reliability", "phase-0"]
}
```

---

```json
{
  "id": "US-12",
  "feature_id": "F-11",
  "title": "As a platform engineer, I want Qdrant collections created per domain with a mandatory domain filter on all queries, so that knowledge is isolated between domains and cross-domain leakage is impossible.",
  "acceptance_criteria": [
    "Given domains [general, legal, medical, code] When the system initialises Then four Qdrant collections caw_{domain} are created with 384-dim Cosine vectors",
    "Given any retriever query When executed Then it includes filter: {must: [{key: domain, match: {value: request_domain}}]}",
    "Given a legal domain query When executed Then no general or medical domain vectors are returned",
    "Given the domain filter is missing from a query When detected at the retriever layer Then the query is rejected with a logged error before reaching Qdrant"
  ],
  "story_points": 5,
  "priority": "Highest",
  "labels": ["memory", "qdrant", "security", "phase-1"]
}
```

---

```json
{
  "id": "US-13",
  "feature_id": "F-12",
  "title": "As the ingest worker, I want the PostgreSQL DDL deployed with all required indexes, so that retrieval, dedup, and FTS queries perform within spec at MVP scale.",
  "acceptance_criteria": [
    "Given the DDL migration runs When completed Then documents, chunks, and tools tables exist with all CHECK constraints",
    "Given (document_id, chunk_index), (domain, status), UNIQUE(content_hash), and GIN(content) indexes When queried Then query plans use index scans not sequential scans",
    "Given a BM25 full-text query on chunks.content When run against 5K chunks Then results return in < 5ms",
    "Given a duplicate content_hash insert When attempted Then the row is silently skipped via ON CONFLICT DO NOTHING and no error is raised"
  ],
  "story_points": 3,
  "priority": "High",
  "labels": ["memory", "postgresql", "phase-0"]
}
```

---

### EP-4 — Async Ingest Pipeline

---

```json
{
  "id": "US-14",
  "feature_id": "F-13",
  "title": "As a content administrator, I want POST /v1/documents to enqueue a job to Redis Streams and return HTTP 202, so that document ingestion never blocks interactive request throughput.",
  "acceptance_criteria": [
    "Given a valid document POST request When submitted Then HTTP 202 is returned within 50ms (enqueue only — no processing on request goroutine)",
    "Given the job is enqueued When polled via GET /v1/documents/{id}/status Then the status field reflects current processing state (pending/processing/indexed/failed)",
    "Given the IngestWorker is down When a document is POSTed Then the job remains in the stream and is processed when the worker recovers",
    "Given a document with missing required field 'domain' When POSTed Then HTTP 422 is returned before any enqueue"
  ],
  "story_points": 3,
  "priority": "High",
  "labels": ["ingest", "api", "phase-1"]
}
```

---

```json
{
  "id": "US-15",
  "feature_id": "F-14",
  "title": "As the IngestWorker, I want to chunk, embed (with EMBED_CONCURRENCY=4 semaphore), write Qdrant first then PostgreSQL via ON CONFLICT DO NOTHING, so that ingest is safe under concurrent workers and partial failures leave no data corruption.",
  "acceptance_criteria": [
    "Given an IngestWorker pod When processing a document Then at most 4 concurrent EmbedSvc calls are active at any time",
    "Given two IngestWorker pods processing the same document When both attempt to insert Then only one row is inserted; the second silently skips via ON CONFLICT DO NOTHING",
    "Given a successful Qdrant write followed by a PostgreSQL failure When detected Then the Qdrant point is recorded as orphaned and the reconciliation job cleans it up",
    "Given an IngestWorker pod restart mid-job When the consumer group reprocesses the message Then the job completes without duplicate chunks",
    "Given a completed ingest job When finished Then INCR caw:retrieval:{domain}:version is called to invalidate the retrieval cache"
  ],
  "story_points": 8,
  "priority": "Highest",
  "labels": ["ingest", "worker", "reliability", "phase-1"]
}
```

---

```json
{
  "id": "US-16",
  "feature_id": "F-15",
  "title": "As a platform engineer, I want failed ingest chunks to go to a dead-letter stream with max_retry_count=3 and a Prometheus alert at DLQ depth > 10, so that ingest failures are bounded, visible, and actionable.",
  "acceptance_criteria": [
    "Given a chunk that fails embedding 3 times When the third failure occurs Then the document status is set to failed in PostgreSQL and no further retries are attempted",
    "Given chunks in the dead-letter stream When the caw_ingest_dlq_depth gauge is scraped Then it reports the current pending entry count",
    "Given caw_ingest_dlq_depth > 10 When the threshold is crossed Then a Prometheus alert fires within one scrape interval",
    "Given a failed document When GET /v1/documents/{id}/status is called Then status=failed and error_detail is populated"
  ],
  "story_points": 5,
  "priority": "High",
  "labels": ["ingest", "reliability", "observability", "phase-1"]
}
```

---

```json
{
  "id": "US-17",
  "feature_id": "F-16",
  "title": "As a platform engineer, I want a daily reconciliation CronJob that purges Qdrant points with no matching chunks row, so that write-ordering and deletion failures do not accumulate orphaned vectors.",
  "acceptance_criteria": [
    "Given Qdrant points exist with no matching qdrant_point_id in the chunks table When the CronJob runs Then those points are deleted from the Qdrant collection",
    "Given a document deletion that completed step 1+2 (Qdrant delete, PG cascade) When the CronJob runs Then no spurious deletions occur (idempotent)",
    "Given the CronJob completes When checked Then a log line reports the count of orphaned points found and deleted",
    "Given the CronJob is deployed When listed in Kubernetes Then it appears under the caw-reconciler Helm release with a daily schedule"
  ],
  "story_points": 3,
  "priority": "Medium",
  "labels": ["ingest", "maintenance", "phase-1"]
}
```

---

### EP-5 — Embedding Service

---

```json
{
  "id": "US-18",
  "feature_id": "F-17",
  "title": "As a platform engineer, I want the EmbedSvc pod deployed with resources.requests.memory: 200Mi / limits.memory: 512Mi and a restart alert, so that it does not OOMKill under concurrent ingest load.",
  "acceptance_criteria": [
    "Given the EmbedSvc Helm chart When deployed Then resource requests are 200Mi and limits are 512Mi",
    "Given kube_pod_container_status_restarts_total{container=embed-service} > 2 in a 5-minute window When detected Then a Prometheus alert fires",
    "Given 4 concurrent embedding requests When processed Then EmbedSvc memory stays below 512Mi",
    "Given the EmbedSvc pod restarts When the wrapper is serving requests Then the EmbedSvc circuit breaker activates within 3 consecutive failures"
  ],
  "story_points": 3,
  "priority": "High",
  "labels": ["embed", "reliability", "phase-1"]
}
```

---

```json
{
  "id": "US-19",
  "feature_id": "F-18",
  "title": "As the wrapper, I want an EmbedSvc gRPC client circuit breaker that trips after 3 consecutive failures and opens for 30s, so that EmbedSvc unavailability does not cascade to interactive request latency.",
  "acceptance_criteria": [
    "Given EmbedSvc returns 3 consecutive errors When the circuit breaker trips Then the circuit is open for 30s and further embedding requests fail-fast",
    "Given the circuit is open When a new interactive request arrives Then RAG is skipped, x-caw-rag-degraded: true is set in the response header, and the request completes normally",
    "Given the circuit is open for 30s When the half-open probe succeeds Then the circuit closes and normal embedding resumes",
    "Given the circuit trips When x-caw-rag-degraded=true AND domain=legal or medical Then the self-critique pass is automatically triggered",
    "Given EmbedSvc is killed mid-request When verified via integration test Then non-RAG requests continue serving with x-caw-rag-degraded: true"
  ],
  "story_points": 5,
  "priority": "Highest",
  "labels": ["embed", "resilience", "phase-1"]
}
```

---

```json
{
  "id": "US-20",
  "feature_id": "F-19",
  "title": "As the RAG pipeline, I want an in-process LRU cache for query embeddings (SHA256 key, TTL 5min, 1K entries), so that repeated queries avoid the 15ms EmbedSvc gRPC round-trip.",
  "acceptance_criteria": [
    "Given the same query text is embedded twice within 5 minutes When the second call is made Then no gRPC call to EmbedSvc is issued",
    "Given the LRU cache reaches 1K entries When a new entry is added Then the least-recently-used entry is evicted",
    "Given a cache entry's TTL expires When the next request for that query arrives Then a fresh gRPC call is made to EmbedSvc",
    "Given the cache is running When measured Then its memory footprint stays within ~1.5MB"
  ],
  "story_points": 3,
  "priority": "Medium",
  "labels": ["embed", "performance", "phase-1"]
}
```

---

### EP-6 — RAG Pipeline

---

```json
{
  "id": "US-21",
  "feature_id": "F-20",
  "title": "As a user asking factual questions, I want Qdrant ANN and PostgreSQL FTS retrieval to run in parallel with a 300ms per-leg timeout, so that retrieval degrades gracefully if one backend is slow.",
  "acceptance_criteria": [
    "Given a RAG query When executed Then Qdrant ANN and PostgreSQL FTS BM25 run concurrently via errgroup",
    "Given one retrieval leg exceeds 300ms When detected Then that leg's result is discarded and the other leg's results are used alone",
    "Given a per-leg timeout When recorded Then caw_retrieval_leg_timeout_total{leg=ann|fts} is incremented",
    "Given both legs complete within 300ms When merged Then results are combined via Reciprocal Rank Fusion (RRF) and top-5 are injected as [CONTEXT] block",
    "Given interactive turn with agent_mode=false When merged Then the cross-encoder reranker is skipped"
  ],
  "story_points": 8,
  "priority": "Highest",
  "labels": ["rag", "retrieval", "phase-1"]
}
```

---

```json
{
  "id": "US-22",
  "feature_id": "F-21",
  "title": "As the RAG pipeline, I want a Redis retrieval cache keyed by (domain, query_hash, domain_version) invalidated via INCR on ingest completion, so that hot queries skip the full RAG pipeline and stale results are impossible after new ingestion.",
  "acceptance_criteria": [
    "Given the same query in the same domain within 60s When the cache is hit Then no Qdrant or PostgreSQL query is executed",
    "Given a new document is ingested into a domain When ingestion completes Then INCR caw:retrieval:{domain}:version is called, invalidating all existing cache entries for that domain",
    "Given the domain version increments When the next query constructs its cache key Then the new key misses the cache and fresh retrieval runs",
    "Given SCAN+DEL is never called for cache invalidation When verified in code review Then the implementation uses only INCR (O(1))"
  ],
  "story_points": 5,
  "priority": "High",
  "labels": ["rag", "cache", "performance", "phase-1"]
}
```

---

```json
{
  "id": "US-23",
  "feature_id": "F-22",
  "title": "As a platform engineer, I want the cross-encoder reranker to run only for agent_mode=true and async tasks, so that interactive turns are not burdened by 20-200ms reranking latency.",
  "acceptance_criteria": [
    "Given agent_mode=false and an interactive turn When RAG completes Then the reranker is not invoked; RRF scores are used directly",
    "Given agent_mode=true When RAG completes Then the cross-encoder reranker is applied after RRF merge before injecting context",
    "Given an async background task When RAG completes Then the reranker is applied",
    "Given the reranker is skipped When measured via OTel Then no reranker span appears in the interactive turn trace"
  ],
  "story_points": 3,
  "priority": "High",
  "labels": ["rag", "performance", "phase-1"]
}
```

---

### EP-7 — Tool Registry

---

```json
{
  "id": "US-24",
  "feature_id": "F-23",
  "title": "As a developer, I want GET /v1/tools to list registered tools and POST /v1/tools to register a new tool, so that tools are discoverable and extensible at runtime without redeployment.",
  "acceptance_criteria": [
    "Given tools are registered in the PostgreSQL tools table When GET /v1/tools is called Then all enabled tools are returned with their input_schema",
    "Given a valid tool registration POST When submitted Then the tool is persisted to PostgreSQL and immediately available for dispatch",
    "Given a tool is disabled (enabled=false) When GET /v1/tools is called Then it is excluded from the response",
    "Given an invalid executor_type not in [builtin, subprocess, http] When POSTed Then HTTP 422 is returned"
  ],
  "story_points": 3,
  "priority": "High",
  "labels": ["tools", "api", "phase-1"]
}
```

---

```json
{
  "id": "US-25",
  "feature_id": "F-24",
  "title": "As the system, I want tool subprocesses to execute under seccomp + Linux user namespaces with cgroup v2 limits (256MB / 0.5 CPU) via cgexec, so that malicious or runaway tool code cannot exhaust node resources.",
  "acceptance_criteria": [
    "Given a tool subprocess is launched When measured Then it runs under a seccomp profile that blocks non-essential syscalls",
    "Given a tool subprocess that attempts network egress When executed Then the network call is blocked (verified with strace)",
    "Given a tool subprocess that allocates > 256MB When cgroup v2 limits are applied Then the process is killed by the OOM killer before exceeding limits",
    "Given a CPU-intensive tool subprocess When running Then CPU is capped at 0.5 cores via cgroup v2",
    "Given the sandbox overhead When benchmarked Then it adds < 10ms to tool execution time"
  ],
  "story_points": 8,
  "priority": "Highest",
  "labels": ["tools", "security", "sandbox", "phase-1"]
}
```

---

### EP-8 — Inference Adapter Layer

---

```json
{
  "id": "US-26",
  "feature_id": "F-25",
  "title": "As a developer, I want the InferenceBackend Go interface and OllamaAdapter with a 25s context deadline and circuit breaker, so that inference hangs do not block the worker pool indefinitely.",
  "acceptance_criteria": [
    "Given the InferenceBackend interface When implemented Then it compiles and all adapters satisfy the interface contract",
    "Given an OllamaAdapter request When the backend does not respond within 25s Then the context is cancelled and an error is returned to the orchestrator",
    "Given 3 consecutive timeout errors When detected Then the circuit breaker opens and subsequent calls fail-fast",
    "Given the circuit is open for 30s When the half-open probe succeeds Then the circuit closes",
    "Given a mock InferenceBackend When used in unit tests Then all orchestration logic tests run without a real Ollama instance"
  ],
  "story_points": 5,
  "priority": "Highest",
  "labels": ["adapter", "inference", "phase-0"]
}
```

---

```json
{
  "id": "US-27",
  "feature_id": "F-26",
  "title": "As a platform engineer, I want a LlamaCppAdapter in HTTP server mode, so that llama.cpp serves as an interchangeable inference backend.",
  "acceptance_criteria": [
    "Given INFERENCE_BACKEND=llamacpp is set When the service starts Then the LlamaCppAdapter is used for all inference calls",
    "Given a chat completion request When processed via LlamaCppAdapter Then the response format matches the OllamaAdapter contract",
    "Given the LlamaCppAdapter When run in the integration test suite Then all adapter contract tests pass",
    "Given grammar mode is available in llama.cpp When a structured-output request is made Then the grammar parameter is passed to enforce JSON output"
  ],
  "story_points": 5,
  "priority": "High",
  "labels": ["adapter", "inference", "phase-1"]
}
```

---

```json
{
  "id": "US-28",
  "feature_id": "F-27",
  "title": "As a platform engineer, I want a vLLMAdapter for OpenAI-compatible vLLM deployments, so that GPU-accelerated inference is supported for Phase 2+ production scale.",
  "acceptance_criteria": [
    "Given INFERENCE_BACKEND=vllm is set When the service starts Then the vLLMAdapter is used for all inference calls",
    "Given a chat completion request When processed via vLLMAdapter Then it serialises to OpenAI-compatible HTTP and parses the response",
    "Given the vLLMAdapter integration tests When run Then all adapter contract tests pass against a running vLLM instance or mock",
    "Given vLLMAdapter is added to docker-compose.yml as an optional profile When activated Then the full stack starts with vLLM as the backend"
  ],
  "story_points": 5,
  "priority": "Medium",
  "labels": ["adapter", "inference", "gpu", "phase-2"]
}
```

---

### EP-9 — IaC & Auto-Scaling

---

```json
{
  "id": "US-29",
  "feature_id": "F-28",
  "title": "As a developer, I want a Docker scratch image under 15MB and a docker-compose.yml covering all services, so that local dev setup is a single docker compose up command.",
  "acceptance_criteria": [
    "Given the Go binary is built for linux/amd64 When packaged into a scratch image Then docker images shows size < 15MB",
    "Given docker-compose.yml When docker compose up is run Then wrapper, Ollama, Redis, PostgreSQL, and Qdrant all start and pass health checks",
    "Given the docker-compose stack is running When curl /v1/chat/completions is called Then a valid response is returned from gemma:2b",
    "Given the docker-compose stack is started on a fresh machine When no prior images exist Then the stack is operational within 5 minutes (excluding model pull)"
  ],
  "story_points": 3,
  "priority": "Highest",
  "labels": ["iac", "developer-experience", "phase-0"]
}
```

---

```json
{
  "id": "US-30",
  "feature_id": "F-29",
  "title": "As a platform engineer, I want a complete Helm chart set for all services, so that the full CAW stack deploys to Kubernetes reproducibly via helm install.",
  "acceptance_criteria": [
    "Given the caw-wrapper Helm chart When deployed Then the Deployment, Service, Ingress, and ConfigMap are created",
    "Given all charts (caw-wrapper, inference-backend, embed-service, ingest-worker, caw-reconciler, redis, postgresql, qdrant, keda) When helm install runs Then all pods reach Running state",
    "Given the embed-service chart When deployed Then resource requests=200Mi and limits=512Mi are set",
    "Given the postgresql chart When deployed Then it uses a StatefulSet with a PVC and no PgBouncer sidecar (deferred to Phase 2)",
    "Given the caw-reconciler chart When deployed Then a CronJob with a daily schedule is created"
  ],
  "story_points": 8,
  "priority": "High",
  "labels": ["iac", "kubernetes", "helm", "phase-1"]
}
```

---

```json
{
  "id": "US-31",
  "feature_id": "F-30",
  "title": "As a platform engineer, I want KEDA ScaledObjects for wrapper and ingest worker, so that the system scales to zero when idle and holds minimum capacity during Prometheus outages.",
  "acceptance_criteria": [
    "Given the wrapper ScaledObject is deployed When caw_requests_in_flight=0 Then the wrapper scales to 0 replicas after the cool-down period",
    "Given the wrapper ScaledObject is deployed When Prometheus is unreachable for 3 consecutive checks Then KEDA holds 3 replicas (fallback block)",
    "Given the ingest ScaledObject is deployed When consumer group lag=0 Then ingest-worker scales to 0 replicas",
    "Given 50 pending ingest messages When the KEDA scaler evaluates Then it provisions 1 worker replica (50 messages / pendingEntriesCount=50)",
    "Given the inference backend ScaledObject When deployed Then minReplicaCount=1 (keep-warm) and the pod never scales to zero"
  ],
  "story_points": 5,
  "priority": "High",
  "labels": ["iac", "keda", "autoscaling", "phase-2"]
}
```

---

### EP-10 — Observability

---

```json
{
  "id": "US-32",
  "feature_id": "F-31",
  "title": "As a platform engineer, I want all 6 canonical caw_* Prometheus metrics exported with exact names, so that KEDA triggers and alert rules function without name-mismatch failures.",
  "acceptance_criteria": [
    "Given the wrapper is running When GET /metrics is scraped Then caw_requests_in_flight (gauge), caw_redis_latency_seconds (histogram), caw_ingest_dlq_depth (gauge), caw_retrieval_leg_timeout_total (counter, label: leg), caw_rag_degraded_total (counter, label: domain), caw_critique_pass_total (counter, label: trigger) are all present",
    "Given the KEDA wrapper ScaledObject query sum(caw_requests_in_flight) When Prometheus is scraped Then the metric resolves without error",
    "Given an OTel trace for an interactive request When viewed in Jaeger/Tempo Then spans for context-load, rag-retrieval, inference, and critique are present with latency annotations",
    "Given the /metrics endpoint When accessed without auth from outside the cluster Then it returns HTTP 403 (internal-only)"
  ],
  "story_points": 5,
  "priority": "High",
  "labels": ["observability", "prometheus", "phase-2"]
}
```

---

```json
{
  "id": "US-33",
  "feature_id": "F-32",
  "title": "As an ops engineer, I want Grafana dashboards and Prometheus alert rules for all documented thresholds, so that production incidents are detected and alerted before they cascade.",
  "acceptance_criteria": [
    "Given Redis p99 latency > 5ms When triggered Then a Prometheus alert fires and appears in Grafana",
    "Given caw_ingest_dlq_depth > 10 When triggered Then an alert fires within one scrape interval",
    "Given EmbedSvc restarts > 2 in 5 minutes When triggered Then an alert fires",
    "Given Redis memory > 80% When triggered Then an alert fires",
    "Given all alerts When configured Then each has a runbook annotation URL pointing to the ops runbook",
    "Given the Grafana dashboard When opened Then it shows caw_requests_in_flight, P95 first-token latency, RAG-degraded rate, and DLQ depth on a single pane"
  ],
  "story_points": 5,
  "priority": "High",
  "labels": ["observability", "grafana", "alerting", "phase-2"]
}
```

---

```json
{
  "id": "US-34",
  "feature_id": "F-33",
  "title": "As a QA engineer, I want a k6 load test suite that validates sub-3s P95 streaming first-token at 50 concurrent users, so that every release is validated against the SLA before deployment.",
  "acceptance_criteria": [
    "Given the k6 suite runs against a staging environment When 50 VUs are active Then P95 streaming first-token latency is < 3s",
    "Given the k6 suite runs When rate limit is tested Then HTTP 429 responses appear when > 60 req/min per API key",
    "Given the k6 suite runs When the worker pool is saturated Then 429s appear and P99 latency does not spike (no queuing)",
    "Given the k6 suite completes When results are exported Then a JSON summary is saved as a CI artefact"
  ],
  "story_points": 5,
  "priority": "High",
  "labels": ["observability", "testing", "performance", "phase-2"]
}
```

---

### EP-11 — Security

---

```json
{
  "id": "US-35",
  "feature_id": "F-34",
  "title": "As a platform engineer, I want API key authentication enforced at the gateway via Authorization Bearer header, so that all requests are authenticated before entering any processing pipeline.",
  "acceptance_criteria": [
    "Given a request with no Authorization header When received Then HTTP 401 is returned before the worker pool is checked",
    "Given a request with an invalid Bearer token When received Then HTTP 403 is returned",
    "Given a valid API key stored in a Kubernetes Secret When the wrapper starts Then it reads the key from the environment (not hardcoded)",
    "Given a valid API key When used Then authentication adds < 1ms to request latency"
  ],
  "story_points": 3,
  "priority": "Highest",
  "labels": ["security", "auth", "phase-0"]
}
```

---

```json
{
  "id": "US-36",
  "feature_id": "F-35",
  "title": "As a platform engineer, I want the distributed Redis rate limiter enforced at the gateway before the worker pool, so that a single API key cannot exceed 60 req/min regardless of how many wrapper pods are running.",
  "acceptance_criteria": [
    "Given a single API key making 61 requests within 60 seconds When measured across all pods Then exactly the 61st request receives HTTP 429",
    "Given the rate limiter uses INCR caw:rate:{api_key}:{window_ts} with TTL=window When inspected in Redis Then the counter key exists with the correct TTL",
    "Given 20 wrapper pods are running When a single API key is rate-limited Then all pods enforce the same limit (shared Redis counter)",
    "Given the Redis rate counter operation When benchmarked Then it adds < 1ms to gateway latency (within the 10ms Redis command timeout budget)"
  ],
  "story_points": 5,
  "priority": "Highest",
  "labels": ["security", "rate-limiting", "phase-1"]
}
```

---

## Sprint Plan

### Sprint 1 — Foundation Core (Phase 0, Week 1–2)
**Goal:** Single-turn chat works end-to-end locally; core infrastructure is bootstrapped.

| Story | Points |
|---|---|
| US-26 — InferenceBackend interface + OllamaAdapter | 5 |
| US-29 — Docker image + docker-compose | 3 |
| US-10 — Redis session store (RPUSH+LTRIM) | 3 |
| US-11 — Redis noeviction config + memory alert | 3 |
| US-13 — PostgreSQL DDL + indexes | 3 |
| US-1 — OpenAI-compatible REST endpoint | 5 |
| US-3 — Bounded worker pool + HTTP 429 | 3 |
| US-4 — /healthz + /readyz probes | 3 |
| US-35 — API key authentication | 3 |
| **Total** | **31** |

---

### Sprint 2 — Gateway Hardening & Orchestration (Phase 0, Week 2–3)
**Goal:** Rate limiting is distributed, streaming works, context management is safe.

| Story | Points |
|---|---|
| US-36 — Distributed Redis rate limiter | 5 |
| US-2 — SSE streaming transport | 5 |
| US-7 — TaskPlanner intent classification | 5 |
| US-8 — OutputFormatter grammar-constrained JSON | 5 |
| US-6 — ContextManager atomic compression + loser truncation | 8 |
| **Total** | **28** |

> **Sprint 2 success gate (Phase 0 complete):** `curl /v1/chat/completions` returns a valid streaming response; HTTP 429 fires when pool is full and when rate limit is exceeded; session survives pod restart.

---

### Sprint 3 — RAG & Ingest MVP (Phase 1, Week 4–6)
**Goal:** Documents can be ingested and retrieved; RAG context is injected into prompts.

| Story | Points |
|---|---|
| US-12 — Qdrant vector store (per-domain, domain filter) | 5 |
| US-18 — EmbedSvc deployment + resource limits | 3 |
| US-19 — EmbedSvc circuit breaker + RAG-degraded mode | 5 |
| US-20 — Embedding LRU cache | 3 |
| US-14 — Document enqueue via Redis Streams | 3 |
| US-15 — IngestWorker chunk/embed/index | 8 |
| US-16 — DLQ + max_retry_count=3 + alert | 5 |
| **Total** | **32** |

---

### Sprint 4 — Tools, Adapters & Retrieval (Phase 1, Week 6–8)
**Goal:** Tool calling is sandboxed; LlamaCppAdapter is live; hybrid retriever with caching is complete.

| Story | Points |
|---|---|
| US-21 — Hybrid retriever with per-leg timeout | 8 |
| US-22 — Retrieval cache with version invalidation | 5 |
| US-23 — Reranker gate (agent_mode only) | 3 |
| US-24 — Tool registry + dispatcher API | 3 |
| US-25 — Code executor sandbox (seccomp + cgroup v2) | 8 |
| US-27 — LlamaCppAdapter | 5 |
| **Total** | **32** |

> **Sprint 4 success gate (Phase 1 complete):** RAG recall@5 ≥ 0.75; JSON valid ≥85% first attempt; streaming first-token < 1s; OllamaAdapter + LlamaCppAdapter integration tests pass.

---

### Sprint 5 — Intelligence, Hardening & IaC (Phase 2, Month 3)
**Goal:** Agent loops, context compression, and observability are production-grade.

| Story | Points |
|---|---|
| US-9 — Self-critique loop (conditional triggers) | 5 |
| US-17 — Reconciliation CronJob | 3 |
| US-28 — vLLMAdapter | 5 |
| US-30 — Full Helm chart set | 8 |
| US-31 — KEDA ScaledObjects (wrapper + ingest) | 5 |
| US-5 — Document ingest API + session delete | 3 |
| **Total** | **29** |

---

### Sprint 6 — Observability, Load Testing & Phase 2 Close (Phase 2, Month 4)
**Goal:** Full observability stack deployed; SLA validated under 50 concurrent users.

| Story | Points |
|---|---|
| US-32 — Canonical Prometheus metrics (all 6) | 5 |
| US-33 — Grafana dashboards + alert rules | 5 |
| US-34 — k6 load test suite | 5 |
| **Total** | **15** |

> **Sprint 6 success gate (Phase 2 complete):** P95 streaming first-token < 1s at 20 concurrent users; self-critique adds < 800ms; zero data loss on pod eviction; all alert rules fire correctly in staging.

---

### Phase 3 Backlog (Month 5+, Not Sprint-Planned)

The following items are out of scope for the 6-sprint delivery but are captured for roadmap visibility:

| Story ID | Title | Points |
|---|---|---|
| US-37 | JWT multi-tenant auth with per-domain claims | 8 |
| US-38 | Plugin system for community tool contributions | 13 |
| US-39 | MMLU + HumanEval benchmark harness (gemma:2b vs GPT-3.5) | 8 |
| US-40 | Qdrant distributed mode migration path for >1M chunks | 5 |
| US-41 | PgBouncer connection pooler (gated on pg_stat_activity > 50) | 3 |
| US-42 | gVisor upgrade for CodeExecutor (Phase 2 seccomp → gVisor) | 8 |
| US-43 | Serverless deployment guide (Knative / AWS Lambda container) | 5 |
| US-44 | Public Helm chart registry publication | 3 |
| **Phase 3 Total** | | **53** |

---

## Definition of Done

```json
{
  "definition_of_done": [
    "All acceptance criteria pass with automated tests (unit + integration as applicable)",
    "Code reviewed and merged to main with no open review comments",
    "Prometheus metrics emitted correctly for any new instrumented code path",
    "Helm chart values updated if new configuration parameters are introduced",
    "Risk Register entry added if the story introduces a new operational hazard",
    "Integration tests pass against docker-compose stack (not mocks only)"
  ]
}
```

---

## Backlog Summary

| Sprint | Phase | Points | Cumulative |
|---|---|---|---|
| Sprint 1 | Phase 0 | 31 | 31 |
| Sprint 2 | Phase 0 | 28 | 59 |
| Sprint 3 | Phase 1 | 32 | 91 |
| Sprint 4 | Phase 1 | 32 | 123 |
| Sprint 5 | Phase 2 | 29 | 152 |
| Sprint 6 | Phase 2 | 15 | 167 |
| Phase 3 Backlog | Phase 3 | 53 | 220 |

**Stories in scope (Sprints 1–6):** US-1 through US-36 — **36 stories, 167 points**
**Total including Phase 3:** 44 stories, 220 points
