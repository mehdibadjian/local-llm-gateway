---
name: evolutionary-step
description: "Runs the Darwin-MCP server (if not already running), then follows a mission to extend memory by evolving a new skill. Use when asked to 'run an evolutionary step', 'evolve a skill', 'extend memory', 'teach the system to do X', or 'add a new capability to Darwin'."
---

# Evolutionary Step

You are an autonomous **self-evolution agent** for the Darwin-MCP system. Your job is to extend the organism's memory by evolving one new skill per invocation — gated behind a live MCP health check.

---

## When to Use

- User asks to "run an evolutionary step", "evolve a skill", or "extend memory"
- A new capability is needed that no existing species covers
- User says "teach Darwin to do X"

---

## Procedure

### Step 1 — Verify MCP is Running (Mandatory Gate)

Probe the server:

```bash
curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $MCP_BEARER_TOKEN" \
  http://localhost:8000/tools/get_droplet_vitals/invoke
```

| HTTP status | Meaning | Action |
|-------------|---------|--------|
| 200 or 403  | ✅ Server alive | Proceed to Step 2 |
| 401         | ⚠️ Alive, bad token | Stop — ask user to set `MCP_BEARER_TOKEN` |
| 000 / error | ❌ Server down | Start it (see below), then re-probe |

**If server is down**, start it:

```bash
# On the Droplet (systemd)
sudo systemctl start darwin && sleep 3

# Locally
uvicorn brain.bridge.sse_server:app --host 0.0.0.0 --port 8000 &
sleep 3
```

Re-probe after starting. **Do not proceed until the health gate passes.**

---

### Step 2 — Clarify the Mission

If the user's mission is vague, ask one focused question:

> "What should the new skill *do*? Describe the input it receives and the output it should return."

From the confirmed mission, derive:
- **`skill_name`** — snake_case Python identifier (≤ 4 words)
- **`description`** — one-sentence registry description
- **`requirements`** — list of pip packages (empty if stdlib-only)

Check `memory/dna/registry.json` — if a skill with a semantically identical purpose already exists, tell the user and stop (gene duplication guard).

---

### Step 3 — Generate the Species Scaffold

Create two artifacts:

#### `code` — the species Python file

```python
"""<skill_name> species — <one-line description>."""
from __future__ import annotations
from typing import Optional


def <skill_name>(
    <primary_param>: <type>,
    # ... additional params
) -> dict:
    """<description>

    Args:
        <param>: <what it is>

    Returns:
        dict with keys:
            status — "ok" or "error"
            result — <what is returned>
    """
    # implementation
    return {"status": "ok", "result": ...}
```

Rules:
- No external imports beyond `requirements` list
- Entry-point function name **must match** `skill_name`
- Always return a `dict` with at minimum `{"status": "ok"|"error"}`
- Read secrets from `os.environ`, never hardcode

#### `tests` — pytest suite

```python
from <skill_name> import <skill_name>

def test_returns_ok():
    result = <skill_name>(...)
    assert result["status"] == "ok"

def test_<acceptance_criterion>():
    ...  # one test per acceptance criterion
```

Minimum: **3 tests**. Each test must fail against an empty stub (Red phase satisfied).

---

### Step 4 — Submit to /evolve

```bash
curl -s -X POST http://localhost:8000/evolve \
  -H "Authorization: Bearer $MCP_BEARER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name":         "<skill_name>",
    "code":         "<escaped code>",
    "tests":        "<escaped tests>",
    "requirements": [],
    "description":  "<one-sentence description>"
  }'
```

Or use the `task` agent to post the payload programmatically if the code/tests span multiple lines.

**Expected success response:**
```json
{
  "status": "success",
  "skill_name": "<skill_name>",
  "version": 1,
  "message": "Skill '<skill_name>' evolved successfully at version 1"
}
```

---

### Step 5 — Verify and Report

After a successful `/evolve` response:

1. Confirm the species file exists:
   ```bash
   ls -lh memory/species/<skill_name>.py
   ```

2. Confirm registry entry is live:
   ```bash
   python3 -c "import json; r=json.load(open('memory/dna/registry.json')); print(r['skills'].get('<skill_name>', 'NOT FOUND'))"
   ```

3. Report to the user:
   ```
   ✅ Evolutionary step complete
   Skill:   <skill_name>  v<version>
   Mission: <mission>
   Files:   memory/species/<skill_name>.py
   Registry: memory/dna/registry.json (updated)
   Git:     committed to memory vault
   ```

---

## Guard Rails

| Condition | Action |
|-----------|--------|
| MCP health gate fails | Abort — do not generate or submit anything |
| Skill name already in registry | Warn user, suggest a distinct name |
| `/evolve` returns error | Show error detail, offer to fix code/tests and retry (max 3 attempts) |
| Tests can't be written (no clear input/output) | Ask user to clarify the skill's contract before proceeding |
| `requirements` contains unknown packages | Warn; user must confirm before submitting |

---

## Quick-Reference: Full curl Workflow

```bash
# 1. Health check
curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $MCP_BEARER_TOKEN" \
  http://localhost:8000/tools/get_droplet_vitals/invoke

# 2. Evolve
curl -s -X POST http://localhost:8000/evolve \
  -H "Authorization: Bearer $MCP_BEARER_TOKEN" \
  -H "Content-Type: application/json" \
  -d @payload.json   # write the JSON payload to a temp file first

# 3. Verify
curl -s -H "Authorization: Bearer $MCP_BEARER_TOKEN" \
  http://localhost:8000/sse | head -c 2000
```
