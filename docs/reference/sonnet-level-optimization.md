# Sonnet-Level Optimization Plan — Apple M3 (8 GB)

## Objective

Eliminate memory thrashing and reasoning drift to achieve Claude 3.5 Sonnet parity using
1.58-bit (ternary weight) models. Runs fully offline on Apple M3 hardware.

**Hard Constraints**
- Total system RAM: < 6.5 GB
- Zero Python dependencies
- Zero Docker dependencies
- All logic in Go 1.21+ (module currently at `go 1.25.1` — compatible)

**RAM Budget**
| Component | Target |
|---|---|
| BitNet model (i2_s ternary) | ~2.2 GB |
| BGE-Small-v1.5 ONNX embedder | ~33 MB |
| Go runtime + Redis + Qdrant | ~200 MB |
| **Total** | **< 2.5 GB active** |

---

## Codebase State (at planning time)

| Area | Current State | Required Delta |
|---|---|---|
| `internal/adapter/` | Ollama, LlamaCpp, vLLM + `InferenceBackend` interface + circuit breaker | Add `BitNetAdapter` |
| `internal/embed/client.go` | HTTP client → Python EmbedSvc | Replace with in-process ONNX |
| `embed_service/` | Python HTTP server (`server.py`, `http_server.py`) | Delete after migration |
| `internal/tools/sandbox.go` | `os/exec` + cgroup (Linux) or plain exec (macOS) | Add wazero dual-path for WASM plugins |
| `internal/tools/dispatcher.go` | No input sanitization | Add security filter middleware |
| `internal/orchestration/task_planner.go` | 2-phase CoT decomposer | Add 3-pass PEV state machine |
| `internal/grammar/` | Does not exist | Create with `json.gbnf` + `bash.gbnf` |
| `internal/memory/session.go` | 200-entry Redis list, no token budget | Add `HistoryManager` with 2 000-token cap |
| `internal/gateway/chat.go` | No caching | Add in-process semantic LRU cache |
| `internal/observability/metrics.go` | 5 canonical `caw_*` metrics | Add `caw_heap_alloc_mb` + RAM watchdog |

---

## Risk Register

### 🔴 CRITICAL — Wazero Only Runs WASM (Phase 3 · T7)

The plan requires replacing all `os/exec` with `wazero`. **wazero executes WASM binaries only** —
it cannot run arbitrary shell commands or existing plugin scripts.

**Resolution:** Dual-path sandbox:

- `CODEEXEC_RUNTIME=wasm` → wazero path for `.wasm` plugin tools (strict, in-process, 10 s timeout, `./workspace` FS mount only)
- Default path retains `exec.Command` **but is always preceded by the security filter (Phase 3 · T8)**

The security filter is the primary safety layer on macOS/M3. wazero becomes available as an
opt-in for teams shipping compiled WASM tool plugins.

### 🟡 MEDIUM — ONNX Runtime Has a CGo Dependency (Phase 1 · T2)

`onnxruntime-go` requires `CGO_ENABLED=1` and the pre-built `libonnxruntime` shared library for
arm64. This is **not Python**, but it is a native C library that must be present on the host.

- Target: BGE-Small-v1.5 ONNX (~33 MB idle) vs current Python EmbedSvc (~500 MB). ✅ Meets `<50 MB` requirement.
- "Zero Python" is fully satisfied. "Zero C libraries" is not achievable for ONNX — this is expected and acceptable.

### 🟡 MEDIUM — BitNet.cpp API Compatibility (Phase 1 · T1)

BitNet.cpp's HTTP server exposes a llama.cpp-compatible API. The `i2_s` quantization flag is a
**server launch argument** (`./server -m model.gguf -q i2_s`), not a Go runtime flag. The Go
adapter points `baseURL` at the running bitnet.cpp process — identical pattern to `OllamaAdapter`.

`Accelerate.framework` linkage and AMX instruction usage are compile-time concerns for the
bitnet.cpp binary, not the Go codebase.

### 🟢 LOW — `internal/memory/redis.go` Reference

The original spec references `internal/memory/redis.go` for the `HistoryManager`. This file does
not exist — session logic lives in `internal/memory/session.go`. Implementation target is `session.go`.

### 🟢 LOW — Semantic Cache Does Not Require Redis Vector Ops

The spec says "check against Redis" for the semantic cache. Redis without RedisSearch has no
native vector operations. Resolution: in-process `LRU(256)` of `(embedding []float32, response string)`
pairs with Go-computed cosine similarity. Cache miss falls through to normal generation. No Redis
schema changes needed.

---

## Phase 1 — Low-Memory Foundation

### T1 · BitNet.cpp Adapter
**File:** `internal/adapter/bitnet.go` (CREATE), `internal/adapter/factory.go` (MODIFY)

