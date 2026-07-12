#!/usr/bin/env bash
set -euo pipefail

TARGET="${1:-all}"
LINES="${JOURNAL_LINES:-200}"

case "$TARGET" in
  server)
    exec journalctl -u weibo-assistant-server.service -n "$LINES" -f
    ;;
  monitor)
    exec journalctl -u weibo-assistant-monitor.service -n "$LINES" -f
    ;;
  frontend)
    exec journalctl -u weibo-assistant-frontend.service -n "$LINES" -f
    ;;
  all)
    exec journalctl \
      -u weibo-assistant-server.service \
      -u weibo-assistant-monitor.service \
      -u weibo-assistant-frontend.service \
      -n "$LINES" -f
    ;;
  *)
    echo "usage: $0 [server|monitor|frontend|all]" >&2
    exit 2
    ;;
esac
