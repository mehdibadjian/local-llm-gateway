# CAW — Capability Amplification Wrapper

> Transform a small local model into a production-grade AI system — **100% offline, 100% local**.

CAW is a stateless Go service that wraps a small language model (e.g., `gemma:2b`) with multi-step reasoning, long-context handling, structured output, RAG-backed retrieval, tool calling, and JWT multi-tenant auth — without modifying the underlying model.

**North Star metric:** Close ≥ 60% of the capability gap between `gemma:2b` and GPT-3.5 on MMLU and HumanEval benchmarks, running fully offline on a $24 Droplet (4 GB RAM).

---

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                     API Gateway (Fiber)                  │
│  OpenAI-compatible HTTP · SSE streaming · Worker pool   │
│  JWT multi-tenant auth · Bearer token auth · /healthz   │
└───────────────────────┬─────────────────────────────────┘
                        │
        ┌───────────────┼───────────────┐
        ▼               ▼               ▼
┌──────────────┐ ┌────────────┐ ┌─────────────────┐
│Orchestration │ │  Memory    │ │  Tool Registry  │
│ContextManager│ │  Layer     │ │  Dispatcher     │
│ TaskPlanner  │ │  Redis     │ │  CodeExecutor   │
│ OutputFormat │ │  Qdrant    │ │  (gVisor/native)│
│ Self-Critique│ │  Postgres  │ │  Plugin System  │
└──────┬───────┘ └─────┬──────┘ └────────┬────────┘
       │               │                 │
       └───────────────┼─────────────────┘
                       ▼
          ┌────────────────────────┐
          │   Inference Adapter    │
          │  OllamaAdapter         │
          │  LlamaCppAdapter       │
          │  vLLMAdapter (Phase 2) │
          └───────────┬────────────┘
                      ▼
          ┌────────────────────────┐
          │   Local Model          │
          │   gemma:2b / llama3    │
          │   (via Ollama / llama.cpp)│
          └────────────────────────┘
```

### Layer Summary

| Layer | Description |
|---|---|
| **API Gateway** | OpenAI-compatible HTTP surface (Fiber v2), SSE streaming, worker-pool backpressure, JWT + Bearer auth |
| **Orchestration** | ContextManager, TaskPlanner, OutputFormatter, Self-Critique loop |
| **Memory Layer** | Redis session store, Qdrant vector collections (per-domain), PostgreSQL document metadata + FTS |
| **Async Ingest** | Redis Streams job queue, IngestWorker, DLQ, daily reconciliation CronJob |
| **Embedding Service** | Dedicated `all-MiniLM-L6-v2` pod (384-dim) via gRPC with circuit breaker + LRU cache |
| **RAG Pipeline** | Parallel Qdrant ANN + PG FTS, RRF merge, cross-encoder reranker (agent mode) |
| **Tool Registry** | Tool dispatcher, CodeExecutor sandbox (seccomp + cgroup v2, optional gVisor), community plugins |
| **Inference Adapter** | Pluggable `InferenceBackend` interface — OllamaAdapter, LlamaCppAdapter, vLLMAdapter |
| **IaC / Scaling** | Docker scratch image (<15 MB), Helm charts, KEDA ScaledObjects, serverless manifests |
| **Observability** | OTel traces, 6 canonical `caw_*` Prometheus metrics, Grafana dashboards, k6 load tests |

---

## Quick Start

### Prerequisites

- [Docker](https://docs.docker.com/get-docker/) + Docker Compose
- [Ollama](https://ollama.ai) (for local model inference)

### 1 — Pull the model

```bash
ollama pull gemma:2b
```

### 2 — Start the stack

```bash
docker compose up -d
```

This starts: CAW wrapper, Ollama, Redis, PostgreSQL, Qdrant, and the embedding service.

### 3 — Send a request

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer dev-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemma:2b",
    "messages": [{"role": "user", "content": "Explain transformers in one paragraph."}],
    "stream": false
  }'
```

### 4 — Stream a response

```bash
curl -N http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer dev-key" \
  -H "Content-Type: application/json" \
  -d '{"model":"gemma:2b","messages":[{"role":"user","content":"Write a Go HTTP server"}],"stream":true}'
```

---

## Configuration

All configuration is via environment variables. See `.env.example` for the full list.

