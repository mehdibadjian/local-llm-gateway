---
name: go-fiber-gateway
description: "Best practices for the CAW API Gateway built with Go Fiber v2 (fasthttp). Covers SSE streaming, bounded worker pool, distributed rate limiting, auth middleware, /healthz + /readyz probes, and OpenTelemetry tracing. Use when implementing or reviewing any Fiber handler, middleware, or gateway concern in the CAW service."
sources:
  - library: gofiber/fiber
    context7_id: /gofiber/fiber
    snippets: 1325
    score: 94.72
  - library: gofiber/docs
    context7_id: /gofiber/docs
    snippets: 15195
    score: 82.17
---

# Go Fiber Gateway Skill — CAW

## Role

You are implementing the **API Gateway** layer of the Capability Amplification Wrapper. Every
pattern here is derived from the CAW architecture spec (`docs/reference/architecture.md`) and
verified against current Fiber v3 documentation via Context7.

---

## 1 — SSE Streaming

Use `SendStreamWriter` with a `*bufio.Writer`. Set all four required headers **before** starting
the write loop. Flush after each event; return immediately on flush error (client disconnect).

```go
app.Get("/v1/chat/completions", func(c fiber.Ctx) error {
    c.Set("Content-Type",      "text/event-stream")
    c.Set("Cache-Control",     "no-cache")
    c.Set("Connection",        "keep-alive")
    c.Set("Transfer-Encoding", "chunked")

    return c.SendStreamWriter(func(w *bufio.Writer) {
        for token := range tokenCh {
            fmt.Fprintf(w, "data: %s\n\n", token)
            if err := w.Flush(); err != nil {
                return // client disconnected
            }
        }
        fmt.Fprintf(w, "data: [DONE]\n\n")
        w.Flush()
    })
})
```

**Rules:**
- Never hold a Redis or Qdrant connection open during a `SendStreamWriter` loop.
- Token budget is ≤ 256 tokens/response for interactive turns (architecture Assumption C).
- Flush every individual token — do **not** batch inside the stream writer.

---

## 2 — Bounded Worker Pool (HTTP 429 Backpressure)

The worker pool is the CAW's primary backpressure mechanism. The pool depth (configurable via
`WORKER_POOL_SIZE` env var) determines when to return 429. Track in-flight count via the
`caw_requests_in_flight` Prometheus gauge — KEDA uses this metric to scale wrapper pods.

```go
// internal/api/pool.go
type WorkerPool struct {
    sem chan struct{}
}

func NewWorkerPool(size int) *WorkerPool {
    return &WorkerPool{sem: make(chan struct{}, size)}
}

func WorkerPoolMiddleware(pool *WorkerPool) fiber.Handler {
    return func(c fiber.Ctx) error {
        select {
        case pool.sem <- struct{}{}:
            cawRequestsInFlight.Add(1)          // caw_requests_in_flight gauge +1
            defer func() {
                <-pool.sem
                cawRequestsInFlight.Add(-1)     // gauge -1
            }()
            return c.Next()
        default:
            return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
                "error": "worker pool full",
            })
        }
    }
}
```

**Rules:**
- Check pool **before** touching Redis or any downstream service.
- The gauge value is what KEDA's `sum(caw_requests_in_flight)` query reads — never decrement
  before the response is fully flushed.

---

## 3 — Distributed Redis Rate Limiter

Per-pod token buckets are bypassed by horizontal scaling (20 pods × 60 = 1200 effective).
Use a Redis `INCR` + `EXPIRE` counter for a true global limit.

```go
// Lua script: atomic INCR+EXPIRE (sets TTL only on first call in window)
var rateLimitScript = redis.NewScript(`
    local key     = KEYS[1]
    local limit   = tonumber(ARGV[1])
    local window  = tonumber(ARGV[2])
    local current = redis.call("INCR", key)
    if current == 1 then
        redis.call("EXPIRE", key, window)
    end
    if current > limit then return 0 end
    return 1
