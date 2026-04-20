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