- Implement `BitNetAdapter` satisfying `InferenceBackend` (Generate, Stream, HealthCheck)
- HTTP client to bitnet.cpp server (`BITNET_BASE_URL`, default `http://localhost:8080`)
- Reuse existing circuit breaker pattern from `OllamaAdapter`
- Register via `INFERENCE_BACKEND=bitnet` in `factory.go`
- **Hard Assertion:** Model must use `i2_s` quantization. Document required server launch command:
  ```
  ./server -m BitNet-b1.58-2B-4T.gguf -q i2_s --port 8080
  ```

### T2 · Go-Native ONNX Embedding Migration
**Files:** `internal/embed/onnx_client.go` (CREATE), `internal/embed/factory.go` (CREATE), `embed_service/` (DELETE after migration)

- Implement `ONNXEmbedClient` satisfying `embed.EmbedClient` using `github.com/yalue/onnxruntime_go`
- Load BGE-Small-v1.5 ONNX model at startup (path via `EMBED_MODEL_PATH` env var)
- Preserve existing `LRUCache` + `CircuitBreaker` wrappers
- Add `EMBED_BACKEND=onnx` env var toggle in new `factory.go`; default `http` keeps current behaviour for backwards compatibility
- Delete `embed_service/` only after `ONNXEmbedClient` tests pass
- **Hard Assertion:** No Python bridge. Model runs in-process via CGo + arm64 `libonnxruntime.dylib`

### T3 · Resource-Aware Semaphore
**File:** `internal/adapter/semaphore.go` (CREATE)

- Implement `GlobalResourceSemaphore` using `golang.org/x/sync/semaphore`
- Weight assignments: LLM `Generate` = 2, `Embed` call = 1, `maxWeight` = 2
- Ensures embedder and LLM never run at 100% CPU concurrently
- Wire into `chatComplete` handler and embed client `Embed()` method
- **Constraint:** Must prevent thermal throttling and RAM spikes on 8 GB unified memory

---

## Phase 2 — Reasoning & Reliability

### T4 · Plan-Execute-Verify (PEV) State Machine
**Files:** `internal/orchestration/pev.go` (CREATE), `internal/orchestration/pipeline.go` (MODIFY)

- Add `PEVOrchestrator` struct (separate from existing `ChainOfThoughtDecomposer`)
- **Pass 1 — Plan:** Prompt model to output ONLY a JSON `<plan>` object. If raw code is detected in the response, strip and re-prompt (max 2 retries before fallback to CoT)
- **Pass 2 — Execute:** Run plan steps via tool dispatcher
- **Pass 3 — Verify:** Self-verify output against plan using a structured rubric prompt; surface score in response metadata
- Wire into `pipeline.go` when `intent == IntentCodeGeneration || IntentComplexReasoning`

### T5 · GBNF Grammar Enforcement
**Files:** `internal/grammar/json.gbnf` (CREATE), `internal/grammar/bash.gbnf` (CREATE), `internal/grammar/grammar.go` (CREATE)

- Define grammar schemas for structured JSON tool calls and bash command output
- Add `LoadGrammar(name string) (string, error)` helper backed by `go:embed`
- Modify tool-call dispatch path in `dispatcher.go`: populate `GenerateRequest.Grammar` from the appropriate grammar before calling `backend.Generate`
- Note: `GenerateRequest.Grammar` field already exists in `internal/adapter/backend.go`

### T6 · Context Window Squeezer
**File:** `internal/memory/session.go` (MODIFY)

- Add `HistoryManager` wrapping `SessionStore`
- Token estimator: `len(text) / 4` (no CGo, no external tokenizer)
- If estimated token count of loaded history exceeds 2 000:
  1. Call `backend.Generate` with prompt: `"Summarize the conversation state into a compact JSON object preserving key facts, decisions, and unresolved questions."`
  2. Store summary as a synthetic `system` message
  3. Discard raw turn tokens, keep only the summary
- Wire into the gateway chat handler session load path

---

## Phase 3 — Secure Tool Execution

### T7 · Wazero WASM Sandbox
**File:** `internal/tools/sandbox.go` (MODIFY), `go.mod` (MODIFY — add `github.com/tetratelabs/wazero`)

- Add wazero path selected by `CODEEXEC_RUNTIME=wasm`
- WASM path: instantiate wazero runtime, mount only `./workspace` via WASI `fs.DirFS`, enforce 10 s context timeout, no network access
- Default path: retains `exec.Command` but always runs through security filter first
- `BuildCommandForTest` remains available for white-box tests on the exec path

### T8 · Non-Interactive Security Filter
**File:** `internal/tools/dispatcher.go` (MODIFY)

