# weibo-assistant

微博群聊监听与提醒服务。程序使用 Chrome 登录态，通过 Chrome DevTools Protocol 接收微博群聊消息，写入 PostgreSQL，并按照通知规则异步发送企业微信提醒。

## 目录

- [主要功能](#主要功能)
- [运行架构](#运行架构)
- [环境要求](#环境要求)
- [本地运行](#本地运行)
- [常用配置](#常用配置)
- [消息补偿与回溯](#消息补偿与回溯)
- [Linux 部署](#linux-部署)
- [API 与健康检查](#api-与健康检查)

## 主要功能

- 持久化微博登录态；登录失效时通过企业微信发送二维码，也可在网页异常提示中点击“登录”刷新并展示二维码，扫码后自动恢复监听。
- 实时解析群聊文本和图片消息，补全图片地址并统一入库；受保护图片由后端使用微博登录态代理，HEIC 自动转换为 JPEG。
- 企业微信图片通知会将微博缩略图地址转换为 `imageType=origin` 原图并完整转交中转服务；压缩、文件回退和大小降级统一由 `wework-send` 处理。
- 启动补偿、断线补漏和指定时间范围的历史消息回溯。
- 数据库唯一键去重，避免实时监听与历史同步产生重复消息和通知。
- 按群聊、发送人和消息类型配置通知规则；消息列表支持按正文关键词模糊搜索。
- 企业微信通知通过 outbox 异步投递，支持失败重试和状态记录。
- 消息列表、实时更新、通知管理、每日消息统计和 CSV/JSON/Markdown 导出。
- 健康检查、Prometheus 指标、结构化日志和 systemd 常驻部署。

## 运行架构

```text
微博群聊页 -> monitor -> PostgreSQL -> 企业微信中转服务
                           |
浏览器前端 <- server（静态页面、API、WebSocket、图片代理）
```

生产环境推荐运行两个长期进程，它们来自同一个 `weibo-assistant` 可执行文件：

| 进程 | 职责 | 默认端口 |
| --- | --- | --- |
| `server` | Vue 静态页面、API、浏览器 WebSocket、图片代理、健康检查和指标 | `6001` |
| `monitor` | Chrome 登录、消息监听、历史同步和通知投递 | 无 |

## 环境要求

- Go `1.26+`
- PostgreSQL
- Chrome 或 Chromium
- Node.js 和 pnpm，仅用于构建或开发前端
- Linux 生产环境：systemd `240+`、logrotate；显示微博 HEIC 图片还需 `libheif-examples`
- 企业微信消息中转服务，用于消息提醒和登录二维码通知

## 本地运行

1. 创建配置：

```bash
cp .env.example .env
```

至少填写 PostgreSQL 连接串：

```env
DATABASE_URL=postgresql://用户名:密码@127.0.0.1:5432/weibo_data
```

本机同时访问统一 server 和 Vite 时，还应把 `.env.example` 中的生产 Origin 改成本机地址：

```env
APP_ENV=development
FRONTEND_PORT=6001
CORS_ALLOWED_ORIGINS=http://localhost:6001,http://localhost:6002
```

需要企业微信通知时，再填写：

```env
WEWORK_RELAY_URL=http://中转服务地址:5001
WEWORK_RELAY_API_KEY=
WEWORK_RELAY_TOUSER=
```

2. 安装前端依赖、生成 server 必需的静态目录并执行数据库迁移：

```bash
pnpm --dir frontend install
pnpm --dir frontend run build
go run ./cmd/weibo-assistant migrate
```

3. 在三个终端分别启动 monitor、统一 server 和 Vite：

```bash
go run ./cmd/weibo-assistant monitor
```

```bash
go run ./cmd/weibo-assistant server
```

```bash
VITE_API_BASE=http://localhost:6001 pnpm --dir frontend run dev
```

本地开发页面地址为 `http://localhost:6002/weibo/`。当前 Vite 构建的 base 固定为 `/weibo/`；直连 `http://localhost:6001/` 虽能返回 HTML，但不会剥离静态资源的 `/weibo/` 前缀，不作为本地页面入口。生产页面应按后文部署文档通过 Nginx 将 `/weibo/` 代理到 server。

首次启动或登录失效时，如果已配置企业微信中转，monitor 会发送提示和二维码；也可以使用网页异常条中的“登录”按钮。使用微博 App 扫码后，monitor 会自动选择目标群并开始监听；登录态保存在 `WEIBO_PROFILE_DIR`，默认目录为 `.weibo-profile`。

配置企业微信中转后，monitor 启动和正常退出时会发送“微博群聊monitor已开启/已关闭”。Linux 文字消息不添加环境前缀，其他系统保留“【测试】”；图片消息不添加前缀。

## 常用配置

完整字段、推荐配置值和说明见 [.env.example](./.env.example)；代码内建默认值以 `internal/config/config.go` 为准。

| 配置 | 说明 |
| --- | --- |
| `APP_ENV` | 运行环境；`production` 会关闭开发消息接口 |
| `DATABASE_URL` | PostgreSQL 连接串，必填 |
| `BACKEND_BIND_ADDRESS` / `BACKEND_PORT` | API 监听地址和端口，默认 `0.0.0.0:6001` |
| `FRONTEND_DIST_DIR` | server 托管的 Vue 构建目录，默认 `frontend/dist` |
| `FRONTEND_PORT` | 独立 `frontend` 子命令的监听端口，也是未显式配置 CORS 时用于校验页面 Origin 的端口；代码默认 `6002`，统一生产部署设为 `6001` |
| `CORS_ALLOWED_ORIGINS` | 允许访问 API 和 WebSocket 的完整 Origin，多个值使用逗号分隔 |
| `WEIBO_PROFILE_DIR` | Chrome 登录态目录，默认 `.weibo-profile` |
| `HEADLESS` | 是否无头运行 Chrome，服务器建议设为 `true` |
| `CHROME_EXECUTABLE` | Chrome/Chromium 路径；留空时自动查找 |
| `CHROME_DEBUG_PORT` | CDP 本机端口，默认 `9222` |
| `CHROME_RENDERER_CPU_LIMIT_PERCENT` | 单个 renderer 的 CPU 看门狗阈值，默认 `50%`；连续超限时先重载、仍不恢复则重启 monitor |
| `CHROME_RENDERER_CPU_CONSECUTIVE_CHECKS` | 触发恢复动作所需的连续超限次数，默认 `3` 次 |
| `CHROME_RENDERER_CPU_CHECK_INTERVAL_SECONDS` | 独立 renderer CPU 采样间隔，默认 `60` 秒（1 分钟） |
| `TARGET_GROUP_ID` / `TARGET_GROUP_NAME` | monitor 只采集的目标群聊，同时用于环境变量默认通知规则；优先使用稳定 ID |
| `TARGET_SENDER_ID` / `TARGET_SENDER_NAME` | 默认通知规则匹配的发送人，优先使用稳定 ID |
| `WEWORK_RELAY_*` | 企业微信中转地址、鉴权密钥和接收人 |
| `HEALTH_CHECK_INTERVAL_SECONDS` | monitor 心跳间隔，默认 `60` 秒 |
| `MONITOR_STALE_AFTER_SECONDS` | 心跳超时阈值，默认 `180` 秒 |
| `LOGIN_QR_REFRESH_SECONDS` | 未登录时检查二维码的间隔，默认 `120` 秒 |
| `LOG_LEVEL` | `debug`、`info`、`warn` 或 `error` |

生产环境只有 server 对外监听 `BACKEND_BIND_ADDRESS:BACKEND_PORT`，前端静态文件与 API 共用该端口。通过反向代理部署时，`CORS_ALLOWED_ORIGINS` 必须包含页面的完整 Origin。API 当前没有应用层鉴权，不应直接暴露到公网。

## 消息补偿与回溯

实时 WebSocket 负责低延迟接收，微博历史接口负责补偿遗漏消息。三种任务都由 monitor 执行：

| 模式 | 触发方式 | 通知策略 |
| --- | --- | --- |
| `startup` | monitor 启动后自动追到数据库已有的最新消息 | 不通知 |
| `recovery` | 微博 WebSocket 断开并重连后自动补漏 | 新消息命中规则时通知 |
| `manual` | 前端“消息回溯”或消息列表向上加载到本地边界 | 不通知 |

历史消息和实时消息共用 `dedupe_key` 唯一约束，不会重复入库。手动回溯可指定起始时间和可选结束时间，任务进度通过 WebSocket 更新；任务显示“等待 monitor”时，应检查 monitor 服务和微博登录态。

## Linux 部署

所有命令都在项目根目录执行。

首次部署：

```bash
cp .env.example .env
# 编辑 .env

./scripts/build-linux.sh
./bin/weibo-assistant migrate

sudo APP_DIR="$HOME/repos/weibo-assistant" \
  RUN_USER="$(id -un)" \
  RUN_GROUP="$(id -gn)" \
  ENABLE_NOW=true \
  ./scripts/install-systemd.sh
```

`build-linux.sh` 会同时生成 Go 可执行文件 `bin/weibo-assistant` 和 Vue 静态文件 `frontend/dist`。生产环境由 systemd 运行构建结果，不使用 `go run` 或 Vite 开发服务器。

更新部署：

```bash
cd ~/repos/weibo-assistant

git pull
./scripts/build-linux.sh
./bin/weibo-assistant migrate
sudo systemctl restart weibo-assistant-server.service \
  weibo-assistant-monitor.service
```

查看状态和日志：

```bash
./scripts/systemd-status.sh
./scripts/systemd-logs.sh monitor
```

完整说明见 [systemd 长期运行与日志](./docs/systemd长期运行与日志.md)。

当前前端构建固定使用 `/weibo/` base。要正常加载静态资源和调用 API，请按部署文档配置 `/weibo/` 反向代理；不要把直连 `6001/` 当作生产页面地址。

## API 与健康检查

| 接口 | 用途 |
| --- | --- |
| `GET /livez` | API 进程存活检查，不访问外部依赖 |
| `GET /readyz` | 检查数据库和 monitor 心跳，异常时返回 `503` |
| `GET /healthz` | 与 `/readyz` 相同的兼容入口 |
| `GET /metrics` | Prometheus 指标 |
| `GET /api/media/image` | 通过已登录微博会话代理聊天图片，HEIC 自动转换为 JPEG |
| `GET /api/monitor/status` | monitor、登录态和最后消息状态 |
| `POST /api/monitor/login-qr` | 重载 monitor 登录页并返回最新登录二维码 PNG；仅在登录失效时可用 |
| `GET /api/groups` | 群聊列表 |
| `GET /api/messages` | 消息列表 |
| `GET/POST /api/groups/{id}/history-sync` | 查询或创建消息回溯任务 |
| `GET/POST /api/notification-rules` | 查询或创建通知规则 |
| `PATCH/DELETE /api/notification-rules/{id}` | 更新或归档通知规则 |
| `GET /api/notification-outbox` | 查询通知投递队列及失败状态 |
| `GET /api/notification-events` | 通知事件和投递状态 |

示例：

```bash
curl http://localhost:6001/readyz
curl 'http://localhost:6001/api/messages?limit=50&includeRaw=false'
```
