---
name: otel-observability
description: "Best practices for OpenTelemetry Go in CAW. Covers the 6 canonical caw_* Prometheus metrics, OTel trace spans for critical path operations, meter/tracer initialization, and attribute conventions. Use when implementing or reviewing any observability, metrics, or tracing concern in the CAW service."
sources:
  - library: open-telemetry/opentelemetry-go
    context7_id: /open-telemetry/opentelemetry-go
    snippets: 265
    score: 76.88
  - library: opentelemetry.io
    context7_id: /websites/opentelemetry_io
    snippets: 14068
    score: 77.04
---

# OTel Observability Skill — CAW

## Role

You are implementing or reviewing observability in the Capability Amplification Wrapper.
Every metric name here is **canonical** — KEDA ScaledObjects and Prometheus alert rules
reference these exact strings. Changing them breaks autoscaling.

---

## 1 — Canonical CAW Prometheus Metrics

These 6 metrics MUST be exported by the wrapper service. Names are frozen.

```go
// internal/metrics/metrics.go
package metrics

import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/metric"
)

var (
    // caw_requests_in_flight — KEDA wrapper scaler trigger
    RequestsInFlight metric.Int64UpDownCounter

    // caw_redis_latency_seconds — alert p99 > 5ms
    RedisLatency metric.Float64Histogram

    // caw_ingest_dlq_depth — alert > 10
    IngestDLQDepth metric.Int64ObservableGauge

    // caw_retrieval_leg_timeout_total{leg="ann|fts"}
    RetrievalLegTimeout metric.Int64Counter

    // caw_rag_degraded_total{domain}
    RAGDegradedTotal metric.Int64Counter

    // caw_critique_pass_total{trigger}
    CritiquePassTotal metric.Int64Counter
)

func Init() error {
    meter := otel.Meter("caw")

    var err error
    RequestsInFlight, err = meter.Int64UpDownCounter("caw_requests_in_flight",
        metric.WithDescription("Current requests held in worker pool; KEDA scaler trigger"),
    )
    if err != nil { return err }

    RedisLatency, err = meter.Float64Histogram("caw_redis_latency_seconds",
        metric.WithDescription("Per-command Redis round-trip time"),
        metric.WithUnit("s"),
        metric.WithExplicitBucketBoundaries(0.001, 0.005, 0.010, 0.025, 0.050, 0.100),
    )
    if err != nil { return err }

    IngestDLQDepth, err = meter.Int64ObservableGauge("caw_ingest_dlq_depth",
        metric.WithDescription("Pending entries in dead-letter stream; alert > 10"),
    )
    if err != nil { return err }

    RetrievalLegTimeout, err = meter.Int64Counter("caw_retrieval_leg_timeout_total",
        metric.WithDescription("Count of 300ms retrieval timeouts per leg"),
    )
    if err != nil { return err }

    RAGDegradedTotal, err = meter.Int64Counter("caw_rag_degraded_total",
        metric.WithDescription("Requests served in RAG-degraded mode"),
    )
    if err != nil { return err }

    CritiquePassTotal, err = meter.Int64Counter("caw_critique_pass_total",
        metric.WithDescription("Self-critique pass invocations by trigger type"),
    )
    return err
}
```

---

## 2 — Recording Each Metric

### `caw_requests_in_flight` (Worker Pool Gauge)

```go
// In WorkerPool middleware
metrics.RequestsInFlight.Add(ctx, 1)      // on acquire
defer metrics.RequestsInFlight.Add(ctx, -1) // on release
```

### `caw_redis_latency_seconds` (Per-command histogram)

```go
func RecordRedisOp(ctx context.Context, operation string, fn func() error) error {
    start := time.Now()
    err := fn()
    metrics.RedisLatency.Record(ctx, time.Since(start).Seconds(),
        metric.WithAttributes(attribute.String("operation", operation)),
    )
    return err
}
// Usage:
RecordRedisOp(ctx, "rpush_ltrim", func() error { _, err := rdb.Pipelined(...); return err })
```

### `caw_ingest_dlq_depth` (Observable Gauge — polled async)

```go
meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
    depth, _ := rdb.XLen(ctx, "caw:ingest:dlq").Result()
    o.ObserveInt64(metrics.IngestDLQDepth, depth)
    return nil
}, metrics.IngestDLQDepth)
```

### `caw_retrieval_leg_timeout_total`

```go
// In hybrid retriever — on errgroup leg context deadline exceeded
metrics.RetrievalLegTimeout.Add(ctx, 1,
    metric.WithAttributes(attribute.String("leg", "ann")), // or "fts"
)
```

