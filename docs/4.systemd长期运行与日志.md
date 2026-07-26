# systemd 长期运行与日志查看

本文描述当前三个 Go 进程的生产部署方式。快速入口见 [项目 README](../README.md)，架构说明见 [1.技术方案.md](./1.技术方案.md)。

## 目录

- [目标](#目标)
- [服务拆分](#服务拆分)
- [推荐目录](#推荐目录)
- [构建与部署](#构建与部署)
- [部署前检查](#部署前检查)
- [环境变量](#环境变量)
- [首次微博扫码登录](#首次微博扫码登录)
- [安装 systemd 服务](#安装-systemd-服务)
- [启动、停止、重启](#启动停止重启)
- [日志查看](#日志查看)
- [健康检查](#健康检查)
- [部署后验收](#部署后验收)
- [前端部署](#前端部署)
- [常见问题](#常见问题)

## 目标

在 Linux 服务器上让 `weibo-assistant` 长期运行：

- 后端 API / WebSocket 常驻，默认监听 `6001`。
- 监控进程常驻，复用微博登录态打开网页版群聊并监听 WebSocket 消息。
- 可选前端服务常驻，默认监听 `6002`。
- server 和 monitor 日志写入项目的 `logs/` 目录并自动轮转；frontend 日志进入 `journald`。

## 服务拆分

建议拆成两个核心 systemd 服务：

| 服务 | 命令 | 作用 |
| --- | --- | --- |
| `weibo-assistant-server.service` | `bin/weibo-assistant server` | 提供 HTTP API、前端 WebSocket、数据库查询、实时推送 |
| `weibo-assistant-monitor.service` | `bin/weibo-assistant monitor` | 启动无头 Chrome，处理二维码登录，监听微博群聊消息，写入 PostgreSQL，命中规则后通知企业微信 |

前端是可选服务：

| 服务 | 命令 | 作用 |
| --- | --- | --- |
| `weibo-assistant-frontend.service` | `bin/weibo-assistant frontend` | 用 Go 静态文件服务暴露 Vue 构建产物，不依赖生产 Node 进程 |

## 推荐目录

本文统一假设项目安装在当前 Linux 用户的：

```text
~/repos/weibo-assistant/
  bin/weibo-assistant
  .env
  .weibo-profile/
  logs/
    server.log
    monitor.log
  migrations/
  frontend/
    dist/
```

首次部署时进入项目目录：

```bash
cd ~/repos/weibo-assistant
```

后续构建、迁移和辅助脚本命令，除非特别说明，都在这个目录中执行。

> systemd 的 `WorkingDirectory`、`EnvironmentFile` 和 `ExecStart` 不会像 shell 一样展开 `~`。安装服务时必须传入已经展开的绝对路径，例如 `/home/你的用户名/repos/weibo-assistant`。本文使用 `APP_DIR="$HOME/repos/weibo-assistant"`，shell 会在调用安装脚本前展开 `$HOME`。

## 构建与部署

生产服务器需要 systemd `240+`（项目日志使用 `StandardOutput=append:`）和 `logrotate`。Ubuntu/Debian 可先执行：

```bash
sudo apt update
sudo apt install logrotate
systemctl --version | head -n 1
```

在服务器项目目录中构建：

```bash
./scripts/build-linux.sh
```

这个脚本会生成：

```text
bin/weibo-assistant
frontend/dist/
```

如果你是在本地构建后上传到服务器，至少需要同步这些内容：

```text
bin/weibo-assistant
migrations/
frontend/dist/
.env
deploy/
scripts/
```

如果在其他机器构建二进制，必须确保目标系统和架构一致；不要把 macOS 构建出的 `bin/weibo-assistant` 直接上传到 Linux。

`.env` 含有数据库密码、企业微信中转密钥、微博监听配置，不要提交到 Git，也不要放到公开目录。

首次部署或包含新迁移时，启动服务前需执行：

```bash
./bin/weibo-assistant migrate
```

`server` 和 `monitor` 启动时也会执行同一个版本化迁移器；显式执行便于在重启前提前发现权限、SQL 或校验和问题。

## 部署前检查

安装 systemd 服务前，在项目根目录依次确认：

```bash
test -x ./bin/weibo-assistant
test -f ./.env
test -d ./frontend/dist
./bin/weibo-assistant migrate
```

`.env` 中的 `FRONTEND_DIST_DIR`、`WEIBO_PROFILE_DIR` 应使用目标服务器上的绝对路径。确认 Chrome 和 PostgreSQL 可用：

```bash
set -a
. ./.env
set +a
"$CHROME_EXECUTABLE" --version
psql "$DATABASE_URL" -c 'select 1'
```

## 环境变量

服务器上的 `~/repos/weibo-assistant/.env` 推荐至少包含：

```env
APP_ENV=production
DATABASE_URL=postgresql://weibo_user:换成强密码@127.0.0.1:5432/weibo_data
BACKEND_PORT=6001
BACKEND_BIND_ADDRESS=0.0.0.0
FRONTEND_PORT=6002
FRONTEND_BIND_ADDRESS=0.0.0.0
FRONTEND_DIST_DIR=/home/你的用户名/repos/weibo-assistant/frontend/dist
CORS_ALLOWED_ORIGINS=

WEIBO_CHAT_URL=https://api.weibo.com/chat#/chat
WEIBO_PROFILE_DIR=/home/你的用户名/repos/weibo-assistant/.weibo-profile
HEADLESS=true
CHROME_EXECUTABLE=/usr/bin/google-chrome
CHROME_NO_SANDBOX=false
CHROME_DEBUG_PORT=9222

TARGET_GROUP_ID=目标群聊ID
TARGET_GROUP_NAME=
TARGET_SENDER_ID=目标发送人ID
TARGET_SENDER_NAME=

WEWORK_RELAY_URL=http://aliserver.tailed5977.ts.net:5001
WEWORK_RELAY_API_KEY=换成中转服务密钥
WEWORK_RELAY_TOUSER=
NOTIFICATION_POLL_INTERVAL_MS=1000
NOTIFICATION_MAX_ATTEMPTS=8

HEALTH_CHECK_INTERVAL_SECONDS=60
MONITOR_STALE_AFTER_SECONDS=180
PAGE_RELOAD_INTERVAL_MINUTES=0
LOGIN_QR_REFRESH_SECONDS=120
IMAGE_CACHE_TTL_SECONDS=86400
IMAGE_CACHE_MAX_ITEMS=1000
LOG_LEVEL=info
```

权限建议：

```bash
chmod 600 ~/repos/weibo-assistant/.env
```

`FRONTEND_DIST_DIR` 和 `WEIBO_PROFILE_DIR` 建议填写绝对路径。可先执行 `echo "$HOME"` 查看自己的 home 目录，再替换示例中的 `/home/你的用户名`。

`BACKEND_BIND_ADDRESS=0.0.0.0` 和 `FRONTEND_BIND_ADDRESS=0.0.0.0` 支持从 Tailscale 内网访问。前端通过访问页面时的主机名连接同一主机的 `6001` 端口，因此可直接打开 `http://<Tailscale-IP>:6002` 或对应 MagicDNS 地址。若要只监听 Tailscale 网卡，可将两个值改为本机的 Tailscale `100.x` 地址；同时建议使用主机防火墙禁止公网接口访问 `6001/6002`。

`CORS_ALLOWED_ORIGINS` 留空时，会自动接受从 `FRONTEND_PORT` 发起的 localhost、私有 IP、Tailscale `100.64.0.0/10` 和 `*.ts.net` Origin。需要使用其他域名或反向代理时，应填写完整 Origin，例如 `https://weibo.example.ts.net`；多个值用逗号分隔。

## 首次微博扫码登录

项目没有独立的 `login` 命令，也不需要服务器具备图形桌面。首次启动 monitor 或登录态失效时，monitor 会：

1. 识别微博登录页，并将 monitor 状态更新为 `login_required`。
2. 在 DOM 中找到二维码图片，通过 CDP 读取其原始 PNG 资源，避免页面弹层遮挡；读取失败时才降级为关闭弹层后截图。
3. 使用 `WEWORK_RELAY_URL` 和 `WEWORK_RELAY_API_KEY` 先发送一条文字提示，再发送一条二维码 PNG 图片。
4. 保持无头 Chrome 运行，等待使用微博 App 扫描二维码。
5. 登录成功后通过企业微信发送文字确认，将 Cookie、localStorage 等状态保存在 `WEIBO_PROFILE_DIR`，并自动恢复群聊监听。

登录态目录包含微博账号凭证，建议权限收紧：

```bash
chmod 700 ~/repos/weibo-assistant/.weibo-profile
```

如果没有收到二维码，检查 `WEWORK_RELAY_API_KEY`、`WEWORK_RELAY_TOUSER` 和 monitor 日志。登录提醒发送失败时，monitor 保持运行并每分钟重试尚未成功的消息。未登录期间每隔 `LOGIN_QR_REFRESH_SECONDS` 重新读取二维码：二维码未变化时不重复发送，过期刷新为新二维码后会重新发送图片；文字提示在本次未登录期间只发送一次。

monitor 进入监控主循环时发送“微博群聊monitor已开启”，正常停止或从可捕获的运行错误退出时发送“微博群聊monitor已关闭”。关闭通知使用独立的 20 秒超时，不受主进程取消信号影响；`SIGKILL`、主机断电和操作系统崩溃无法发送退出通知。

## 安装 systemd 服务

项目已提供模板：

```text
deploy/systemd/weibo-assistant-server.service
deploy/systemd/weibo-assistant-monitor.service
deploy/systemd/weibo-assistant-frontend.service
```

推荐用脚本安装。先确认当前用户和项目绝对路径：

```bash
cd ~/repos/weibo-assistant
echo "APP_DIR=$HOME/repos/weibo-assistant"
echo "RUN_USER=$(id -un)"
echo "RUN_GROUP=$(id -gn)"
```

只安装后端和监控：

```bash
sudo APP_DIR="$HOME/repos/weibo-assistant" \
  RUN_USER="$(id -un)" \
  RUN_GROUP="$(id -gn)" \
  ./scripts/install-systemd.sh
```

安装并立即启用、启动：

```bash
sudo APP_DIR="$HOME/repos/weibo-assistant" \
  RUN_USER="$(id -un)" \
  RUN_GROUP="$(id -gn)" \
  ENABLE_NOW=true \
  ./scripts/install-systemd.sh
```

连前端服务一起安装：

```bash
sudo APP_DIR="$HOME/repos/weibo-assistant" \
  RUN_USER="$(id -un)" \
  RUN_GROUP="$(id -gn)" \
  INSTALL_FRONTEND=true \
  ./scripts/install-systemd.sh
```

脚本会把 `APP_DIR`、`RUN_USER`、`RUN_GROUP` 和该用户真实的 home 目录写入 unit 文件。服务最终以当前 Linux 用户运行，因此能直接访问该用户的 `.weibo-profile`，不需要额外创建 `weibo` 系统用户。特殊账号无法通过系统用户数据库取得 home 时，可额外传入 `RUN_HOME=/实际/home/目录`。

安装脚本会在写入 unit 前检查：systemd 版本、绝对 `APP_DIR`、运行用户和组、二进制、`.env`、迁移文件、`logrotate`；安装 frontend 时还会检查 `frontend/dist/index.html`。写入后还会验证 systemd unit 和 logrotate 配置，任一检查失败都会停止，不会启用不完整的服务。

脚本会把模板渲染到：

```text
/etc/systemd/system/weibo-assistant-server.service
/etc/systemd/system/weibo-assistant-monitor.service
/etc/systemd/system/weibo-assistant-frontend.service
/etc/logrotate.d/weibo-assistant
```

并执行：

```bash
systemctl daemon-reload
```

## 启动、停止、重启

启动并设置开机自启：

```bash
sudo systemctl enable --now weibo-assistant-server.service
sudo systemctl enable --now weibo-assistant-monitor.service
```

如果使用前端服务：

```bash
sudo systemctl enable --now weibo-assistant-frontend.service
```

查看状态：

```bash
./scripts/systemd-status.sh server
./scripts/systemd-status.sh monitor
./scripts/systemd-status.sh frontend
./scripts/systemd-status.sh all
```

也可以直接使用：

```bash
systemctl status weibo-assistant-server.service
systemctl status weibo-assistant-monitor.service
systemctl status weibo-assistant-frontend.service
```

重启：

```bash
sudo systemctl restart weibo-assistant-server.service
sudo systemctl restart weibo-assistant-monitor.service
sudo systemctl restart weibo-assistant-frontend.service
```

停止：

```bash
sudo systemctl stop weibo-assistant-monitor.service
sudo systemctl stop weibo-assistant-server.service
```

更新程序后通常执行：

```bash
cd ~/repos/weibo-assistant
git pull
./scripts/build-linux.sh
./bin/weibo-assistant migrate
sudo systemctl restart weibo-assistant-server.service
sudo systemctl restart weibo-assistant-monitor.service
sudo systemctl restart weibo-assistant-frontend.service
```

如果没有安装前端服务，省略 frontend 的重启命令。执行迁移后再重启，可以在旧进程仍然运行时提前发现数据库升级错误。

## 日志查看

server 和 monitor 的标准输出、错误输出分别写入：

```text
logs/server.log
logs/monitor.log
```

安装脚本会自动创建目录与文件，并安装 `/etc/logrotate.d/weibo-assistant`：日志每天轮转，保留 14 份并压缩旧日志。frontend 仍写入 `journald`。

安装前请确认服务器已有 `logrotate`（Ubuntu/Debian 可执行 `sudo apt install logrotate`）；安装脚本检测不到时会停止，避免日志在无轮转保护的情况下持续增长。

推荐使用脚本：

```bash
./scripts/systemd-logs.sh server
./scripts/systemd-logs.sh monitor
./scripts/systemd-logs.sh frontend
./scripts/systemd-logs.sh all
```

默认显示最近 `200` 行并持续跟随。`all` 同时跟随 server 和 monitor；frontend 需单独查看。想调整行数：

```bash
LOG_LINES=500 ./scripts/systemd-logs.sh monitor
```

也可以直接使用：

```bash
tail -n 200 -f logs/server.log
tail -n 200 -f logs/monitor.log
journalctl -u weibo-assistant-frontend.service -n 200 -f
```

按时间或日志等级过滤可使用 `rg`：

```bash
rg '^time=.* level=(WARN|ERROR)' logs/monitor.log
```

## 健康检查

后端 API：

```bash
curl http://127.0.0.1:6001/livez
curl http://127.0.0.1:6001/readyz
curl http://127.0.0.1:6001/metrics
curl http://127.0.0.1:6001/api/monitor/status
curl http://127.0.0.1:6001/api/groups
curl "http://127.0.0.1:6001/api/messages?limit=10"
```

- `/livez` 不访问外部依赖，只检查 API 进程是否存活。
- `/readyz` 检查数据库和 monitor freshness；数据库不可用或 monitor 心跳过期时返回 HTTP `503`，响应同时包含最后消息时间和通知积压。
- `/metrics` 返回 Prometheus 文本，可直接配置 Prometheus 抓取。
- monitor 心跳超过 `MONITOR_STALE_AFTER_SECONDS` 后会标记为 stale。

生产 `LOG_LEVEL=info` 不输出群聊正文、群名和昵称，只记录 ID、类型、正文长度和短哈希。网络资源日志只保留主机、路径和少量用于定位媒体的参数；未知查询参数、用户信息、fragment、`data:` 内容和 `blob:` 地址会被脱敏。仅在短期排查时使用 `LOG_LEVEL=debug`，并限制项目 `logs/` 目录访问权限。

WebSocket 路由存在性：

```bash
curl -i http://127.0.0.1:6001/api/ws
```

正常情况下，普通 HTTP 请求会返回 `400 websocket upgrade required`，说明路由存在；浏览器前端会用 WebSocket 协议连接。

监控日志里重点看这些关键词：

```text
monitor heartbeat
network monitor enabled
network message parsed
network message persisted
target message wework notification sent
```

后端日志里重点看：

```text
api server listening
realtime listener starting
websocket client connected
websocket broadcast
```

生产 `info` 日志不会输出群聊正文、群名和昵称。若临时设置 `LOG_LEVEL=debug`，完成排查后应立即恢复；调试内容会随项目日志按 14 天轮转策略到期清理，如需更短保留期可调整 `/etc/logrotate.d/weibo-assistant`。

## 部署后验收

启动服务后建议逐项执行：

```bash
systemctl is-active weibo-assistant-server.service
systemctl is-active weibo-assistant-monitor.service
# 仅在安装了项目 frontend service 时执行：
systemctl is-active weibo-assistant-frontend.service

curl --fail http://127.0.0.1:6001/livez
curl --fail http://127.0.0.1:6001/readyz
curl --fail http://127.0.0.1:6001/api/monitor/status
curl --fail http://127.0.0.1:6002/

ls -l logs/server.log logs/monitor.log
sudo logrotate --debug /etc/logrotate.d/weibo-assistant
```

`/readyz` 返回成功代表数据库可用且 monitor 心跳仍新鲜；还应确认 `/api/monitor/status` 中 `fresh=true`，且 `logs/monitor.log` 出现 `network monitor enabled` 和持续更新的 `monitor heartbeat`。

## 前端部署

简单方式是使用项目自带的前端 systemd 服务：

```bash
cd ~/repos/weibo-assistant
./scripts/build-linux.sh
sudo APP_DIR="$HOME/repos/weibo-assistant" \
  RUN_USER="$(id -un)" \
  RUN_GROUP="$(id -gn)" \
  INSTALL_FRONTEND=true \
  ./scripts/install-systemd.sh
sudo systemctl enable --now weibo-assistant-frontend.service
```

访问：

```text
http://服务器IP:6002
```

如果后端不是同一台机器或不是 `6001`，创建：

```text
~/repos/weibo-assistant/frontend/.env.production
```

内容示例：

```env
VITE_API_BASE=http://服务器IP:6001
```

更正式的生产部署可以用 Nginx 直接托管 `frontend/dist/`，再把 `/api` 和 `/api/ws` 反向代理到 `127.0.0.1:6001`。

## 常见问题

### 服务启动后立刻退出

查看日志：

```bash
./scripts/systemd-logs.sh server
./scripts/systemd-logs.sh monitor
```

常见原因：

- `.env` 不存在或权限不对。
- `DATABASE_URL` 连接失败。
- `WEWORK_RELAY_API_KEY` 未配置时通知 worker 不会启动，登录二维码也无法发送；monitor 仍会等待登录并持续报告错误。
- `CHROME_EXECUTABLE` 路径不对。

### monitor 启动了但没有消息

先看是否打开了微博聊天页：

```bash
./scripts/systemd-logs.sh monitor
```

关注：

```text
target group selection
network monitor enabled
monitor heartbeat
```

如果一直没有选中目标群聊，检查：

- `TARGET_GROUP_ID` 是否为群聊 ID。
- `/api/monitor/status` 是否为 `login_required`；如果是，检查企业微信中的二维码消息并扫码。
- `WEIBO_PROFILE_DIR` 是否指向正确目录。

### 前端没有实时刷新

检查后端服务：

```bash
./scripts/systemd-logs.sh server
```

重点看：

```text
websocket client connected
realtime listener starting
websocket broadcast
```

同时确认前端访问的后端地址正确：

```bash
curl http://127.0.0.1:6001/api/monitor/status
```

### 图片不显示

先确认后端返回的附件里是否已有图片 URL：

```bash
curl "http://127.0.0.1:6001/api/messages?limit=5"
```

如果附件只有 `fid` 没有 `url`，说明还没有从微博 payload 或 DOM/network 响应里解析出可展示地址。继续观察 monitor 日志里的图片解析相关输出。

### Chrome 沙箱报错

普通 Linux 服务器优先保持：

```env
CHROME_NO_SANDBOX=false
```

如果日志明确提示 sandbox 相关错误，再临时改为：

```env
CHROME_NO_SANDBOX=true
```

修改 `.env` 后重启 monitor：

```bash
sudo systemctl restart weibo-assistant-monitor.service
```
