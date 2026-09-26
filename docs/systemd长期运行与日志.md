# systemd 长期运行与日志查看

本文描述当前统一前后端的生产部署方式。快速入口和运行架构见 [项目 README](../README.md)，代码分层见 [后端代码导读](./后端代码导读.md)。

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
- [统一前后端与 Nginx 子路径部署](#统一前后端与-nginx-子路径部署)
- [常见问题](#常见问题)

## 目标

在 Linux 服务器上让 `weibo-assistant` 长期运行：

- 后端 API / WebSocket 常驻，默认监听 `6001`。
- 监控进程常驻，复用微博登录态打开网页版群聊并监听 WebSocket 消息。
- Vue 构建产物由 server 同端口托管，不再需要独立前端服务。
- 前端构建固定使用 `/weibo/` base，由 Nginx 按本文配置剥离该前缀后转发给 server。
- server 和 monitor 日志写入项目的 `logs/` 目录并自动轮转。

## 服务拆分

建议拆成两个核心 systemd 服务：

| 服务 | 命令 | 作用 |
| --- | --- | --- |
| `weibo-assistant-server.service` | `bin/weibo-assistant server` | 提供 Vue 页面、HTTP API、WebSocket、图片代理、数据库查询和实时推送 |
| `weibo-assistant-monitor.service` | `bin/weibo-assistant monitor` | 启动无头 Chrome，处理二维码登录，监听微博群聊消息，写入 PostgreSQL，命中规则后通知企业微信 |


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
sudo apt install logrotate libheif-examples
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
FRONTEND_PORT=6001
FRONTEND_DIST_DIR=/home/你的用户名/repos/weibo-assistant/frontend/dist
CORS_ALLOWED_ORIGINS=https://weibo.example.ts.net

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
WEWORK_RELAY_APP=message
WEWORK_RELAY_TOUSER=
NOTIFICATION_POLL_INTERVAL_MS=1000
NOTIFICATION_MAX_ATTEMPTS=8

HEALTH_CHECK_INTERVAL_SECONDS=60
MONITOR_STALE_AFTER_SECONDS=180
PAGE_RELOAD_INTERVAL_MINUTES=360
CPU_POWER_ALERT_WATTS=25
CPU_TEMPERATURE_ALERT_CELSIUS=75
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

`FRONTEND_PORT` 是独立 `frontend` 子命令的端口，同时参与未显式配置 `CORS_ALLOWED_ORIGINS` 时的 Origin 校验。统一部署虽然不启动独立 frontend 服务，仍应将它设为页面实际使用的端口；页面与 server 同端口时设为 `6001`。代码默认值为 `6002`，适合本地 Vite 开发。

`BACKEND_BIND_ADDRESS=0.0.0.0` 允许从 Tailscale 内网访问统一的 6001 服务。若只允许本机 Nginx 转发，可改为 `127.0.0.1`；若直接从 Tailscale IP 访问，可改为对应 `100.x` 地址并限制防火墙端口。

统一生产部署应显式填写 `CORS_ALLOWED_ORIGINS`，值为浏览器实际访问页面的完整 Origin，例如 `https://homeserver.example.ts.net`。路径 `/weibo/` 不属于 Origin，不要写入该变量；多个 Origin 用逗号分隔。否则 WebSocket 握手会返回 `403 websocket origin is not allowed`。

## 首次微博扫码登录

项目没有独立的 `login` 命令，也不需要服务器具备图形桌面。首次启动 monitor 或登录态失效时，monitor 会：

1. 识别微博登录页，并将 monitor 状态更新为 `login_required`。
2. 在 DOM 中找到二维码图片，通过 CDP 读取其原始 PNG 资源，避免页面弹层遮挡；读取失败时才降级为关闭弹层后截图。
3. 使用 `WEWORK_RELAY_URL` 和 `WEWORK_RELAY_API_KEY` 先发送一条文字提示，再发送一条二维码 PNG 图片；也可以在网页异常条点击“登录”，由 `POST /api/monitor/login-qr` 重载 monitor 登录页并弹窗展示新二维码。
4. 保持无头 Chrome 运行，等待使用微博 App 扫描二维码。
5. 登录成功后通过企业微信发送文字确认，将 Cookie、localStorage 等状态保存在 `WEIBO_PROFILE_DIR`，并自动恢复群聊监听。

登录态目录包含微博账号凭证，建议权限收紧：

```bash
chmod 700 ~/repos/weibo-assistant/.weibo-profile
```

如果没有收到企业微信二维码，检查 `WEWORK_RELAY_API_KEY`、`WEWORK_RELAY_TOUSER` 和 monitor 日志，也可直接使用网页“登录”按钮。登录提醒发送失败时，monitor 保持运行并每分钟重试尚未成功的消息。未登录期间每隔 `LOGIN_QR_REFRESH_SECONDS` 重新读取二维码：二维码未变化时不重复发送，过期刷新为新二维码后会重新发送图片；文字提示在本次未登录期间只发送一次。

普通微博图片通知会把 `/msget_thumbnail` 转换为带 `imageType=origin` 的 `/msget` 原图地址，
使用登录态下载完整字节后直接提交给 `wework-send`。本项目不按文件大小压缩或拒绝图片；
企微图片、文件及文字降级策略由中转服务统一决定。

配置企业微信中转后，monitor 进入监控主循环时发送“微博群聊monitor已开启”，正常停止或从可捕获的运行错误退出时发送“微博群聊monitor已关闭”。关闭通知使用独立的 20 秒超时，不受主进程取消信号影响；`SIGKILL`、主机断电和操作系统崩溃无法发送退出通知。

## 安装 systemd 服务

项目已提供模板：

```text
deploy/systemd/weibo-assistant-server.service
deploy/systemd/weibo-assistant-monitor.service
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


脚本会把 `APP_DIR`、`RUN_USER`、`RUN_GROUP` 和该用户真实的 home 目录写入 unit 文件。服务最终以当前 Linux 用户运行，因此能直接访问该用户的 `.weibo-profile`，不需要额外创建 `weibo` 系统用户。特殊账号无法通过系统用户数据库取得 home 时，可额外传入 `RUN_HOME=/实际/home/目录`。

安装脚本会在写入 unit 前检查：systemd 版本、绝对 `APP_DIR`、运行用户和组、二进制、`.env`、迁移文件和 `logrotate`。写入后还会验证 systemd unit 和 logrotate 配置。统一 server 启动时还要求 `FRONTEND_DIST_DIR` 存在，因此不要跳过前文的 `test -d ./frontend/dist`；仅当使用兼容选项 `INSTALL_FRONTEND=true` 时，安装脚本本身才额外检查 `frontend/dist/index.html`。

脚本会把模板渲染到：

```text
/etc/systemd/system/weibo-assistant-server.service
/etc/systemd/system/weibo-assistant-monitor.service
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


查看状态：

```bash
./scripts/systemd-status.sh server
./scripts/systemd-status.sh monitor
./scripts/systemd-status.sh all
```

也可以直接使用：

```bash
systemctl status weibo-assistant-server.service
systemctl status weibo-assistant-monitor.service
```

重启：

```bash
sudo systemctl restart weibo-assistant-server.service
sudo systemctl restart weibo-assistant-monitor.service
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
```

server 会同时重新加载后端与 frontend/dist，无需重启独立前端服务。执行迁移后再重启，可以在旧进程仍然运行时提前发现数据库升级错误。

## 日志查看

server 和 monitor 的标准输出、错误输出分别写入：

```text
logs/server.log
logs/monitor.log
```

安装脚本会自动创建目录与文件，并安装 `/etc/logrotate.d/weibo-assistant`：日志每天轮转，保留 14 份并压缩旧日志。

安装前请确认服务器已有 `logrotate` 和 `libheif-examples`（Ubuntu/Debian 可执行 `sudo apt install logrotate libheif-examples`）。安装脚本会强制检查 `logrotate`；目前不会检查 `heif-convert`，缺少它时服务仍可启动，但 HEIC 图片转换会失败。

推荐使用脚本：

```bash
./scripts/systemd-logs.sh server
./scripts/systemd-logs.sh monitor
./scripts/systemd-logs.sh all
```

默认显示最近 `200` 行并持续跟随。`all` 同时跟随 server 和 monitor。想调整行数：

```bash
LOG_LINES=500 ./scripts/systemd-logs.sh monitor
```

也可以直接使用：

```bash
tail -n 200 -f logs/server.log
tail -n 200 -f logs/monitor.log
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
微博监听状态更新
network monitor enabled
已收到微博消息
新消息已保存
target message wework notification sent
```

后端日志里重点看：

```text
api server listening
实时消息服务已启动，正在等待新消息
网页已连接实时消息
实时消息已推送到网页
```

生产 `info` 日志不会输出群聊正文、群名和昵称。若临时设置 `LOG_LEVEL=debug`，完成排查后应立即恢复；调试内容会随项目日志按 14 天轮转策略到期清理，如需更短保留期可调整 `/etc/logrotate.d/weibo-assistant`。

## 部署后验收

启动服务后建议逐项执行：

```bash
systemctl is-active weibo-assistant-server.service
systemctl is-active weibo-assistant-monitor.service

curl --fail http://127.0.0.1:6001/livez
curl --fail http://127.0.0.1:6001/readyz
curl --fail http://127.0.0.1:6001/api/monitor/status
curl --fail http://127.0.0.1:6001/

ls -l logs/server.log logs/monitor.log
sudo logrotate --debug /etc/logrotate.d/weibo-assistant
```

最后一个命令只确认 server 能读取 `frontend/dist/index.html`。该 HTML 的资源路径以 `/weibo/` 开头，完整页面应在配置 Nginx 后通过外部 `/weibo/` 地址验收，而不是直接打开后端根路径。

`/readyz` 返回成功代表数据库可用且 monitor 心跳仍新鲜；还应确认 `/api/monitor/status` 中 `fresh=true`、`lastHeartbeatAt` 持续更新，且 `logs/monitor.log` 出现 `network monitor enabled`。正常监听心跳在 `LOG_LEVEL=debug` 时记录为“微博实时监听正常”，生产默认 `info` 不会逐次打印心跳日志。

## 统一前后端与 Nginx 子路径部署

生产环境由 `weibo-assistant-server.service` 在 `6001` 同时提供 Vue 页面、API 和 WebSocket，不需要启动任何独立前端服务。构建并重启：

```bash
cd ~/repos/weibo-assistant
./scripts/build-linux.sh
sudo systemctl restart weibo-assistant-server.service
```

使用 `/weibo/` 子路径时，前端构建的 base 为 `/weibo/`。Nginx 只需一个代理块：

```nginx
location = /weibo {
    return 301 /weibo/;
}

location /weibo/ {
    proxy_pass http://127.0.0.1:6001/;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_read_timeout 86400;
}
```

对应 `.env`：

```env
BACKEND_PORT=6001
FRONTEND_PORT=6001
FRONTEND_DIST_DIR=/home/你的用户名/repos/weibo-assistant/frontend/dist
CORS_ALLOWED_ORIGINS=https://homeserver.example.ts.net
```

这里的 Origin 不包含 `/weibo/` 路径。修改后执行 `sudo nginx -t`、重新加载 Nginx 并重启 server。

最后通过浏览器访问 `https://homeserver.example.ts.net/weibo/`，并在开发者工具的 Network 面板确认 `/weibo/assets/*`、`/weibo/api/*` 和 WebSocket 都成功。直连 `http://127.0.0.1:6001/` 只适合检查 index 是否存在，不是完整页面入口。

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
target group select status
network monitor enabled
微博监听状态更新
```

如果一直没有选中目标群聊，检查：

- `TARGET_GROUP_ID` 是否为群聊 ID。
- `/api/monitor/status` 是否为 `login_required`；如果是，使用网页异常条的“登录”按钮，或检查企业微信中的二维码消息并扫码。
- `WEIBO_PROFILE_DIR` 是否指向正确目录。

### 前端没有实时刷新

检查后端服务：

```bash
./scripts/systemd-logs.sh server
```

重点看：

```text
网页已连接实时消息
实时消息服务已启动，正在等待新消息
实时消息已推送到网页
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

如果附件只有 `fid` 没有 `url`，说明还没有从微博 payload 或 DOM/network 响应里解析出可展示地址，继续观察 monitor 日志。若已有 `url` 但仍不显示，检查 server 日志和 `GET /api/media/image`：该接口会复用已登录 Chromium 的微博 Cookie 代取图片，并把 HEIC 转为 JPEG；因此 monitor 必须在线且服务器需安装 `libheif-examples`。

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
