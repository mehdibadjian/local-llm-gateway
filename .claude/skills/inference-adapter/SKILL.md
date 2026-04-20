---
name: inference-adapter
description: "Best practices for the CAW InferenceBackend interface and pluggable adapters (OllamaAdapter, LlamaCppAdapter, vLLMAdapter). Covers the Go interface contract, context.Context 25s hard deadline, circuit breaker state machine (3 failures → open 30s), streaming token response handling, and adapter-specific HTTP client patterns. Use when implementing or reviewing any InferenceBackend adapter, the circuit breaker, or the streaming generation path."
sources:
  - library: grpc/grpc-go
    context7_id: /grpc/grpc-go
    snippets: 307
    score: 83.3
---

# Inference Adapter Skill — CAW

## Role

You are implementing or reviewing the **Inference Adapter Layer** of the Capability Amplification
Wrapper. This layer decouples the orchestration engine from specific inference backends via a Go
interface. Source of truth: `docs/reference/architecture.md § Deliverable 1, Adapter Layer`.

---

## 1 — InferenceBackend Interface Contract

The interface is the only coupling point between the orchestration engine and any backend.
Swapping backends requires only an env var change (`INFERENCE_BACKEND=ollama|llamacpp|vllm`).

```go
// internal/adapter/interface.go

package adapter

import "context"

// GenerateRequest carries the prompt and generation constraints.
type GenerateRequest struct {
    Prompt      string
    Model       string
    MaxTokens   int           // default 256 for interactive; higher for agent loops
    Temperature float32
    Stream      bool
    Format      string        // "json" for grammar-constrained output
}

// GenerateResponse carries either a full response or the streaming channel.
type GenerateResponse struct {
    Content    string             // non-streaming
    TokenStream <-chan string     // streaming: closed on completion or error
    InputTokens  int
    OutputTokens int
}

// InferenceBackend is the adapter interface. All adapters MUST implement this.
type InferenceBackend interface {
    // Generate runs inference. Context MUST carry a 25s deadline (architecture step 7).
    Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error)

    // HealthCheck verifies the backend process is reachable. Used by /readyz probe.
    // Must NOT do a full inference round-trip (architecture Phase 0 deliverables).
    HealthCheck(ctx context.Context) error
}
```

---

## 2 — Circuit Breaker (Architecture CR-4, CR-A)

The circuit breaker wraps every `InferenceBackend.Generate` call and every `EmbedSvc` gRPC call.
State: **Closed** → **Open** (after 3 consecutive failures) → **Half-Open** (after 30s cooldown).

```go
// internal/adapter/circuitbreaker.go

type State int

const (
    StateClosed   State = iota // normal — pass through
    StateOpen                  // failing — reject immediately
    StateHalfOpen              // testing — allow one probe
)

type CircuitBreaker struct {
    mu          sync.Mutex
    state       State
    failures    int
    threshold   int           // 3 — trips at 3 consecutive failures
    openUntil   time.Time     // reopens after 30s
    cooldown    time.Duration // 30s
}

func NewCircuitBreaker() *CircuitBreaker {
    return &CircuitBreaker{
        threshold: 3,
        cooldown:  30 * time.Second,
    }
}

// Allow returns true if the request may proceed.
func (cb *CircuitBreaker) Allow() bool {
    cb.mu.Lock()
    defer cb.mu.Unlock()
    switch cb.state {
    case StateClosed:
        return true
    case StateOpen:
        if time.Now().After(cb.openUntil) {
            cb.state = StateHalfOpen
            return true // probe request
        }
        return false
    case StateHalfOpen:
        return false // only one probe at a time
    }
    return false
}

// RecordSuccess resets the breaker to Closed.
func (cb *CircuitBreaker) RecordSuccess() {
    cb.mu.Lock()
    defer cb.mu.Unlock()
    cb.state = StateClosed
    cb.failures = 0
}

// RecordFailure increments the failure count; trips to Open after threshold.
func (cb *CircuitBreaker) RecordFailure() {
    cb.mu.Lock()
    defer cb.mu.Unlock()
    cb.failures++
    if cb.failures >= cb.threshold {
        cb.state = StateOpen
        cb.openUntil = time.Now().Add(cb.cooldown)
    }
}
```

**Wrapping the adapter with the circuit breaker:**

```go
func (a *OllamaAdapter) Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
    if !a.cb.Allow() {
        return nil, ErrCircuitOpen
    }
    resp, err := a.doGenerate(ctx, req)
    if err != nil {
        a.cb.RecordFailure()
        return nil, err
    }
    a.cb.RecordSuccess()
    return resp, nil
}
```

---

## 3 — Context Deadline: 25s Hard Limit (Architecture Step 7)