- Add `preDispatchFilter(call ToolCall) error` called before any executor
- Blocked tokens (string-level scan of `call.Input`): `chmod`, `sudo`, `curl`, `wget`, `rm -rf`, `eval`, `base64`
- Applied to `subprocess` and `plugin` executor types only
- Return typed `ErrForbiddenCommand` (not a generic error) so callers can distinguish security rejections
- Unit tests must cover every blocked token

---

## Phase 4 — Performance & Monitoring

### T9 · Semantic Cache
**File:** `internal/gateway/chat.go` (MODIFY)

- In `chatComplete`, before `backend.Generate`:
  1. Embed the last user message via injected `embed.EmbedClient`
  2. Scan in-process `LRU(256)` of `(embedding []float32, response string)` pairs
  3. If cosine similarity ≥ 0.95: return cached response, set header `X-CAW-Cache-Hit: semantic`
- Store new `(embedding, response)` pairs after successful generation
- No Redis schema changes needed
- **Dependency:** Requires ONNX embed client (T2) to be in place for sub-50 ms embedding latency

### T10 · RAM Watchdog + Prometheus Metric
**Files:** `internal/observability/watchdog.go` (CREATE), `internal/observability/metrics.go` (MODIFY)

- `StartRAMWatchdog(backend InferenceBackend, thresholdMB uint64)` polls `runtime.ReadMemStats` every 5 s
- If `HeapAlloc > thresholdMB * 1024 * 1024` (default 5 120 MB): pause new task dispatch and return `RESOURCE_EXHAUSTED` to callers
- Add `caw_heap_alloc_mb` as a `prometheus.NewGaugeFunc` reading `HeapAlloc / 1 MiB`
- Register in `observability.RegisterMetrics()`
- Wire `StartRAMWatchdog` in `cmd/wrapper/main.go` startup

---

## File Change Summary

| File | Action |
|---|---|
| `internal/adapter/bitnet.go` | CREATE |
| `internal/adapter/factory.go` | MODIFY — add `bitnet` case |
| `internal/adapter/semaphore.go` | CREATE |
| `internal/embed/onnx_client.go` | CREATE |
| `internal/embed/factory.go` | CREATE |
| `embed_service/` | DELETE (after T2 tests pass) |
| `internal/grammar/json.gbnf` | CREATE |
| `internal/grammar/bash.gbnf` | CREATE |
| `internal/grammar/grammar.go` | CREATE |
| `internal/orchestration/pev.go` | CREATE |
| `internal/orchestration/pipeline.go` | MODIFY |
| `internal/memory/session.go` | MODIFY |
| `internal/tools/dispatcher.go` | MODIFY |
| `internal/tools/sandbox.go` | MODIFY |
| `internal/gateway/chat.go` | MODIFY |
| `internal/observability/watchdog.go` | CREATE |
| `internal/observability/metrics.go` | MODIFY |
| `go.mod` | MODIFY — add `onnxruntime-go`, `wazero` |

---

## Implementation Order

```
Phase 1 (must complete first — foundational memory model)
  ① p1-resource-semaphore   low effort / low risk
  ② p1-bitnet-adapter       medium effort / low risk
  ③ p1-onnx-embed           high effort / high risk  ← unblocks semantic cache

Phase 2 (reasoning layer)
  ④ p2-gbnf-grammar         low effort / low risk
  ⑤ p2-pev-state-machine    medium effort / medium risk  ← depends on ④
  ⑥ p2-context-squeezer     medium effort / medium risk

Phase 3 (security layer)
  ⑦ p3-security-filter      low effort / low risk
  ⑧ p3-wazero-sandbox       high effort / high risk  ← depends on ⑦

Phase 4 (performance + observability)
  ⑨ p4-ram-watchdog         medium effort / low risk
  ⑩ p4-semantic-cache       medium effort / medium risk  ← depends on ③
```

---

## Hard Constraints Compliance

| Constraint | Satisfied by |
|---|---|
| `i2_s` quantization — never FP16/Q4_K_M | BitNetAdapter passes model via env; rejects other quant strings at config validation |
| No Python bridge | ONNX runs in-process via CGo (libonnxruntime arm64) |
| No `exec.Command` for WASM tools | wazero path enforced via `CODEEXEC_RUNTIME=wasm` |
| Embedder + LLM never at 100% concurrent | Weighted semaphore, max_weight=2 |
| Context > 2 000 tokens → summarize | HistoryManager in `session.go` |
| HeapAlloc > 5 GB → RESOURCE_EXHAUSTED | RAM watchdog goroutine |
| Total RAM < 6.5 GB | BitNet ~2.2 GB + BGE ~33 MB + runtime ~200 MB ✅ |
