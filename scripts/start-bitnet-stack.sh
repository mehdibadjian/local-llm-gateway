#!/bin/bash
# Starts BitNet server + CAW gateway. Run from any directory.
# Requires: Homebrew (for auto-install of PostgreSQL + Redis if missing)
set -e

GGUF=/Users/pappi/models/BitNet-b1.58-2B-4T/ggml-model-i2_s.gguf
BITNET_DIR=/Users/pappi/dev/BitNet
GATEWAY_DIR=/Users/pappi/Desktop/dev/local-llm-gateway

# ── PostgreSQL ──────────────────────────────────────────────────────────────
ensure_postgres() {
  local PG_VERSION=16
  local PG_FORMULA="postgresql@${PG_VERSION}"

  if ! brew list "$PG_FORMULA" &>/dev/null; then
    echo "==> Installing ${PG_FORMULA} via Homebrew..."
    brew install "$PG_FORMULA"
  fi

  if ! brew services list | grep -q "^${PG_FORMULA}.*started"; then
    echo "==> Starting ${PG_FORMULA} via Homebrew services..."
    brew services start "$PG_FORMULA"
  fi

  local PG_BIN
  PG_BIN="$(brew --prefix "${PG_FORMULA}")/bin"

  echo "==> Waiting for PostgreSQL to be ready..."
  for i in $(seq 1 20); do
    if "${PG_BIN}/pg_isready" -q 2>/dev/null; then
      echo "    PostgreSQL ready ✓"
      break
    fi
    sleep 1
    if [ "$i" -eq 20 ]; then
      echo "    ERROR: PostgreSQL did not become ready in time."
      exit 1
    fi
  done

  # Create role + database if they don't exist
  if ! "${PG_BIN}/psql" -U "$(whoami)" -tAc "SELECT 1 FROM pg_roles WHERE rolname='caw'" postgres 2>/dev/null | grep -q 1; then
    echo "==> Creating PostgreSQL role 'caw'..."
    "${PG_BIN}/createuser" -s caw 2>/dev/null || true
    "${PG_BIN}/psql" -U "$(whoami)" -c "ALTER ROLE caw WITH PASSWORD 'caw';" postgres 2>/dev/null || true
  fi

  if ! "${PG_BIN}/psql" -U caw -tAc "SELECT 1" caw &>/dev/null; then
    echo "==> Creating database 'caw'..."
    "${PG_BIN}/createdb" -U caw caw 2>/dev/null || true
  fi
}

ensure_postgres

# ── Redis ───────────────────────────────────────────────────────────────────
ensure_redis() {
  if ! brew list redis &>/dev/null; then
    echo "==> Installing redis via Homebrew..."
    brew install redis
  fi

  if ! brew services list | grep -q "^redis.*started"; then
    echo "==> Starting redis via Homebrew services..."
    brew services start redis
  fi

  echo "==> Waiting for Redis to be ready..."
  for i in $(seq 1 10); do
    if redis-cli ping &>/dev/null; then
      echo "    Redis ready ✓"
      break
    fi
    sleep 1
    if [ "$i" -eq 10 ]; then
      echo "    ERROR: Redis did not become ready in time."
      exit 1
    fi
  done
}

ensure_redis

echo "==> Starting BitNet server on :8082 (background)..."
LLAMA_ARG_MODEL=$GGUF $BITNET_DIR/build/bin/llama-server --port 8082 \
  > /tmp/bitnet-server.log 2>&1 &
BITNET_PID=$!
echo "    PID=$BITNET_PID  logs: /tmp/bitnet-server.log"

echo "==> Waiting for BitNet to be ready..."
for i in $(seq 1 30); do
  if curl -sf http://127.0.0.1:8082/health > /dev/null 2>&1; then
    echo "    BitNet ready ✓"
    break
  fi
  sleep 1
  if [ $i -eq 30 ]; then
    echo "    Timed out waiting for BitNet. Check /tmp/bitnet-server.log"
    kill $BITNET_PID 2>/dev/null
    exit 1
  fi
done

echo "==> Killing any existing process on :8080..."
lsof -ti :8080 | xargs kill -9 2>/dev/null || true

echo "==> Starting CAW gateway on :8080 (foreground)..."
cd $GATEWAY_DIR
INFERENCE_BACKEND=bitnet \
  BITNET_BASE_URL=http://localhost:8082 \
  CAW_API_KEY=dev-key \
  go run ./cmd/wrapper

# Gateway exited — clean up BitNet server
kill $BITNET_PID 2>/dev/null