`)

func RateLimitMiddleware(rdb *redis.Client) fiber.Handler {
    return func(c fiber.Ctx) error {
        apiKey  := c.Get("Authorization") // "Bearer <key>"
        window  := int64(60)              // 60-second sliding window
        key     := fmt.Sprintf("caw:rate:%s:%d", apiKey, time.Now().Unix()/window)

        allowed, err := rateLimitScript.Run(c.Context(), rdb,
            []string{key}, 60, window).Int()
        if err != nil || allowed == 0 {
            return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
                "error": "rate limit exceeded",
            })
        }
        return c.Next()
    }
}
```

**Rules:**
- Key format: `caw:rate:{api_key}:{window_ts}` where `window_ts = unix_seconds / 60`.
- Redis command budget ≤ 10 ms; use `ReadTimeout: 10*time.Millisecond` on the client.
- Rate limiter middleware runs **before** worker pool check (cheaper rejection first).

---

## 4 — Bearer Token Auth Middleware

```go
func AuthMiddleware(apiKey string) fiber.Handler {
    return func(c fiber.Ctx) error {
        auth := c.Get("Authorization")
        if !strings.HasPrefix(auth, "Bearer ") || auth[7:] != apiKey {
            return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
                "error": "unauthorized",
            })
        }
        return c.Next()
    }
}
```

**Rules:**
- API key MUST come from env var / K8s Secret — never hardcoded.
- Use `subtle.ConstantTimeCompare` to prevent timing side-channel attacks:

```go
import "crypto/subtle"

valid := subtle.ConstantTimeCompare([]byte(auth[7:]), []byte(apiKey)) == 1
```

---

## 5 — Health & Readiness Probes

`/healthz` — liveness: always 200 if the process is alive.
`/readyz` — readiness: checks the **inference backend health endpoint only**. Do NOT do a full
inference round-trip (blocks K8s scale-out for 10–30 s on pod startup).

```go
app.Get("/healthz", func(c fiber.Ctx) error {
    return c.SendStatus(fiber.StatusOK)
})

app.Get("/readyz", func(c fiber.Ctx) error {
    ctx, cancel := context.WithTimeout(c.Context(), 2*time.Second)
    defer cancel()
    if err := inferenceBackend.HealthCheck(ctx); err != nil {
        return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
            "error": err.Error(),
        })
    }
    return c.SendStatus(fiber.StatusOK)
})
```

---

## 6 — Skip Logic Pattern (ResponseTime / Tracing)

Exclude health probes from middleware chains to avoid polluting metrics.

```go
app.Use(responsetime.New(responsetime.Config{
    Next: func(c fiber.Ctx) bool {
        p := c.Path()
        return p == "/healthz" || p == "/readyz" || p == "/metrics"
    },
}))
```

---

## 7 — Middleware Registration Order

```go
app := fiber.New(fiber.Config{
    BodyLimit: 4 * 1024 * 1024,        // 4 MB max body (architecture security rule)
})

app.Use(otelfiber.Middleware())         // OTel tracing — first, captures all
app.Use(AuthMiddleware(cfg.APIKey))     // Auth — reject before rate limit
app.Use(RateLimitMiddleware(rdb))       // Distributed rate limit
app.Use(WorkerPoolMiddleware(pool))     // Backpressure — last gate before handlers
```

---

## 8 — Error Response Contract

All errors MUST follow the OpenAI error shape for client compatibility:

```go
return c.Status(code).JSON(fiber.Map{
    "error": fiber.Map{
        "message": msg,
        "type":    errType,  // e.g. "rate_limit_error", "server_error"
        "code":    code,
    },
})
```

---

## Sources

| Library | Stars | Context7 ID | URL |
|---------|-------|-------------|-----|
| gofiber/fiber | 35k+ | /gofiber/fiber | https://github.com/gofiber/fiber |
| gofiber/docs | — | /gofiber/docs | https://docs.gofiber.io |
