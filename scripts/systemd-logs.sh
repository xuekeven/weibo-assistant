#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOG_DIR="${LOG_DIR:-$ROOT_DIR/logs}"
TARGET="${1:-all}"
LINES="${LOG_LINES:-${JOURNAL_LINES:-200}}"

case "$TARGET" in
  server)
    exec tail -n "$LINES" -F "$LOG_DIR/server.log"
    ;;
  monitor)
    exec tail -n "$LINES" -F "$LOG_DIR/monitor.log"
    ;;
  frontend)
    exec journalctl -u weibo-assistant-frontend.service -n "$LINES" -f
    ;;
  all)
    echo "following server and monitor logs; use '$0 frontend' for the frontend journal" >&2
    exec tail -n "$LINES" -F "$LOG_DIR/server.log" "$LOG_DIR/monitor.log"
    ;;
  *)
    echo "usage: $0 [server|monitor|frontend|all]" >&2
    exit 2
    ;;
esac