| Variable | Default | Description |
|---|---|---|
| `CAW_API_KEY` | *(required)* | Bearer token for API auth |
| `CAW_JWT_SECRET` | *(optional)* | If set, enables JWT multi-tenant auth |
| `OLLAMA_BASE_URL` | `http://localhost:11434` | Ollama inference endpoint |
| `INFERENCE_BACKEND` | `ollama` | Adapter: `ollama`, `llamacpp`, `vllm` |
| `REDIS_ADDR` | `localhost:6379` | Redis address |
| `DATABASE_URL` | *(required)* | PostgreSQL DSN |
| `QDRANT_BASE_URL` | `http://localhost:6333` | Qdrant endpoint |
| `EMBED_BASE_URL` | `http://localhost:5000` | Embedding service endpoint |
| `WORKER_POOL_SIZE` | `10` | Max concurrent inference requests |
| `CODEEXEC_RUNTIME` | `native` | Code sandbox: `native` or `gvisor` |
| `CAW_PLUGIN_DIR` | *(optional)* | Directory of community plugin binaries |

### JWT Auth (multi-tenant)

When `CAW_JWT_SECRET` is set, requests must include a signed JWT with a `domains` claim:

```bash
# Generate a token (example with jwt-cli)
jwt encode --secret "$CAW_JWT_SECRET" '{"sub":"tenant-1","domains":["finance","legal"]}'

# Use it
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer <token>" \
  -H "X-Domain: finance" \
  ...
```

Without `CAW_JWT_SECRET`, the service falls back to API key auth (backward compatible).

---

## Project Layout

```
.
├── cmd/wrapper/          # main.go — entrypoint, DI wiring
├── internal/
│   ├── adapter/          # InferenceBackend interface + Ollama/LlamaCpp/vLLM adapters
│   ├── embed/            # gRPC client for embedding service + LRU cache + circuit breaker
│   ├── gateway/          # Fiber handlers, SSE streaming, worker pool, auth middleware
│   ├── ingest/           # IngestWorker, Redis Streams consumer, DLQ, reconciler
│   ├── memory/           # Redis session store, context window manager
│   ├── observability/    # OTel tracer/meter bootstrap, Prometheus metrics
│   ├── orchestration/    # ContextManager, TaskPlanner, OutputFormatter, self-critique
│   ├── rag/              # Retriever (ANN + FTS + RRF), reranker, chunk store
│   ├── security/         # JWT middleware, API key auth, constant-time compare
│   └── tools/            # Dispatcher, CodeExecutor (sandbox), PluginExecutor, loader
├── embed_service/        # Python gRPC server — all-MiniLM-L6-v2 (sentence-transformers)
├── proto/embed/          # Protobuf contract for embedding gRPC
├── scripts/
│   ├── benchmark/        # MMLU + HumanEval benchmark harness
│   └── migrate_qdrant.go # Qdrant collection migration CLI
├── deploy/
│   ├── helm/             # Helm charts (caw-wrapper, embed-service, qdrant-distributed,
│   │                     #   pgbouncer, ingest-worker, caw-reconciler, inference-backend)
│   ├── keda/             # KEDA ScaledObjects (wrapper + ingest-worker)
│   ├── grafana/          # Dashboard JSON + datasource config
│   ├── prometheus/       # Alert rules
│   ├── redis/            # Redis config
│   ├── docker/           # Docker-specific overrides
│   └── serverless/       # Knative + AWS Lambda manifests
├── .github/workflows/    # helm-publish.yml — OCI publish on helm-v* tags
├── tests/                # Go test packages mirroring internal/
│   ├── adapter/
│   ├── benchmark/        # Requires -tags benchmark
│   ├── embed/
│   ├── gateway/
│   ├── iac/              # Helm/K8s manifest tests
│   ├── ingest/
│   ├── k6/               # Load tests
│   ├── memory/
│   ├── observability/
│   ├── orchestration/
│   ├── rag/
│   ├── security/
│   └── tools/
├── docs/reference/       # Architecture spec, agile backlog
├── Dockerfile            # Multi-stage build → scratch image (<15 MB)
├── docker-compose.yml    # Full local stack
├── go.mod / go.sum
└── progress.txt          # Sprint completion log
```

---

## Running Tests

```bash
# All tests (standard)
go test ./tests/... -count=1

# All tests including benchmark harness
go test ./tests/... -tags benchmark -count=1

# Single package
go test ./tests/security/... -v

# With race detector
go test -race ./tests/... -count=1
```

**12 test packages, 0 failures** on the current `main` branch.

---

## Benchmark Harness

The benchmark harness measures gap closure between `gemma:2b` and GPT-3.5 baselines.

| Benchmark | gemma:2b baseline | GPT-3.5 baseline | Target gap closed |
|---|---|---|---|
| MMLU | 35% | 70% | ≥ 60% |
| HumanEval | 12% | 48% | ≥ 60% |

