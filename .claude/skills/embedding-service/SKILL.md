---
name: embedding-service
description: "Best practices for the CAW Embedding Service — a dedicated Python pod running all-MiniLM-L6-v2 (384-dim, 90 MB) exposed via gRPC. Covers sentence-transformers model loading, Python gRPC server setup, gRPC health check protocol, concurrency cap (EMBED_CONCURRENCY=4), memory limits (512Mi), Kubernetes pod spec, and the matching Go gRPC client stub with circuit breaker + LRU cache. Use when implementing or reviewing the EmbedSvc pod, its proto contract, or the Go client in the ingest worker or RAG retriever."
sources:
  - library: huggingface/sentence-transformers
    context7_id: /huggingface/sentence-transformers
    snippets: 1729
    score: 88.62
  - library: grpc/grpc (Python)
    context7_id: /grpc/grpc
    snippets: 2060
    score: 52.75
---

# Embedding Service Skill — CAW

## Role

You are implementing or reviewing the **Embedding Service** pod of the Capability Amplification
Wrapper. This is a **dedicated Python service** (not Go) that wraps `all-MiniLM-L6-v2` and
exposes a unary gRPC `Embed` RPC. The Go wrapper consumes it via gRPC. Source of truth:
`docs/reference/architecture.md § Embedding Service`, tech stack matrix, and risk register CR-A / CR-K.

---

## 1 — Proto Contract

Define once; generate both Python and Go stubs from the same `.proto`. Place in `proto/embed/v1/embed.proto`.

```proto
syntax = "proto3";
package embed.v1;
option go_package = "github.com/yourorg/caw/internal/embed/v1;embedv1";

service EmbedService {
  // Embed encodes a single text chunk into a 384-dim float32 vector.
  rpc Embed (EmbedRequest) returns (EmbedResponse);

  // Check implements the standard gRPC health check protocol.
  // Used by /readyz and by the Go circuit breaker.
}

message EmbedRequest {
  string text = 1;
}

message EmbedResponse {
  repeated float embedding = 1 [packed = true]; // 384 float32 values
}
```

Generate stubs:
```bash
# Python
python -m grpc_tools.protoc -I proto --python_out=embed_service --grpc_python_out=embed_service proto/embed/v1/embed.proto

# Go
protoc --go_out=. --go-grpc_out=. proto/embed/v1/embed.proto
```

---

## 2 — Python gRPC Server (EmbedSvc Pod)

```python
# embed_service/server.py

import os
import logging
from concurrent import futures

import grpc
import numpy as np
from grpc_health.v1 import health, health_pb2_grpc
from sentence_transformers import SentenceTransformer

import embed_pb2
import embed_pb2_grpc

log = logging.getLogger(__name__)

# Load model once at startup — do NOT reload per request
MODEL_NAME = "sentence-transformers/all-MiniLM-L6-v2"
_model: SentenceTransformer | None = None


def get_model() -> SentenceTransformer:
    global _model
    if _model is None:
        log.info("Loading model %s ...", MODEL_NAME)
        _model = SentenceTransformer(MODEL_NAME)
        log.info("Model loaded. Embedding dim: %d", _model.get_sentence_embedding_dimension())
    return _model


class EmbedServicer(embed_pb2_grpc.EmbedServiceServicer):
    def __init__(self):
        self.model = get_model()

    def Embed(self, request: embed_pb2.EmbedRequest, context: grpc.ServicerContext) -> embed_pb2.EmbedResponse:
        if not request.text:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "text must not be empty")

        # encode() returns np.ndarray shape (384,)
        # normalise_embeddings=True keeps cosine similarity numerically stable
        vec: np.ndarray = self.model.encode(
            request.text,
            normalize_embeddings=True,
            show_progress_bar=False,
        )
        return embed_pb2.EmbedResponse(embedding=vec.tolist())


def serve() -> None:
    port = os.environ.get("EMBED_PORT", "50051")
    # Architecture CR-K: resources.limits.memory: 512Mi
    # max_workers controls concurrent requests — keep at EMBED_CONCURRENCY (default 4)
    # to match the semaphore cap in the ingest worker (architecture EG-H)
    max_workers = int(os.environ.get("EMBED_CONCURRENCY", "4"))

    server = grpc.server(futures.ThreadPoolExecutor(max_workers=max_workers))
    embed_pb2_grpc.add_EmbedServiceServicer_to_server(EmbedServicer(), server)

    # Standard gRPC health check protocol — used by Go circuit breaker and K8s probes
    health_servicer = health.HealthServicer(
        experimental_non_blocking=True,
        experimental_thread_pool=futures.ThreadPoolExecutor(max_workers=2),
    )
    health_pb2_grpc.add_HealthServicer_to_server(health_servicer, server)
    health_servicer.set("embed.v1.EmbedService", health_pb2.HealthCheckResponse.SERVING)

    server.add_insecure_port(f"[::]:{port}")
    server.start()
    log.info("EmbedSvc listening on :%s (max_workers=%d)", port, max_workers)
    server.wait_for_termination()


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)
    serve()
```

