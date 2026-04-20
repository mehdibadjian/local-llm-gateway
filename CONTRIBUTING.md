# Contributing to CAW

Thanks for your interest in making local AI more capable. CAW is a practical project — contributions that make `gemma:2b` (or any small model) more useful in real workflows are the highest priority.

---

## What we're looking for

| Area | Examples |
|---|---|
| **Inference adapters** | vLLM adapter, OpenAI-compat pass-through, llama.cpp HTTP |
| **Agentic loop improvements** | Better DONE detection, retry-with-feedback, multi-step planning |
| **RAG & retrieval** | Reranker improvements, BM25 tuning, chunking strategies |
| **Tool plugins** | New community tools (JSON schema → subprocess binary) |
| **Benchmark harness** | New MMLU categories, HumanEval variants, real-world task evals |
| **Observability** | Dashboard improvements, alert rules, trace coverage |
| **Documentation** | Architecture explainers, tutorials, deployment guides |

---

## Development setup

### Prerequisites

- Go 1.21+
- Docker + Docker Compose
- [Ollama](https://ollama.ai) with `gemma:2b` pulled
- Python 3.10+ (for embedding service)

### Start the local stack

```bash
git clone https://github.com/caw/wrapper.git
cd wrapper
docker compose up -d
```

Verify it's running:

```bash
curl http://localhost:8080/healthz
# → {"status":"ok"}
```

### Run the tests

```bash
go test ./tests/... -count=1
```

All 12 test packages must pass with 0 failures before you open a PR.

---

## Workflow

### 1. Branch

```bash
git checkout -b feat/my-feature
```

Use the prefix that matches your change:

| Prefix | When |
|---|---|
| `feat/` | New feature or capability |
| `fix/` | Bug fix |
| `chore/` | Tooling, CI, dependency updates |
| `docs/` | Documentation only |
| `test/` | Test-only changes |

### 2. Write failing tests first (TDD)

Every behavioural change must have a test. Write the test, confirm it fails, then implement.

```bash
# Confirm test is red
go test ./tests/your-package/... -run TestYourFeature -v

# Implement
# ...

# Confirm test is green
go test ./tests/your-package/... -run TestYourFeature -v
```

Tests live in `tests/` mirroring the `internal/` layout. Never put tests inside `internal/`.

### 3. Quality checks

```bash
go vet ./...
gofmt -l .          # should print nothing
go test ./tests/... -count=1
```

### 4. Commit

Use the canonical commit format:

```
feat(US-XX): short present-tense description

Optional body explaining the why, not the what.

Co-authored-by: Your Name <you@example.com>
```

If your change doesn't map to a backlog story, omit `(US-XX)`.

Examples:
- `feat(US-12): add Qdrant collection alias for zero-downtime reindex`
- `fix: prevent DONE signal from firing inside bash code blocks`
- `chore: bump go-redis to v9.6.0`

### 5. Pull request

- Target `main`
- Title mirrors the commit subject
- Describe **what** changed and **why** (not how — the code shows that)
- Link related issues or backlog stories if applicable

---

## Architecture decisions

Before starting a large change, open an issue or discussion to align on approach. The core design constraints are:

- **Stateless service** — no in-process state beyond the worker pool counter. All state goes to Redis / Postgres / Qdrant.
- **No vendor lock-in** — every external dependency is behind an interface. The inference backend, embedding service, and vector store are all swappable.
- **Security by default** — auth middleware runs before all handlers. Constant-time token comparison. No secrets in code.
- **Offline-first** — the system must work with zero internet access. Web augmentation is an optional enhancement, never a hard dependency.

---

## Adding a tool plugin

Plugins are subprocess binaries that read a JSON request from stdin and write a JSON response to stdout.

```bash
# 1. Build your binary
go build -o my-tool ./cmd/tools/my-tool

# 2. Place in plugin directory
cp my-tool /plugins/my-tool

# 3. Register in tool schema (tools/loader.go)
# CAW auto-discovers binaries in CAW_PLUGIN_DIR
```

Plugin contract:

```json
// stdin
{"tool": "my-tool", "input": {"query": "..."}}

// stdout
{"output": "...", "error": null}
```

---

## Reporting bugs

Open a GitHub issue with:

1. **Minimal reproduction** — the exact `curl` command or test that triggers the bug
2. **Expected vs actual** — what you expected, what happened
3. **Environment** — Go version, model name, Docker version

---

## Code style

- **Go idioms** — `if err != nil` not panics; named return values only when they genuinely aid clarity
- **Comments only where needed** — complex regex, non-obvious algorithm steps, or interface contracts
- **No `fmt.Println` in production paths** — use structured logging or the OTel tracer
- **Errors wrap with context** — `fmt.Errorf("step %d: %w", i, err)`

---

## License

By contributing you agree your code is released under the [MIT License](LICENSE).
