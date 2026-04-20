# Darwin-MCP — Copilot Instructions

## Project Identity

This is **mcp-evolution-core** — the Brain of the Darwin-MCP system. It is a stateless MCP SSE server deployed on a $5 Droplet that enables a Host LLM to evolve, register, and invoke AI skills (species) at runtime.

## Architecture at a Glance

- **Brain** (`brain/`): Stateless MCP SSE server — the public repo, lives on the Droplet
- **Memory** (`memory/`): Git submodule (`mcp-evolution-vault`) — private, stateful, stores species and registry
- **Registry** (`memory/dna/registry.json`): Single source of truth for all registered skills
- **Species** (`memory/species/`): Python files representing individual AI tools

## Key Components

| File | Role |
|------|------|
| `brain/bridge/sse_server.py` | SSE transport layer with Bearer Token auth |
| `brain/engine/mutator.py` | `request_evolution` pipeline — sandbox → test → promote |
| `brain/engine/sandbox.py` | Temporary virtualenv isolation for mutations |
| `brain/engine/guard.py` | Circuit breaker (recursion depth, CPU/RAM, Toxic flag) |
| `brain/utils/git_manager.py` | Git state machine scoped to `/memory` |
| `brain/utils/registry.py` | Registry read/write operations |
| `brain/watcher/hot_reload.py` | Watchdog file watcher, emits `list_changed` |
| `darwin.service` | Systemd service for Droplet deployment |

## Conventions

- All tests live in `tests/` and use `pytest`
- The agile backlog is the authoritative task list: `docs/reference/agile-backlog.md`
- The technical spec is: `docs/reference/technical-manifesto.md`
- Story format: `US-N` (user story number from the backlog)
- Sprint velocity: ~25–28 story points per 2-week sprint

## Agent Roles

Select the agent role that matches the task. Each role has a corresponding prompt in `.github/prompts/`.

### Story Implementer (`story-implementer.prompt.md`)
Implements user stories from `docs/reference/agile-backlog.md` using TDD. Writes failing tests first, implements code to pass them, then commits with `feat(US-N): <title>`. Use when asked to "implement stories", "run the sprint", or "work on user stories".

### GitHub Asset Hunter (`github-asset-hunter.prompt.md`)
Searches public GitHub repositories to find and extract the best AI skills, prompts, agents, and instructions for a specified need. Synthesizes findings into a production-ready asset saved to `.claude/skills/` or `.github/prompts/`. Use when asked to "find a skill", "search for a prompt", or "discover an agent".

### Manifesto-to-Epics (`manifesto-to-epics.prompt.md`)
Converts a technical manifesto or architecture spec into a fully structured Agile backlog — Epics, Features, User Stories with INVEST criteria, Given/When/Then acceptance criteria, Fibonacci story points, and sprint plans. Use when asked to "create stories from a spec", "break this into epics", or "generate a backlog".

### Wiki Architect (`wiki-architect.prompt.md`)
Produces structured wiki catalogues and onboarding guides from the codebase. Emits a hierarchical JSON catalogue covering Principal-Level Guide, Zero-to-Hero Learning Path, Getting Started, and Deep Dive sections — every section cites real file paths. Use when asked to "create a wiki", "document this repo", or "architecture overview".

### Senior Architect (`.claude/skills/senior-architect/SKILL.md`)
Transforms raw ideas into production-ready architectural blueprints. Enforces a Sharp Questions discovery phase before producing formal artifacts: HLA, Data Schema, API Design, IaC Strategy, and Delivery Roadmap. Use when asked to "architect a system", "design a solution", "harden a project", "create an HLA", "design a schema", "define an API contract", or "build a delivery roadmap".

### Evolutionary Step (`.claude/skills/evolutionary-step/SKILL.md`)
Runs the MCP server (starts it if needed), then follows a mission to extend memory by evolving a new skill via the `/evolve` pipeline. Use when asked to "run an evolutionary step", "evolve a skill", "extend memory", or "teach Darwin to do X".
