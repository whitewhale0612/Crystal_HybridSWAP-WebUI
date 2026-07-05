#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${1:-$ROOT_DIR/module/bin/chswapctl}"

mkdir -p "$(dirname "$OUT")"

export GOOS="${CHSWAP_GOOS:-linux}"
export GOARCH="${CHSWAP_GOARCH:-arm64}"
export CGO_ENABLED="${CHSWAP_CGO_ENABLED:-0}"

cd "$ROOT_DIR/backend"
go build \
  -trimpath \
  -ldflags='-s -w -extldflags "-static"' \
  -o "$OUT" \
  ./chswapctl

chmod 0755 "$OUT"
file "$OUT"
