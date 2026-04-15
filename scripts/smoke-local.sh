#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SMOKE_ADDR="${CERBERUS_SMOKE_ADDR:-127.0.0.1:55051}"
SMOKE_TASKS="${CERBERUS_SMOKE_TASKS:-1000}"
SMOKE_TIMEOUT="${CERBERUS_SMOKE_TIMEOUT:-60s}"
SMOKE_KEEP="${CERBERUS_SMOKE_KEEP:-0}"
WORKER_ID="worker-smoke-1"

parse_seconds() {
  local value="$1"
  value="${value%s}"
  if [[ ! "$value" =~ ^[0-9]+$ ]] || [[ "$value" -eq 0 ]]; then
    echo "invalid CERBERUS_SMOKE_TIMEOUT: ${CERBERUS_SMOKE_TIMEOUT}" >&2
    exit 2
  fi
  printf '%s\n' "$value"
}

if [[ ! "$SMOKE_TASKS" =~ ^[0-9]+$ ]] || [[ "$SMOKE_TASKS" -eq 0 ]]; then
  echo "invalid CERBERUS_SMOKE_TASKS: ${SMOKE_TASKS}" >&2
  exit 2
fi

TIMEOUT_SECONDS="$(parse_seconds "$SMOKE_TIMEOUT")"
WORK_DIR="$(mktemp -d /tmp/cerberus-smoke.XXXXXX)"
CONFIG_HOME="$WORK_DIR/config"
DATA_DIR="$WORK_DIR/data"
BIN_DIR="$WORK_DIR/bin"
MASTER_LOG="$WORK_DIR/master.log"
WORKER_LOG="$WORK_DIR/worker.log"
HASH_FILE="$WORK_DIR/hashes.txt"
MASTER_PID=""
WORKER_PID=""

cleanup() {
  local status=$?
  if [[ -n "$WORKER_PID" ]]; then
    kill "$WORKER_PID" 2>/dev/null || true
  fi
  if [[ -n "$MASTER_PID" ]]; then
    kill "$MASTER_PID" 2>/dev/null || true
  fi
  if [[ -n "$WORKER_PID" ]]; then
    wait "$WORKER_PID" 2>/dev/null || true
  fi
  if [[ -n "$MASTER_PID" ]]; then
    wait "$MASTER_PID" 2>/dev/null || true
  fi
  if [[ "$status" -eq 0 && "$SMOKE_KEEP" != "1" ]]; then
    rm -rf "$WORK_DIR"
  else
    echo "smoke artifacts kept at $WORK_DIR" >&2
    echo "master log: $MASTER_LOG" >&2
    echo "worker log: $WORKER_LOG" >&2
  fi
}
trap cleanup EXIT

run_master_cli() {
  env XDG_CONFIG_HOME="$CONFIG_HOME" CERBERUS_DATA_DIR="$DATA_DIR" "$BIN_DIR/cerberus-master" --addr "$SMOKE_ADDR" "$@"
}

summary_value() {
  local summary="$1"
  local key="$2"
  printf '%s\n' "$summary" | grep -o "${key}:[0-9]*" | head -n1 | cut -d: -f2
}

summary_total() {
  local summary="$1"
  printf '%s\n' "$summary" | sed -n 's/^Total: \([0-9][0-9]*\).*/\1/p'
}

mkdir -p "$BIN_DIR" "$DATA_DIR/wordlists"
printf "admin\ncerberus123\npassword\n" > "$DATA_DIR/wordlists/test.txt"

: > "$HASH_FILE"
for ((i = 0; i < SMOKE_TASKS; i++)); do
  case $((i % 3)) in
    0) echo "21232f297a57a5a743894a0e4a801fc3" >> "$HASH_FILE" ;;
    1) echo "f6be3f2408481885304a362deafa168a" >> "$HASH_FILE" ;;
    2) echo "5f4dcc3b5aa765d61d8327deb882cf99" >> "$HASH_FILE" ;;
  esac
done

echo "building smoke binaries..."
(cd "$ROOT_DIR" && go build -o "$BIN_DIR/cerberus-master" ./Master)
(cd "$ROOT_DIR" && go build -o "$BIN_DIR/cerberus-worker" ./Worker)

echo "starting master on $SMOKE_ADDR..."
env XDG_CONFIG_HOME="$CONFIG_HOME" CERBERUS_DATA_DIR="$DATA_DIR" "$BIN_DIR/cerberus-master" serve --listen "$SMOKE_ADDR" >"$MASTER_LOG" 2>&1 </dev/null &
MASTER_PID=$!

