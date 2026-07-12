#!/usr/bin/env bash
set -euo pipefail

TARGET="${1:-all}"

show_status() {
  local unit="$1"

  if systemctl cat "$unit" >/dev/null 2>&1; then
    systemctl status "$unit" --no-pager
  else
    echo "[skip] unit not installed: $unit"
  fi
}

case "$TARGET" in
  server)
    show_status weibo-assistant-server.service
    ;;
  monitor)
    show_status weibo-assistant-monitor.service
    ;;
  frontend)
    show_status weibo-assistant-frontend.service
    ;;
  all)
    show_status weibo-assistant-server.service
    show_status weibo-assistant-monitor.service
    show_status weibo-assistant-frontend.service
    ;;
  *)
    echo "usage: $0 [server|monitor|frontend|all]" >&2
    exit 2
    ;;
esac
