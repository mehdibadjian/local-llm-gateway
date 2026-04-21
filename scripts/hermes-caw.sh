#!/usr/bin/env bash
# hermes-caw.sh — Start CAW gateway then launch Hermes pointed at it.
#
# Usage:
#   ./scripts/hermes-caw.sh            # start both CAW + Hermes
#   ./scripts/hermes-caw.sh --caw-only # start only CAW (no Hermes)
#   ./scripts/hermes-caw.sh --hermes-only # attach Hermes to a running CAW
#
# Environment overrides (all optional):
#   CAW_API_KEY          API key for CAW auth (default: dev-key)
#   PORT                 CAW listen port       (default: 8080)
#   OLLAMA_BASE_URL      Ollama endpoint       (default: http://localhost:11434)
#   REDIS_ADDR           Redis address         (default: localhost:6379)
#   DATABASE_URL         Postgres DSN          (default: localhost:5432/caw)
#   QDRANT_BASE_URL      Qdrant endpoint       (default: http://localhost:6333)
#   EMBED_BASE_URL       Embed service         (default: http://localhost:5001)
set -euo pipefail

# ── Resolve repo root ────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"

# ── Defaults ─────────────────────────────────────────────────────────────────
export CAW_API_KEY="${CAW_API_KEY:-dev-key}"
export PORT="${PORT:-8080}"
export OLLAMA_BASE_URL="${OLLAMA_BASE_URL:-http://localhost:11434}"
export REDIS_ADDR="${REDIS_ADDR:-localhost:6379}"
export DATABASE_URL="${DATABASE_URL:-postgres://caw:caw@localhost:5432/caw?sslmode=disable}"
export QDRANT_BASE_URL="${QDRANT_BASE_URL:-http://localhost:6333}"
export EMBED_BASE_URL="${EMBED_BASE_URL:-http://localhost:5001}"

CAW_URL="http://localhost:${PORT}"
MODE="both"

# ── Parse flags ───────────────────────────────────────────────────────────────
for arg in "$@"; do
  case "$arg" in
    --caw-only)    MODE="caw" ;;
    --hermes-only) MODE="hermes" ;;
    --help|-h)
      sed -n '2,18p' "$0" | sed 's/^# *//'
      exit 0
      ;;
  esac
done

# ── Colour helpers ────────────────────────────────────────────────────────────
GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; NC='\033[0m'
info()  { echo -e "${GREEN}[caw]${NC} $*"; }
warn()  { echo -e "${YELLOW}[warn]${NC} $*"; }
error() { echo -e "${RED}[error]${NC} $*" >&2; }

# ── Dependency checks ─────────────────────────────────────────────────────────
require() {
  if ! command -v "$1" &>/dev/null; then
    error "Required command not found: $1"
    exit 1
  fi
}

if [[ "$MODE" != "hermes" ]]; then
  require go
fi
if [[ "$MODE" != "caw" ]]; then
  require hermes
fi

# ── Wait for CAW healthz ──────────────────────────────────────────────────────
wait_for_caw() {
  local retries=30
  info "Waiting for CAW at ${CAW_URL}/healthz ..."
  for i in $(seq 1 $retries); do
    if curl -sf "${CAW_URL}/healthz" &>/dev/null; then
      info "CAW is ready."
      return 0
    fi
    sleep 1
  done
  error "CAW did not become ready after ${retries}s."
  return 1
}

# ── Cleanup on exit ───────────────────────────────────────────────────────────
CAW_PID=""
cleanup() {
  if [[ -n "$CAW_PID" ]]; then
    info "Stopping CAW (pid $CAW_PID)..."
    kill "$CAW_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM

# ── Start CAW ─────────────────────────────────────────────────────────────────
start_caw() {
  info "Starting CAW gateway on port ${PORT}..."
  info "  API key : ${CAW_API_KEY}"
  info "  Ollama  : ${OLLAMA_BASE_URL}"
  info "  Redis   : ${REDIS_ADDR}"

  cd "$REPO_ROOT"
  go run ./cmd/wrapper &
  CAW_PID=$!
  info "CAW pid: ${CAW_PID}"
}

# ── Start Hermes ──────────────────────────────────────────────────────────────
start_hermes() {
  info "Launching Hermes → CAW at ${CAW_URL}/v1"
  info "  MCP server: ${CAW_URL}/mcp"
  # Export so Hermes config.yaml can interpolate ${CAW_API_KEY}
  export HERMES_INFERENCE_PROVIDER="custom"
  export OPENAI_BASE_URL="${CAW_URL}/v1"
  export OPENAI_API_KEY="${CAW_API_KEY}"
  exec hermes
}

# ── Main ──────────────────────────────────────────────────────────────────────
case "$MODE" in
  caw)
    start_caw
    wait $CAW_PID
    ;;
  hermes)
    if ! curl -sf "${CAW_URL}/healthz" &>/dev/null; then
      warn "CAW does not appear to be running at ${CAW_URL}. Starting Hermes anyway..."
    fi
    start_hermes
    ;;
  both)
    start_caw
    wait_for_caw
    start_hermes  # exec — replaces shell, cleanup trap fires on exit
    ;;
esac
