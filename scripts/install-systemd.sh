#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEMPLATE_DIR="$ROOT_DIR/deploy/systemd"
UNIT_DIR="${UNIT_DIR:-/etc/systemd/system}"

APP_DIR="${APP_DIR:-/opt/weibo-assistant}"
RUN_USER="${RUN_USER:-weibo}"
RUN_GROUP="${RUN_GROUP:-$RUN_USER}"
INSTALL_FRONTEND="${INSTALL_FRONTEND:-false}"
ENABLE_NOW="${ENABLE_NOW:-false}"

if [ "$(id -u)" -ne 0 ]; then
  echo "error: please run as root, for example: sudo APP_DIR=$APP_DIR RUN_USER=$RUN_USER $0" >&2
  exit 1
fi

render_unit() {
  local template="$1"
  local target="$2"
  local tmp

  tmp="$(mktemp)"
  sed \
    -e "s|__APP_DIR__|$APP_DIR|g" \
    -e "s|__RUN_USER__|$RUN_USER|g" \
    -e "s|__RUN_GROUP__|$RUN_GROUP|g" \
    "$template" > "$tmp"

  install -m 0644 "$tmp" "$target"
  rm -f "$tmp"
  echo "[ok] installed $target"
}

render_unit "$TEMPLATE_DIR/weibo-assistant-server.service" "$UNIT_DIR/weibo-assistant-server.service"
render_unit "$TEMPLATE_DIR/weibo-assistant-monitor.service" "$UNIT_DIR/weibo-assistant-monitor.service"

if [ "$INSTALL_FRONTEND" = "true" ]; then
  render_unit "$TEMPLATE_DIR/weibo-assistant-frontend.service" "$UNIT_DIR/weibo-assistant-frontend.service"
fi

systemctl daemon-reload
echo "[ok] systemd daemon reloaded"

if [ "$ENABLE_NOW" = "true" ]; then
  systemctl enable --now weibo-assistant-server.service
  systemctl enable --now weibo-assistant-monitor.service
  if [ "$INSTALL_FRONTEND" = "true" ]; then
    systemctl enable --now weibo-assistant-frontend.service
  fi
  echo "[ok] services enabled and started"
else
  echo "[next] start manually:"
  echo "  systemctl enable --now weibo-assistant-server.service"
  echo "  systemctl enable --now weibo-assistant-monitor.service"
  if [ "$INSTALL_FRONTEND" = "true" ]; then
    echo "  systemctl enable --now weibo-assistant-frontend.service"
  fi
fi