Every `Generate` call MUST be wrapped with a 25s `context.WithDeadline`. This is enforced at
the orchestrator level — adapters receive the already-deadlined context.

```go
// internal/engine/orchestrator.go — wrapping the generate call

func (o *Orchestrator) generate(ctx context.Context, req adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
    // Hard 25s deadline on the entire inference call
    genCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
    defer cancel()

    return o.backend.Generate(genCtx, req)
}
```

**Rules:**
- Interactive turns: `max_tokens = 256`, `stream = true`.
- Agent loop steps: `max_tokens = 512`, `stream = false` (result assembled before next step).
- The 25s deadline is set by the orchestrator, not each adapter — adapters propagate `ctx`.

---

## 4 — OllamaAdapter (Phase 0)

Ollama exposes `/api/generate` (streaming NDJSON) and `/api/chat` (OpenAI-compat).
Use `/api/generate` for direct token streaming; use `context.Context` for cancellation.

```go
// internal/adapter/ollama.go

type OllamaAdapter struct {
    baseURL string
    client  *http.Client
    cb      *CircuitBreaker
}

func NewOllamaAdapter(baseURL string) *OllamaAdapter {
    return &OllamaAdapter{
        baseURL: baseURL,
        client:  &http.Client{Timeout: 0}, // no client-level timeout — context controls it
        cb:      NewCircuitBreaker(),
    }
}

func (a *OllamaAdapter) HealthCheck(ctx context.Context) error {
    hCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
    defer cancel()
    req, _ := http.NewRequestWithContext(hCtx, http.MethodGet, a.baseURL+"/api/tags", nil)
    resp, err := a.client.Do(req)
    if err != nil { return err }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        return fmt.Errorf("ollama unhealthy: %d", resp.StatusCode)
    }
    return nil
}

func (a *OllamaAdapter) doGenerate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
    body, _ := json.Marshal(map[string]any{
        "model":  req.Model,
        "prompt": req.Prompt,
        "stream": req.Stream,
        "options": map[string]any{
            "num_predict": req.MaxTokens,
            "temperature": req.Temperature,
        },
        "format": req.Format, // "json" for grammar-constrained output
    })

    httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
        a.baseURL+"/api/generate", bytes.NewReader(body))
    if err != nil { return nil, err }
    httpReq.Header.Set("Content-Type", "application/json")

    resp, err := a.client.Do(httpReq)
    if err != nil { return nil, err }

    if req.Stream {
        return a.handleStream(resp), nil
    }
    return a.handleFull(resp)
}

func (a *OllamaAdapter) handleStream(resp *http.Response) *GenerateResponse {
    ch := make(chan string, 32)
    go func() {
        defer close(ch)
        defer resp.Body.Close()
        dec := json.NewDecoder(resp.Body)
        for {
            var chunk struct {
                Response string `json:"response"`
                Done     bool   `json:"done"`
            }
            if err := dec.Decode(&chunk); err != nil { return }
            if chunk.Response != "" { ch <- chunk.Response }
            if chunk.Done { return }
        }
    }()
    return &GenerateResponse{TokenStream: ch}
}
```

---

## 5 — LlamaCppAdapter (Phase 1)

llama.cpp in HTTP server mode exposes an OpenAI-compatible `/completion` endpoint with SSE
streaming. Use `bufio.Scanner` on the response body to parse `data: {...}` lines.

```go
// internal/adapter/llamacpp.go

func (a *LlamaCppAdapter) doGenerate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
    body, _ := json.Marshal(map[string]any{
        "prompt":    req.Prompt,
        "n_predict": req.MaxTokens,
        "stream":    req.Stream,
        "temperature": req.Temperature,
    })

    httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost,
        a.baseURL+"/completion", bytes.NewReader(body))
    httpReq.Header.Set("Content-Type", "application/json")

    resp, err := a.client.Do(httpReq)
    if err != nil { return nil, err }

    if req.Stream {
        ch := make(chan string, 32)
        go func() {
            defer close(ch)
            defer resp.Body.Close()
            scanner := bufio.NewScanner(resp.Body)
            for scanner.Scan() {
                line := scanner.Text()
                if !strings.HasPrefix(line, "data: ") { continue }
                var chunk struct {
                    Content string `json:"content"`
                    Stop    bool   `json:"stop"`
                }
                if json.Unmarshal([]byte(line[6:]), &chunk) == nil && chunk.Content != "" {
                    ch <- chunk.Content
                }
                if chunk.Stop { return }
            }
        }()
        return &GenerateResponse{TokenStream: ch}, nil
    }
    // Non-streaming
    defer resp.Body.Close()
    var result struct{ Content string `json:"content"` }
    json.NewDecoder(resp.Body).Decode(&result)
    return &GenerateResponse{Content: result.Content}, nil
}
```