```bash
# Dry-run (mock responder, no live model needed)
go test ./tests/benchmark/... -tags benchmark -v

# Live run against Ollama
go run ./scripts/benchmark/... -model gemma:2b -output results/run1.json
```

---

## Helm Charts

All charts live in `deploy/helm/`. Published to `oci://ghcr.io/caw/charts` on `helm-v*` tags.

| Chart | Description |
|---|---|
| `caw-wrapper` | Main API gateway + orchestration service |
| `embed-service` | Python embedding pod (all-MiniLM-L6-v2) |
| `ingest-worker` | Async ingest consumer (Redis Streams) |
| `caw-reconciler` | Daily reconciliation CronJob |
| `inference-backend` | Ollama / llama.cpp sidecar |
| `qdrant-distributed` | 3-replica Qdrant StatefulSet (Raft consensus) |
| `pgbouncer` | PgBouncer connection pooler (transaction mode) |

```bash
# Install the full stack into a cluster
helm install caw oci://ghcr.io/caw/charts/caw-wrapper --version 1.0.0

# Trigger Helm publish (creates the GitHub Actions workflow run)
git tag helm-v1.0.0 && git push origin helm-v1.0.0
```

---

## Autoscaling (KEDA)

| Component | Trigger | Scale range |
|---|---|---|
| `caw-wrapper` | `caw_requests_in_flight` (Prometheus) | 0 → 10 |
| `ingest-worker` | Redis Streams `pendingEntriesCount > 50` | 0 → 5 |
| `inference-backend` | keep-warm (`minReplicaCount: 1`) | 1 → 3 |

Fallback: if Prometheus is unavailable, wrapper holds at 3 replicas.

---

## Observability

### Prometheus metrics (frozen names — referenced by KEDA + alert rules)

| Metric | Type | Description |
|---|---|---|
| `caw_requests_total` | Counter | Total requests by status |
| `caw_requests_in_flight` | Gauge | Active worker slots in use |
| `caw_inference_latency_seconds` | Histogram | End-to-end inference time |
| `caw_redis_latency_seconds` | Histogram | Redis command latency |
| `caw_rag_retrieval_latency_seconds` | Histogram | RAG retrieval time |
| `caw_ingest_dlq_depth` | Gauge | Dead-letter queue depth |

Grafana dashboard: `deploy/grafana/dashboard.json`

### Endpoints

| Path | Description |
|---|---|
| `GET /healthz` | Liveness probe (always 200 if process alive) |
| `GET /readyz` | Readiness probe (checks Redis + Postgres + Qdrant) |
| `GET /metrics` | Prometheus scrape endpoint |

---

## Serverless Deployment

See `deploy/serverless/README.md` for full instructions.

- **Knative:** `deploy/serverless/knative-service.yaml` — scale-to-zero, maxScale 10
- **AWS Lambda:** `deploy/serverless/lambda-function.yaml` — CloudFormation SAM, container image

---

## Plugin System

Community plugins are subprocess binaries that speak JSON on stdin/stdout.

```bash
# Install a plugin
cp my-tool /plugins/my-tool && chmod +x /plugins/my-tool

# Configure
export CAW_PLUGIN_DIR=/plugins
```

Plugin contract (stdin → stdout):

```json
// Request
{"tool": "my-tool", "input": {"query": "..."}}

// Response
{"output": "...", "error": null}
```

---

## Development

```bash
# Build
go build -o caw ./cmd/wrapper

# Run locally (requires Redis, Postgres, Qdrant, Ollama)
CAW_API_KEY=dev-key \
REDIS_ADDR=localhost:6379 \
DATABASE_URL=postgres://caw:caw@localhost:5432/caw?sslmode=disable \
QDRANT_BASE_URL=http://localhost:6333 \
EMBED_BASE_URL=http://localhost:5000 \
OLLAMA_BASE_URL=http://localhost:11434 \
./caw

# Lint
go vet ./...

# Format
gofmt -w .
```

### Embedding service (Python)

```bash
cd embed_service
pip install -r requirements.txt
python server.py          # gRPC on :50051
# or
python http_server.py     # HTTP on :5000
```

---

## Contributing

1. Fork and create a feature branch: `git checkout -b feat/my-feature`
2. Follow TDD — write failing tests first, then implement
3. Ensure `go test ./tests/... -count=1` passes with 0 failures
4. Commit with the canonical format: `feat(US-XX): <title>`
5. Open a pull request against `main`

---

## License

MIT — see [LICENSE](LICENSE) for details.
