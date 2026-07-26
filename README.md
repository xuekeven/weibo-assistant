# weibo-assistant

微博群聊监听与提醒服务。它复用 Chrome 登录态，通过 Chrome DevTools Protocol 监听微博网页版群聊消息，将消息保存到 PostgreSQL，并按数据库通知规则异步发送企业微信提醒。

## 目录

- [当前能力](#当前能力)
  - [消息补偿与消息回溯](#消息补偿与消息回溯)
- [运行架构](#运行架构)
- [环境要求](#环境要求)
- [快速开始](#快速开始)
- [常用配置](#常用配置)
- [构建与运行](#构建与运行)
  - [先执行生产构建](#1-先执行生产构建)
  - [再运行构建后的子命令](#2-再运行构建后的子命令)
  - [Vue 为什么构建时用 Node.js，运行时用 Go](#3-vue-为什么构建时用-nodejs运行时用-go)
  - [本地开发可以使用 go run](#4-本地开发可以使用-go-run)
  - [Linux 生产运行顺序](#5-linux-生产运行顺序)
- [API 与健康检查](#api-与健康检查)
- [可观测性与日志](#可观测性与日志)
- [测试](#测试)
- [安全注意事项](#安全注意事项)
- [文档](#文档)
- [已知限制](#已知限制)

## 当前能力

- Chrome/Chromium 持久化登录态和无头运行。
- monitor 自动识别登录失效，优先从 DOM 读取微博二维码原图并通过企业微信发送；扫码后自动恢复监听。
- 微博 WebSocket 消息解析、图片附件识别与 URL 补全。
- monitor 启动补偿、WebSocket 断线补漏和按时间范围回溯历史消息。
- PostgreSQL 消息、群组、用户、附件和监控状态存储。
- 消息事务写入、数据库唯一键去重和实时 `LISTEN/NOTIFY`。
- 数据库通知规则匹配，环境变量自动同步默认规则。
- 企业微信 outbox 异步投递、指数退避重试和最终失败记录。
- Go HTTP API、浏览器 WebSocket 实时推送和 Vue 消息页面。
- 管理中心按本地自然日统计成员消息，并按单天或区间导出 CSV、JSON、Markdown；群系统事件不计入统计和导出。
- `/livez`、`/readyz`、monitor freshness 与 Prometheus `/metrics`。
- systemd 常驻部署和 Go 静态前端服务。
- 带版本、校验和与事务锁的数据库迁移。

### 消息补偿与消息回溯

实时 WebSocket 负责低延迟接收，微博 `query_messages.json` 历史接口负责补偿和消息回溯。三种同步统一由 monitor 执行，并复用当前 Chrome 登录态：

| 模式 | 触发方式 | 作用 | 企业微信通知 |
| --- | --- | --- | --- |
| 启动补偿 `startup` | monitor 启动并选中目标群后自动创建 | 从最新页向前同步，直到追到启动时数据库已有的最新消息 ID | 不发送 |
| 断线补漏 `recovery` | 检测到 CDP/微博 WebSocket 断开并重连后自动创建 | 从最新页向前扫描，补上断线窗口内遗漏的消息 | 新补入且命中规则时发送 |
| 消息回溯 `manual` | 前端“消息回溯”或向上加载到本地边界时创建 | 按指定起始时间或时间区间归档更早消息 | 不发送 |

三种模式都会在扫描到明确边界后停止。启动补偿优先追到任务创建时记录的 `stop_mid`，也就是启动前数据库已有的最新微博消息 ID；没有 `stop_mid` 时，退回到“扫描到任务创建前已经存在于 PostgreSQL 的微博消息 ID 后停止”。每条消息仍经过 `dedupe_key` 唯一约束，因此实时消息与历史同步并发到达时不会重复入库，也不会重复通知。

前端操作：

1. 选择群聊后点击右上角“消息回溯”。
2. “回溯到”填写希望向前同步到的时间；可选“区间结束”只保存指定区间内的消息。
3. 点击“开始回溯”，任务由 PostgreSQL 队列交给 monitor 执行。
4. 前端通过 WebSocket 接收任务进度和新增消息，不需要保持弹窗打开。

消息列表向上翻到本地最早记录后，也会自动创建一个单页历史同步任务。任务只有在 monitor 正常运行且已经取得微博聊天页 CDP target 时才会执行；页面显示“等待 monitor”时，应检查 monitor 服务和微博登录态。

## 运行架构

```text
Chrome 微博群聊页
  -> CDP Network/WebSocket
  -> 消息解析
  -> PostgreSQL 事务写入
       -> notification_outbox
       -> pg_notify

monitor worker -> 企业微信中转服务
login page    -> CDP 读取二维码原图 -> 企业微信文字 + PNG 登录提醒
server API     -> PostgreSQL -> 浏览器 WebSocket
frontend       -> server API / WebSocket
```

生产环境通常运行三个进程：

- `server`：API、健康检查、指标和前端 WebSocket，默认端口 `6001`。
- `monitor`：Chrome、微博消息监听、入库和通知 worker。
- `frontend`：托管 `frontend/dist`，默认端口 `6002`。

## 环境要求

- Go `1.26+`
- PostgreSQL
- Chromium
- Node.js/npm，仅用于构建或开发 Vue 前端
- Linux 生产部署：systemd `240+` 和 logrotate
- 企业微信消息中转服务；自动发送登录二维码时必需

## 快速开始

### 1. 配置

```bash
cp .env.example .env
```

至少设置数据库连接：

```env
DATABASE_URL=postgresql://用户名:密码@127.0.0.1:5432/weibo_data
```

自动发送登录二维码和企业微信消息需要配置：

```env
WEWORK_RELAY_URL=http://你的中转服务:5001
WEWORK_RELAY_API_KEY=
WEWORK_RELAY_TOUSER=
```

如需用环境变量创建默认消息通知规则，再配置 `TARGET_GROUP_ID` 和 `TARGET_SENDER_ID`。微博原始 ID 比群名和昵称稳定，建议优先配置 ID。

### 2. 执行迁移

```bash
go run ./cmd/weibo-assistant migrate
```

迁移记录保存在 `schema_migrations`。同一版本只执行一次；已执行 SQL 的 SHA-256 发生变化时会拒绝继续迁移。

### 3. 启动 monitor 并登录微博

```bash
go run ./cmd/weibo-assistant monitor
```

项目不再提供独立的 `login` 命令。monitor 会使用 `WEIBO_PROFILE_DIR` 启动 Chrome；首次启动或登录失效时，它会：

1. 将状态更新为 `login_required`。
2. 在 DOM 中定位二维码 `<img>`，通过 Chrome DevTools Protocol 读取其原始图片资源；读取失败时才降级为关闭提示弹层后截图。
3. 调用企业微信中转接口，先发送文字登录提示，再发送二维码 PNG 图片。
4. 保持 Chrome 和 monitor 运行，等待用户用微博 App 扫码。
5. 检测到登录成功后，通过企业微信发送登录成功提示，自动选择目标群并恢复消息监听。

这个流程支持纯命令行 Linux 服务器和 `HEADLESS=true`，不需要 VNC、远程桌面或物理显示器。登录态保存在 `WEIBO_PROFILE_DIR`，默认 `.weibo-profile`；不要把它提交到 Git。

monitor 进入监控主循环和退出时，还会分别通过企业微信发送“微博群聊monitor已开启”和“微博群聊monitor已关闭”。关闭通知覆盖正常停止和可捕获的异常退出；`SIGKILL`、断电或操作系统崩溃无法执行退出通知。

### 4. 本地开发

保持上一步的 monitor 运行，再分别启动 API 和 Vue 开发服务器：

```bash
# 终端 1
go run ./cmd/weibo-assistant server

# 终端 2
npm --prefix frontend install
npm --prefix frontend run dev
```

前端默认访问当前主机的 `6001` 端口，可通过 `VITE_API_BASE` 指定其他地址。Linux 生产部署不要使用 Vite 开发服务器，按下文“构建与运行”的顺序操作。

## 常用配置

完整配置及注释见 [.env.example](./.env.example)。

| 配置 | 默认值 | 说明 |
| --- | --- | --- |
| `APP_ENV` | `development` | `production` 时关闭开发消息接口 |
| `DATABASE_URL` | 无 | PostgreSQL 连接串，必填 |
| `BACKEND_PORT` | `6001` | API 端口 |
| `BACKEND_BIND_ADDRESS` | `0.0.0.0` | API 监听 IP；可填写本机 Tailscale `100.x` 地址收窄范围 |
| `FRONTEND_PORT` | `6002` | 前端静态服务和 CORS 端口 |
| `FRONTEND_BIND_ADDRESS` | `0.0.0.0` | 前端静态服务监听 IP |
| `FRONTEND_DIST_DIR` | `frontend/dist` | Vue 构建产物目录 |
| `CORS_ALLOWED_ORIGINS` | 空 | 额外允许的完整 Origin，逗号分隔；留空时支持 localhost、私有 IP、Tailscale IP 和 `*.ts.net` |
| `WEIBO_PROFILE_DIR` | `.weibo-profile` | Chrome 登录态目录 |
| `HEADLESS` | `true` | monitor 是否无头运行 |
| `CHROME_DEBUG_PORT` | `9222` | 仅本机使用的 CDP 端口 |
| `TARGET_GROUP_ID` | 空 | 默认规则的微博群 ID |
| `TARGET_SENDER_ID` | 空 | 默认规则的微博发送人 ID |
| `WEWORK_RELAY_URL` | 项目默认中转地址 | 企业微信中转服务地址；登录二维码通知必需 |
| `WEWORK_RELAY_API_KEY` | 空 | 企业微信中转鉴权密钥；登录二维码通知必需 |
| `WEWORK_RELAY_TOUSER` | 空 | 接收登录二维码和消息通知的企业微信 userid；空表示由中转服务使用默认接收人 |
| `NOTIFICATION_MAX_ATTEMPTS` | `8` | 通知最大尝试次数 |
| `HEALTH_CHECK_INTERVAL_SECONDS` | `60` | monitor 每隔多少秒检查一次 Chrome、微博页面和目标群状态，并把本次心跳写入数据库 |
| `MONITOR_STALE_AFTER_SECONDS` | `180` | server 多久没有看到新的 monitor 心跳后，将 monitor 判定为 stale（可能卡死或已退出） |
| `LOGIN_QR_REFRESH_SECONDS` | `120` | 未登录时重新检查二维码的间隔；只在二维码内容变化后重新发送图片 |
| `IMAGE_CACHE_TTL_SECONDS` | `86400` | 图片 URL 缓存 TTL |
| `IMAGE_CACHE_MAX_ITEMS` | `1000` | 图片 URL 缓存上限 |
| `LOG_LEVEL` | `info` | `debug/info/warn/error` |

心跳和新消息是两个独立概念。只要 monitor 正常运行，它即使没有采集到新消息，也会每隔 `HEALTH_CHECK_INTERVAL_SECONDS` 更新一次心跳。因此群聊连续数小时没有新消息不会触发 stale；只有心跳本身超过 `MONITOR_STALE_AFTER_SECONDS` 没有更新，才会被认为异常。`lastMessageAt` 仅用于展示最后一次收到消息的时间，不参与健康判断。

前后端默认监听 `0.0.0.0`，可以直接通过 `http://<Tailscale-IP>:6002` 或 MagicDNS 主机名访问；前端会使用当前主机的 `6001` 端口调用 API。若希望进程只接受 Tailscale 流量，可把两个 bind 配置都设置为本机的 Tailscale `100.x` 地址，并配合主机防火墙限制 `6001/6002`。当前 API 不带应用层鉴权，不应暴露到公网。

## 构建与运行

这里需要区分两个概念：

- **构建**：把 Go 和 Vue 源代码转换成生产环境可以运行的文件，但不启动服务。
- **运行**：启动已经构建好的程序，并持续占用当前终端或由 systemd 管理。

### 1. 先执行生产构建

在项目根目录执行：

```bash
./scripts/build-linux.sh
```

它会完成整个项目的生产构建：

1. 内部执行 `go build -o bin/weibo-assistant ./cmd/weibo-assistant`，把 Go 后端编译成 `bin/weibo-assistant`。
2. 在 `frontend` 目录安装 npm 依赖。
3. 执行 `npm run build`，生成 `frontend/dist`。

所以正常开发和部署时，不需要再单独执行一次 `go build -o bin/weibo-assistant ./cmd/weibo-assistant`。该命令保留在脚本内部，是完整构建的一个步骤；只有在明确只想编译后端、跳过前端时，才需要手动执行它。

构建成功后会得到：

```text
bin/weibo-assistant  Go 可执行文件
frontend/dist/       Vue 生产静态文件
```

### 2. 再运行构建后的子命令

只有生成 `bin/weibo-assistant` 后，才能执行下面这些生产命令：

```bash
./bin/weibo-assistant migrate
./bin/weibo-assistant server
./bin/weibo-assistant monitor
./bin/weibo-assistant frontend
```

- `migrate`：连接 PostgreSQL，执行尚未应用的 `migrations/*.sql`，并记录到 `schema_migrations`。
- `server`：启动 API、健康检查、Prometheus 指标和实时 WebSocket，默认端口 `6001`。
- `monitor`：启动 Chrome、处理二维码登录、CDP 消息监听、数据库写入和通知 worker。
- `frontend`：用 Go HTTP 服务托管已经构建好的 `frontend/dist`，默认端口 `6002`。

这些是同一个二进制的不同子命令，不是四套程序。`server`、`monitor` 和 `frontend` 是三个长期运行的进程，手动运行时需要三个终端，生产环境则分别交给三个 systemd 服务。

### 3. Vue 为什么构建时用 Node.js，运行时用 Go

前端源代码确实使用 Vue。Node.js 和 Go 分别负责不同阶段：

```text
Vue 源代码
  -> Node.js + Vite 构建
  -> frontend/dist 中的 HTML、JavaScript、CSS
  -> Go frontend 服务通过 HTTP 返回静态文件
  -> 用户浏览器执行 JavaScript 并渲染页面
```

- **开发阶段**需要 Node.js/Vite：编译 Vue、提供热更新和源码调试。
- **生产构建阶段**也需要 Node.js/Vite：把 Vue 源码生成 `frontend/dist`。
- **生产运行阶段**不需要常驻 Node.js：浏览器能直接运行构建后的 JavaScript，只需要一个 HTTP 服务发送这些文件。本项目选择复用 Go 二进制，也可以改用 Nginx 托管 `frontend/dist`。

`./bin/weibo-assistant frontend` 不会运行 Vue 编译器，也不会构建前端；它只读取并返回 `frontend/dist` 中的文件。因此必须先执行 `./scripts/build-linux.sh`。

### 4. 本地开发可以使用 go run

`go run` 会临时编译并立即运行，所以本地开发时不要求先生成 `bin/weibo-assistant`：

```bash
go run ./cmd/weibo-assistant server
go run ./cmd/weibo-assistant monitor
npm --prefix frontend run dev
```

这里使用 `npm run dev` 启动 Vue，是因为开发服务器提供热更新。`go run ./cmd/weibo-assistant frontend` 也能托管前端，但它只适合已有 `frontend/dist` 的情况，没有 Vue 热更新能力。

| 对比项 | `build-linux.sh` | `go run ...` | `npm run dev` |
| --- | --- | --- | --- |
| 作用 | 构建整个生产项目 | 临时编译并运行 Go | 运行 Vue 开发服务器 |
| 是否立即运行服务 | 否 | 是 | 是 |
| 是否生成 `bin/weibo-assistant` | 是 | 否 | 否 |
| 是否生成 `frontend/dist` | 是 | 否 | 否 |
| 是否支持 Vue 热更新 | 否 | 否 | 是 |

### 5. Linux 生产运行顺序

首次部署：

```bash
./scripts/build-linux.sh
./bin/weibo-assistant migrate

sudo APP_DIR="$HOME/repos/weibo-assistant" \
  RUN_USER="$(id -un)" \
  RUN_GROUP="$(id -gn)" \
  INSTALL_FRONTEND=true \
  ./scripts/install-systemd.sh

sudo systemctl enable --now weibo-assistant-server.service
sudo systemctl enable --now weibo-assistant-monitor.service
sudo systemctl enable --now weibo-assistant-frontend.service
```

monitor 首次启动或发现登录失效时会通过企业微信发送文字提示和二维码截图；扫码成功后自动开始或恢复监听。

代码更新后：

```bash
git pull
./scripts/build-linux.sh
./bin/weibo-assistant migrate
sudo systemctl restart weibo-assistant-server.service
sudo systemctl restart weibo-assistant-monitor.service
sudo systemctl restart weibo-assistant-frontend.service
```

systemd 使用 `bin/weibo-assistant`，不使用 `go run`。上述命令都应在项目根目录执行。

完整步骤见 [systemd 长期运行与日志](./docs/4.systemd长期运行与日志.md)。

## API 与健康检查

```bash
curl http://localhost:6001/livez
curl http://localhost:6001/readyz
curl http://localhost:6001/healthz
curl http://localhost:6001/metrics
curl http://localhost:6001/api/monitor/status
curl http://localhost:6001/api/groups
curl 'http://localhost:6001/api/messages?limit=50&includeRaw=false'
curl 'http://localhost:6001/api/groups/3/message-stats?yesterdayStart=2026-07-21T00%3A00%3A00%2B08%3A00&todayStart=2026-07-22T00%3A00%3A00%2B08%3A00&tomorrowStart=2026-07-23T00%3A00%3A00%2B08%3A00'
curl -OJ 'http://localhost:6001/api/groups/3/messages/export?since=2026-07-22T00%3A00%3A00%2B08%3A00&before=2026-07-23T00%3A00%3A00%2B08%3A00&format=csv'
curl http://localhost:6001/api/groups/3/history-sync
curl 'http://localhost:6001/api/notification-rules?enabled=true'
curl 'http://localhost:6001/api/notification-events?limit=50'
```

- `/livez`：只表示 API 进程存活，不访问依赖。
- `/readyz`：检查数据库和 monitor freshness；任一不可用时返回 HTTP `503`，响应同时包含最后消息时间和通知积压。
- `/healthz`：兼容入口，行为与 `/readyz` 相同。
- `/metrics`：Prometheus 文本指标。
- `/api/groups/{id}/message-stats`：用客户端传入的本地自然日边界统计今日和昨日成员消息。
- `/api/groups/{id}/messages/export`：按 `[since, before)` 区间导出 `csv`、`json` 或 `md`。

统计和导出只包含发送人微博 UID 为数字的真实成员消息，并排除 `system`、`event`、`recall` 及通用群聊事件；CSV 会对可能被表格软件识别为公式的单元格进行转义。

创建消息回溯任务：

```bash
curl -X POST http://localhost:6001/api/groups/3/history-sync \
  -H 'Content-Type: application/json' \
  -d '{
    "from": "2026-06-01T00:00:00+08:00",
    "to": "2026-07-01T00:00:00+08:00",
    "pages": 1000
  }'
```

- `from`：可选，向前同步到的最早时间。
- `to`：可选，只保存不晚于该时间的消息；与 `from` 一起构成时间区间。
- `pages`：可选，最多请求页数，范围 `1-1000`，默认 `1000`。
- `GET /api/groups/{id}/history-sync`：查询该群最近一次任务及分页、入库数量、停止原因和错误。

列表参数、ID、布尔值和时间参数会严格校验。JSON 请求体最大 `1 MiB`，拒绝未知字段和多个顶层 JSON 值。

### 通知规则示例

```bash
curl -X POST http://localhost:6001/api/notification-rules \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "目标用户文本提醒",
    "weiboGroupId": "4761715839862414",
    "weiboSenderId": "6252670367",
    "messageType": "text",
    "enabled": true
  }'
```

规则至少需要一个匹配条件。monitor 启动时会把 `TARGET_GROUP_*` 和 `TARGET_SENDER_*` 同步为“环境变量默认通知规则”。

## 可观测性与日志

`/metrics` 包含：

- 浏览器进程和 CDP 连接状态、重连次数。
- 最近 60 秒及累计消息解析数。
- 入库成功、失败和去重数。
- 通知成功、重试、最终失败和 outbox 积压。
- 网络请求追踪数和图片缓存数。
- server/monitor 内存及 goroutine 数。
- monitor 心跳年龄和最后消息时间。

生产 `info` 日志不输出群聊正文、群名和昵称，只记录稳定 ID、消息类型、正文长度和短 SHA-256。网络资源只保留主机、路径及少量排障所需参数，`data:` 内容与未知查询参数会被脱敏；服务端异常通过请求 ID 关联内部日志，不直接把错误细节返回前端。`LOG_LEVEL=debug` 会输出截断正文，只应在短期排查时启用。

systemd 部署时，server 和 monitor 分别写入 `logs/server.log`、`logs/monitor.log`；安装脚本同时配置每日轮转、保留 14 份和旧日志压缩。frontend service 仍写入 journald。具体命令见 [systemd 长期运行与日志](./docs/4.systemd长期运行与日志.md)。

## 测试

```bash
go test ./...
go vet ./...
cd frontend && npm run build
bash -n scripts/*.sh
```

## 安全注意事项

- 不要提交 `.env` 或 `.weibo-profile/`。
- Chrome DevTools 端口必须只绑定 `127.0.0.1`。
- `WEWORK_RELAY_API_KEY` 和数据库密码按密钥管理。
- 当前 API 适合受控的 Tailscale/内网部署；暴露到不可信网络前应增加鉴权和严格 Origin 白名单。
- 生产环境保持 `LOG_LEVEL=info`；项目 `logs/` 目录保持 `0750`、日志文件保持 `0640`，不要通过 Web 服务器暴露该目录。

## 文档

- [当前技术架构](./docs/1.技术方案.md)
- [PostgreSQL 常用命令](./docs/2.PostgreSQL常用命令.md)
- [PostgreSQL 部署教程](./docs/3.PostgreSQL部署教程.md)
- [systemd 长期运行与日志](./docs/4.systemd长期运行与日志.md)
- [后续优化建议](./docs/5.优化建议.md)
- [后端代码导读](./docs/6.后端代码导读.md)
- [变更记录](./CHANGELOG.md)

## 已知限制

该项目依赖微博网页版内部协议和页面行为，不是官方稳定 API。微博页面、消息结构、登录风控或浏览器行为发生变化时，可能需要更新解析和自动化逻辑。

尚未完成的工作见 [后续优化建议](./docs/5.优化建议.md)。