---

## 6 — vLLMAdapter (Phase 2 — OpenAI-compat)

vLLM exposes `POST /v1/completions` with OpenAI-compatible JSON. Stub in Phase 0/1; full
integration tests in Phase 2 (architecture EG-M).

```go
// internal/adapter/vllm.go

func (a *vLLMAdapter) doGenerate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
    body, _ := json.Marshal(map[string]any{
        "model":       req.Model,
        "prompt":      req.Prompt,
        "max_tokens":  req.MaxTokens,
        "temperature": req.Temperature,
        "stream":      req.Stream,
    })
    httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost,
        a.baseURL+"/v1/completions", bytes.NewReader(body))
    httpReq.Header.Set("Content-Type", "application/json")
    httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
    // ... same streaming/full response handling as OllamaAdapter
    return a.handleResponse(req, a.client.Do(httpReq))
}
```

---

## 7 — EmbedSvc gRPC Client (Circuit Breaker Pattern)

The EmbedSvc circuit breaker is identical to the inference breaker: 3 consecutive gRPC failures
→ open 30s → RAG-degraded mode (`x-caw-rag-degraded: true`).

```go
// internal/embed/client.go

type EmbedClient struct {
    conn   *grpc.ClientConn
    client pb.EmbedServiceClient
    cb     *CircuitBreaker  // reuse same CircuitBreaker type from adapter package
    cache  *lru.Cache       // in-process LRU: SHA256(text) → []float32, TTL 5 min, 1K entries
}

func NewEmbedClient(addr string) (*EmbedClient, error) {
    conn, err := grpc.NewClient(addr,
        grpc.WithTransportCredentials(insecure.NewCredentials()),
        grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy":"round_robin","healthCheckConfig":{"serviceName":""}}`),
        grpc.WithKeepaliveParams(keepalive.ClientParameters{
            Time:                10 * time.Second,
            Timeout:             time.Second,
            PermitWithoutStream: true,
        }),
    )
    if err != nil { return nil, err }
    return &EmbedClient{
        conn:   conn,
        client: pb.NewEmbedServiceClient(conn),
        cb:     NewCircuitBreaker(),
        cache:  mustNewLRU(1000), // 1K entries ≈ 1.5 MB at 384-dim float32
    }, nil
}

func (c *EmbedClient) Embed(ctx context.Context, text string) ([]float32, error) {
    // Check in-process LRU cache first
    key := sha256hex(text)
    if cached, ok := c.cache.Get(key); ok {
        return cached.([]float32), nil
    }

    if !c.cb.Allow() {
        return nil, ErrRAGDegraded // caller sets x-caw-rag-degraded: true header
    }

    eCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
    defer cancel()

    resp, err := c.client.Embed(eCtx, &pb.EmbedRequest{Text: text})
    if err != nil {
        c.cb.RecordFailure()
        return nil, ErrRAGDegraded
    }
    c.cb.RecordSuccess()

    vec := resp.GetEmbedding()
    c.cache.Add(key, vec) // TTL managed at LRU level or by periodic flush
    return vec, nil
}
```

**Rules (architecture CR-A, EG-A):**
- Circuit breaker threshold = 3 failures, cooldown = 30s.
- Cache: 1K entries, SHA256 key, TTL 5 min — eliminates ~15 ms gRPC round-trip on hot queries.
- When breaker is open: set `x-caw-rag-degraded: true` response header and skip RAG context.
- Auto-trigger critique pass when `rag_degraded=true AND domain IN (legal, medical)` (CR-H).

---

## 8 — Adapter Selection at Startup

```go
// cmd/server/main.go

func buildInferenceBackend(cfg Config) (adapter.InferenceBackend, error) {
    switch cfg.InferenceBackend { // from INFERENCE_BACKEND env var
    case "ollama":
        return adapter.NewOllamaAdapter(cfg.OllamaURL), nil
    case "llamacpp":
        return adapter.NewLlamaCppAdapter(cfg.LlamaCppURL), nil
    case "vllm":
        return adapter.NewVLLMAdapter(cfg.VLLMUrl, cfg.VLLMAPIKey), nil
    default:
        return nil, fmt.Errorf("unknown inference backend: %s", cfg.InferenceBackend)
    }
}
```

---

## Sources

| Library | Stars | Context7 ID | URL |
|---------|-------|-------------|-----|
| grpc/grpc-go | 21k+ | /grpc/grpc-go | https://github.com/grpc/grpc-go |
| Ollama API | — | — | https://github.com/ollama/ollama/blob/main/docs/api.md |
| llama.cpp server | — | — | https://github.com/ggerganov/llama.cpp/tree/master/examples/server |