deadline=$((SECONDS + TIMEOUT_SECONDS))
until run_master_cli task list >/dev/null 2>&1; do
  if ! kill -0 "$MASTER_PID" 2>/dev/null; then
    echo "master exited before becoming ready" >&2
    tail -n 80 "$MASTER_LOG" >&2 || true
    exit 1
  fi
  if ((SECONDS >= deadline)); then
    echo "timed out waiting for master" >&2
    tail -n 80 "$MASTER_LOG" >&2 || true
    exit 1
  fi
  sleep 1
done

echo "issuing worker token..."
TOKEN_OUTPUT="$(env XDG_CONFIG_HOME="$CONFIG_HOME" CERBERUS_DATA_DIR="$DATA_DIR" "$BIN_DIR/cerberus-master" token worker issue --worker-id "$WORKER_ID")"
WORKER_TOKEN="$(printf '%s\n' "$TOKEN_OUTPUT" | sed -n 's/^CERBERUS_WORKER_TOKEN=//p')"
if [[ -z "$WORKER_TOKEN" ]]; then
  echo "failed to parse worker token" >&2
  printf '%s\n' "$TOKEN_OUTPUT" >&2
  exit 1
fi

echo "starting worker..."
env XDG_CONFIG_HOME="$CONFIG_HOME" \
  CERBERUS_DATA_DIR="$DATA_DIR" \
  CERBERUS_MASTER_ADDR="$SMOKE_ADDR" \
  CERBERUS_WORKER_ID="$WORKER_ID" \
  CERBERUS_WORKER_TOKEN="$WORKER_TOKEN" \
  "$BIN_DIR/cerberus-worker" >"$WORKER_LOG" 2>&1 </dev/null &
WORKER_PID=$!

deadline=$((SECONDS + TIMEOUT_SECONDS))
until run_master_cli worker list | grep -q "$WORKER_ID"; do
  if ! kill -0 "$WORKER_PID" 2>/dev/null; then
    echo "worker exited before registering" >&2
    tail -n 80 "$WORKER_LOG" >&2 || true
    exit 1
  fi
  if ((SECONDS >= deadline)); then
    echo "timed out waiting for worker registration" >&2
    tail -n 80 "$MASTER_LOG" >&2 || true
    tail -n 80 "$WORKER_LOG" >&2 || true
    exit 1
  fi
  sleep 1
done

echo "submitting $SMOKE_TASKS tasks..."
run_master_cli task add-batch --file "$HASH_FILE" --mode md5 --wordlist wordlists/test.txt --chunk 2

deadline=$((SECONDS + TIMEOUT_SECONDS))
while true; do
  SUMMARY="$(run_master_cli task list)"
  TOTAL="$(summary_total "$SUMMARY")"
  COMPLETED="$(summary_value "$SUMMARY" completed)"
  FAILED="$(summary_value "$SUMMARY" failed)"
  FOUND="$(summary_value "$SUMMARY" found)"

  TOTAL="${TOTAL:-0}"
  COMPLETED="${COMPLETED:-0}"
  FAILED="${FAILED:-0}"
  FOUND="${FOUND:-0}"

  printf 'poll total=%s completed=%s found=%s failed=%s\n' "$TOTAL" "$COMPLETED" "$FOUND" "$FAILED"

  if [[ "$FAILED" -ne 0 ]]; then
    echo "smoke failed: task failures detected" >&2
    echo "$SUMMARY" >&2
    exit 1
  fi
  if [[ "$TOTAL" -eq "$SMOKE_TASKS" && "$COMPLETED" -eq "$SMOKE_TASKS" ]]; then
    break
  fi
  if ((SECONDS >= deadline)); then
    echo "timed out waiting for tasks to complete" >&2
    echo "$SUMMARY" >&2
    tail -n 80 "$MASTER_LOG" >&2 || true
    tail -n 80 "$WORKER_LOG" >&2 || true
    exit 1
  fi
  sleep 1
done

if [[ "$FOUND" -ne "$SMOKE_TASKS" ]]; then
  echo "smoke failed: expected found=$SMOKE_TASKS, got found=$FOUND" >&2
  run_master_cli task list --table --limit 20 >&2 || true
  exit 1
fi

echo "PASS local smoke: tasks=$SMOKE_TASKS completed=$COMPLETED found=$FOUND failed=$FAILED"