### `caw_rag_degraded_total`

```go
// When EmbedSvc circuit breaker is open
metrics.RAGDegradedTotal.Add(ctx, 1,
    metric.WithAttributes(attribute.String("domain", domain)),
)
c.Set("x-caw-rag-degraded", "true")
```

### `caw_critique_pass_total`

```go
// trigger values: opt_in | auto_legal | auto_medical | rag_degraded | side_effect
metrics.CritiquePassTotal.Add(ctx, 1,
    metric.WithAttributes(attribute.String("trigger", trigger)),
)
```

---

## 3 — OTel Tracer: Critical Path Spans

Instrument the 11-step critical path with named spans. Each step becomes a child span.

```go
// internal/engine/orchestrator.go
func (o *Orchestrator) HandleRequest(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
    tracer := otel.Tracer("caw/orchestrator")
    ctx, span := tracer.Start(ctx, "orchestrator.handle",
        trace.WithAttributes(
            attribute.String("domain", req.Domain),
            attribute.Bool("rag_enabled", req.RAGEnabled),
            attribute.Bool("agent_mode", req.AgentMode),
        ),
    )
    defer span.End()

    // Step 3: context load
    ctx, ctxSpan := tracer.Start(ctx, "context_manager.load")
    history, err := o.ctxMgr.Load(ctx, req.SessionID)
    ctxSpan.End()

    // Step 5: RAG retrieval
    ctx, ragSpan := tracer.Start(ctx, "rag.hybrid_search")
    chunks, err := o.rag.HybridSearch(ctx, req.Domain, req.Query, queryVec)
    ragSpan.End()

    // Step 7: inference
    ctx, inferSpan := tracer.Start(ctx, "inference.generate")
    resp, err := o.adapter.Generate(ctx, prompt, constraints)
    inferSpan.End()

    // Step 10: self-critique (if triggered)
    if shouldCritique(req, err) {
        ctx, critiqueSpan := tracer.Start(ctx, "self_critique.pass")
        resp, err = o.critiquer.Verify(ctx, resp)
        critiqueSpan.RecordError(err)
        critiqueSpan.End()
    }

    if err != nil { span.RecordError(err) }
    return resp, err
}
```

**Span naming convention:** `{component}.{operation}` — e.g., `rag.hybrid_search`,
`inference.generate`, `context_manager.compress`, `self_critique.pass`.

---

## 4 — OTel SDK Bootstrap

```go
// cmd/server/otel.go
func setupOTel(ctx context.Context, endpoint string) (func(), error) {
    exporter, err := otlptracehttp.New(ctx,
        otlptracehttp.WithEndpoint(endpoint),
        otlptracehttp.WithInsecure(),
    )
    if err != nil { return nil, err }

    tp := sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(exporter),
        sdktrace.WithResource(resource.NewWithAttributes(
            semconv.SchemaURL,
            semconv.ServiceName("caw-wrapper"),
            semconv.ServiceVersion("1.0.0"),
        )),
    )
    otel.SetTracerProvider(tp)

    shutdown := func() { tp.Shutdown(ctx) }
    return shutdown, nil
}
```

---

## 5 — Prometheus Exposition (Fiber /metrics endpoint)

```go
import "github.com/ansrivas/fiberprometheus/v2"

prometheus := fiberprometheus.New("caw")
prometheus.RegisterAt(app, "/metrics")
app.Use(prometheus.Middleware)
```

**Rule:** Exclude `/metrics`, `/healthz`, `/readyz` from tracing middleware to avoid noise.

---

## 6 — Alert Thresholds (Canonical)

| Metric | Alert Condition |
|--------|----------------|
| `caw_redis_latency_seconds` p99 | > 5ms |
| `caw_ingest_dlq_depth` | > 10 |
| `redis_memory_used_bytes / redis_memory_max_bytes` | > 0.80 |
| `kube_pod_container_status_restarts_total{container="embed-service"}` | > 2 in 5 min |
| `pg_stat_activity_count` | > 50 (triggers PgBouncer) |

---

## Sources

| Library | Stars | Context7 ID | URL |
|---------|-------|-------------|-----|
| open-telemetry/opentelemetry-go | 5k+ | /open-telemetry/opentelemetry-go | https://github.com/open-telemetry/opentelemetry-go |
| opentelemetry.io | — | /websites/opentelemetry_io | https://opentelemetry.io/docs/languages/go |
