#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$ROOT_DIR"
mkdir -p bin

echo "[build] Go backend"
go build -o bin/weibo-assistant ./cmd/weibo-assistant

if [ -d frontend ]; then
  echo "[build] Vue frontend"
  if [ -f frontend/package-lock.json ]; then
    npm --prefix frontend ci
  else
    npm --prefix frontend install
  fi
  npm --prefix frontend run build
fi

echo "[ok] built: $ROOT_DIR/bin/weibo-assistant"
echo "[ok] frontend dist: $ROOT_DIR/frontend/dist"