**Rules:**
- `max_workers` MUST equal `EMBED_CONCURRENCY` (default 4). The ingest worker semaphore is
  set to the same value — they must stay in sync (architecture EG-H).
- Model loaded **once** at startup, shared across all threads. `SentenceTransformer.encode()` is
  thread-safe for inference.
- `normalize_embeddings=True` — required for Cosine distance in Qdrant to work correctly.
- Chunk size MUST stay ≤ 400 tokens — all-MiniLM-L6-v2 max context is 512 tokens (architecture
  tech stack matrix).

---

## 3 — requirements.txt

```
sentence-transformers==3.4.1
grpcio==1.71.0
grpcio-tools==1.71.0
grpcio-health-checking==1.71.0
numpy>=1.24
```

**Memory budget (architecture CR-K):**
- `resources.requests.memory: 200Mi`
- `resources.limits.memory: 512Mi` — 256Mi is too tight for Python + gRPC server + model under concurrent load.
- Prometheus alert: `kube_pod_container_status_restarts_total{container="embed-service"} > 2 in 5 min`.

---

## 4 — Kubernetes Pod Spec (Helm: `helm/embed-service/`)

```yaml
# helm/embed-service/templates/deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: caw-embed-service
spec:
  replicas: 1
  selector:
    matchLabels:
      app: caw-embed-service
  template:
    spec:
      containers:
      - name: embed-service
        image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}"
        ports:
        - containerPort: 50051
          name: grpc
        env:
        - name: EMBED_PORT
          value: "50051"
        - name: EMBED_CONCURRENCY
          value: "4"               # MUST match ingest worker semaphore cap
        resources:
          requests:
            memory: "200Mi"
            cpu: "250m"
          limits:
            memory: "512Mi"        # architecture CR-K: raised from 256Mi
            cpu: "1000m"
        livenessProbe:
          grpc:
            port: 50051
            service: "embed.v1.EmbedService"
          initialDelaySeconds: 15
          periodSeconds: 10
        readinessProbe:
          grpc:
            port: 50051
            service: "embed.v1.EmbedService"
          initialDelaySeconds: 10
          periodSeconds: 5
```

---

## 5 — Go gRPC Client (in RAG Retriever + Ingest Worker)

The Go-side client already appears in the `inference-adapter` skill §7 in brief. Full
implementation repeated here for completeness since it belongs to this service:

```go
// internal/embed/client.go

import (
    "context"
    "crypto/sha256"
    "fmt"
    "sync"
    "time"

    lru "github.com/hashicorp/golang-lru/v2"
    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"
    "google.golang.org/grpc/keepalive"
    embedv1 "github.com/yourorg/caw/internal/embed/v1"
)

var ErrRAGDegraded = errors.New("embed service unavailable: RAG degraded mode")

type EmbedClient struct {
    client embedv1.EmbedServiceClient
    cb     *adapter.CircuitBreaker  // 3 failures → open 30s (architecture CR-A)
    cache  *lru.Cache[string, []float32] // 1K entries, SHA256 key (architecture EG-A)
}

func NewEmbedClient(addr string) (*EmbedClient, error) {
    conn, err := grpc.NewClient(addr,
        grpc.WithTransportCredentials(insecure.NewCredentials()),
        grpc.WithDefaultServiceConfig(`{
            "loadBalancingPolicy":"round_robin",
            "healthCheckConfig":{"serviceName":"embed.v1.EmbedService"}
        }`),
        grpc.WithKeepaliveParams(keepalive.ClientParameters{
            Time:                10 * time.Second,
            Timeout:             time.Second,
            PermitWithoutStream: true,
        }),
    )
    if err != nil {
        return nil, err
    }
    cache, _ := lru.New[string, []float32](1000)
    return &EmbedClient{
        client: embedv1.NewEmbedServiceClient(conn),
        cb:     adapter.NewCircuitBreaker(),
        cache:  cache,
    }, nil
}

func (c *EmbedClient) Embed(ctx context.Context, text string) ([]float32, error) {
    // L1: in-process LRU cache — eliminates ~15ms gRPC round-trip for hot queries
    key := fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
    if cached, ok := c.cache.Get(key); ok {
        return cached, nil
    }

    // L2: circuit breaker — open after 3 consecutive gRPC failures
    if !c.cb.Allow() {
        return nil, ErrRAGDegraded
    }

    rpcCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
    defer cancel()

    resp, err := c.client.Embed(rpcCtx, &embedv1.EmbedRequest{Text: text})
    if err != nil {
        c.cb.RecordFailure()
        return nil, ErrRAGDegraded
    }
    c.cb.RecordSuccess()

    vec := resp.GetEmbedding()
    c.cache.Add(key, vec)
    return vec, nil
}
```

**Go client rules (architecture CR-A, EG-A):**
- Circuit breaker: 3 consecutive failures → open 30 s → RAG-degraded mode.
- LRU cache: 1K entries ≈ 1.5 MB at 384-dim float32 — always check before hitting gRPC.
- gRPC timeout per call: 2 s (model inference is ~15 ms; 2 s catches OOMKill delays).
- On `ErrRAGDegraded`: set `x-caw-rag-degraded: true` response header.
- Auto-trigger self-critique when `rag_degraded=true AND domain IN (legal, medical)` (CR-H).

---

## 6 — Ingest Worker Concurrency Cap (Architecture EG-H)

The ingest worker uses a semaphore to cap concurrent EmbedSvc calls at `EMBED_CONCURRENCY=4`.
This value MUST match the Python server's `max_workers`.

```go
// internal/ingest/worker.go

type Worker struct {
    embedClient *embed.EmbedClient
    embedSem    chan struct{} // semaphore: cap at EMBED_CONCURRENCY
}

func NewWorker(embedClient *embed.EmbedClient) *Worker {
    concurrency := envIntOrDefault("EMBED_CONCURRENCY", 4)
    return &Worker{
        embedClient: embedClient,
        embedSem:    make(chan struct{}, concurrency),
    }
}

func (w *Worker) embedChunk(ctx context.Context, text string) ([]float32, error) {
    w.embedSem <- struct{}{}        // acquire slot
    defer func() { <-w.embedSem }() // release slot

    return w.embedClient.Embed(ctx, text)
}
```

---

## Sources

| Library | Stars | Context7 ID | URL |
|---------|-------|-------------|-----|
| huggingface/sentence-transformers | 16k+ | /huggingface/sentence-transformers | https://github.com/UKPLab/sentence-transformers |
| grpc/grpc (Python) | 41k+ | /grpc/grpc | https://grpc.io/docs/languages/python |
| grpc/grpc-go | 21k+ | /grpc/grpc-go | https://github.com/grpc/grpc-go |
