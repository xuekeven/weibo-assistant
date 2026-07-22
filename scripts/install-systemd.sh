#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEMPLATE_DIR="$ROOT_DIR/deploy/systemd"
UNIT_DIR="${UNIT_DIR:-/etc/systemd/system}"
LOGROTATE_TEMPLATE="$ROOT_DIR/deploy/logrotate/weibo-assistant"
LOGROTATE_DIR="${LOGROTATE_DIR:-/etc/logrotate.d}"

APP_DIR="${APP_DIR:-/opt/weibo-assistant}"
RUN_USER="${RUN_USER:-weibo}"
RUN_GROUP="${RUN_GROUP:-$RUN_USER}"
RUN_HOME="${RUN_HOME:-}"
INSTALL_FRONTEND="${INSTALL_FRONTEND:-false}"
ENABLE_NOW="${ENABLE_NOW:-false}"

for value_name in INSTALL_FRONTEND ENABLE_NOW; do
  value="${!value_name}"
  if [ "$value" != "true" ] && [ "$value" != "false" ]; then
    echo "error: $value_name must be true or false, got: $value" >&2
    exit 1
  fi
done

if [ "$(id -u)" -ne 0 ]; then
  echo "error: please run as root, for example: sudo APP_DIR=$APP_DIR RUN_USER=$RUN_USER $0" >&2
  exit 1
fi

if ! command -v systemctl >/dev/null 2>&1 || ! command -v getent >/dev/null 2>&1; then
  echo "error: systemctl and getent are required on the deployment host" >&2
  exit 1
fi
if ! command -v logrotate >/dev/null 2>&1; then
  echo "error: logrotate is required to prevent project logs from growing without limit" >&2
  exit 1
fi

read -r _ systemd_version _ < <(systemctl --version)
if [[ ! "$systemd_version" =~ ^[0-9]+$ ]] || [ "$systemd_version" -lt 240 ]; then
  echo "error: systemd 240 or newer is required for append-mode project log files" >&2
  exit 1
fi

case "$APP_DIR" in
  /*) ;;
  *)
    echo "error: APP_DIR must be an absolute path: $APP_DIR" >&2
    exit 1
    ;;
esac
if [[ "$APP_DIR" =~ [[:space:]] ]] || [[ "$APP_DIR" == *"|"* ]] || [[ "$APP_DIR" == *"&"* ]] || [[ "$APP_DIR" == *"\\"* ]] || [[ "$APP_DIR" == *"%"* ]]; then
  echo "error: APP_DIR cannot contain whitespace or shell/systemd special characters" >&2
  exit 1
fi

if ! id "$RUN_USER" >/dev/null 2>&1; then
  echo "error: RUN_USER does not exist: $RUN_USER" >&2
  exit 1
fi

if ! getent group "$RUN_GROUP" >/dev/null 2>&1; then
  echo "error: RUN_GROUP does not exist: $RUN_GROUP" >&2
  exit 1
fi

if [ -z "$RUN_HOME" ]; then
  passwd_entry="$(getent passwd "$RUN_USER")"
  IFS=: read -r _ _ _ _ _ RUN_HOME _ <<< "$passwd_entry"
fi
case "$RUN_HOME" in
  /*) ;;
  *)
    echo "error: could not determine an absolute home directory for $RUN_USER; set RUN_HOME explicitly" >&2
    exit 1
    ;;
esac
if [[ "$RUN_HOME" =~ [[:space:]] ]] || [[ "$RUN_HOME" == *"|"* ]] || [[ "$RUN_HOME" == *"&"* ]] || [[ "$RUN_HOME" == *"\\"* ]] || [[ "$RUN_HOME" == *"%"* ]]; then
  echo "error: RUN_HOME cannot contain whitespace or shell/systemd special characters" >&2
  exit 1
fi

if [ ! -x "$APP_DIR/bin/weibo-assistant" ]; then
  echo "error: executable not found: $APP_DIR/bin/weibo-assistant (run ./scripts/build-linux.sh first)" >&2
  exit 1
fi

if [ ! -f "$APP_DIR/.env" ]; then
  echo "error: environment file not found: $APP_DIR/.env" >&2
  exit 1
fi

if [ ! -d "$APP_DIR/migrations" ] || ! compgen -G "$APP_DIR/migrations/*.sql" >/dev/null; then
  echo "error: migration files not found under $APP_DIR/migrations" >&2
  exit 1
fi

if [ "$INSTALL_FRONTEND" = "true" ] && [ ! -f "$APP_DIR/frontend/dist/index.html" ]; then
  echo "error: frontend build not found: $APP_DIR/frontend/dist/index.html" >&2
  exit 1
fi

install -d -m 0750 -o "$RUN_USER" -g "$RUN_GROUP" "$APP_DIR/logs"
touch "$APP_DIR/logs/server.log" "$APP_DIR/logs/monitor.log"
chown "$RUN_USER:$RUN_GROUP" "$APP_DIR/logs/server.log" "$APP_DIR/logs/monitor.log"
chmod 0640 "$APP_DIR/logs/server.log" "$APP_DIR/logs/monitor.log"
echo "[ok] prepared $APP_DIR/logs"

render_unit() {
  local template="$1"
  local target="$2"
  local tmp

  tmp="$(mktemp)"
  sed \
    -e "s|__APP_DIR__|$APP_DIR|g" \
    -e "s|__RUN_USER__|$RUN_USER|g" \
    -e "s|__RUN_GROUP__|$RUN_GROUP|g" \
    -e "s|__RUN_HOME__|$RUN_HOME|g" \
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

render_unit "$LOGROTATE_TEMPLATE" "$LOGROTATE_DIR/weibo-assistant"

if command -v systemd-analyze >/dev/null 2>&1; then
  unit_files=(
    "$UNIT_DIR/weibo-assistant-server.service"
    "$UNIT_DIR/weibo-assistant-monitor.service"
  )
  if [ "$INSTALL_FRONTEND" = "true" ]; then
    unit_files+=("$UNIT_DIR/weibo-assistant-frontend.service")
  fi
  systemd-analyze verify "${unit_files[@]}"
  echo "[ok] systemd units verified"
fi

if ! logrotate --debug "$LOGROTATE_DIR/weibo-assistant" >/dev/null 2>&1; then
  echo "error: installed logrotate configuration is invalid" >&2
  exit 1
fi
echo "[ok] logrotate configuration verified"

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
