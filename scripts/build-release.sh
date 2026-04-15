#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${CERBERUS_BUILD_DIR:-$ROOT_DIR/bin}"
CGO_VALUE="${CGO_ENABLED:-0}"

mkdir -p "$OUT_DIR"

echo "Go: $(go version)"
echo "Output: $OUT_DIR"
echo "CGO_ENABLED=$CGO_VALUE"

(cd "$ROOT_DIR" && env CGO_ENABLED="$CGO_VALUE" go build -trimpath -o "$OUT_DIR/cerberus-master" ./Master)
(cd "$ROOT_DIR" && env CGO_ENABLED="$CGO_VALUE" go build -trimpath -o "$OUT_DIR/cerberus-worker" ./Worker)

echo "Built:"
echo "  $OUT_DIR/cerberus-master"
echo "  $OUT_DIR/cerberus-worker"
