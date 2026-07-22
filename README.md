# weibo-assistant

微博群聊监听与提醒服务。它复用 Chrome 登录态，通过 Chrome DevTools Protocol 监听微博网页版群聊消息，将消息保存到 PostgreSQL，并按数据库通知规则异步发送企业微信提醒。

## 目录

- [已实现能力](#已实现能力)
- [运行架构](#运行架构)
- [消息补偿与消息回溯](#消息补偿与消息回溯)
- [项目结构](#项目结构)
  - [根目录文件和运行时目录](#根目录文件和运行时目录)
  - [Go 程序入口](#go-程序入口)
  - [后端源码 internal](#后端源码-internal)
  - [Vue 前端 frontend](#vue-前端-frontend)
  - [数据库迁移 migrations](#数据库迁移-migrations)
  - [部署模板和运维脚本](#部署模板和运维脚本)
  - [文档目录 docs](#文档目录-docs)
  - [推荐阅读顺序](#推荐阅读顺序)
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

## 已实现能力

- Chrome/Chromium 持久化登录态和无头运行。
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

## 运行架构

```text
Chrome 微博群聊页
  -> CDP Network/WebSocket
  -> 消息解析
  -> PostgreSQL 事务写入
       -> notification_outbox
       -> pg_notify

monitor worker -> 企业微信中转服务
server API     -> PostgreSQL -> 浏览器 WebSocket
frontend       -> server API / WebSocket
```

生产环境通常运行三个进程：

- `server`：API、健康检查、指标和前端 WebSocket，默认端口 `6001`。
- `monitor`：Chrome、微博消息监听、入库和通知 worker。
- `frontend`：托管 `frontend/dist`，默认端口 `6002`。

## 消息补偿与消息回溯

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

## 项目结构

下面只列出需要理解和维护的文件。`node_modules`、Go 缓存以及 Chrome 内部生成的大量文件没有展开。

```text
weibo-assistant/
├── cmd/weibo-assistant/       Go 程序入口
├── internal/                  后端业务源码
│   ├── app/                   组装并启动各个服务
│   ├── browser/               Chrome、CDP 和微博消息采集
│   ├── config/                环境变量加载与校验
│   ├── httpapi/               HTTP API、健康检查和 WebSocket
│   ├── migrate/               数据库迁移执行器
│   ├── notifier/              企业微信中继客户端
│   └── store/                 PostgreSQL 数据访问和事务
├── frontend/                  Vue 前端工程
│   ├── src/                   Vue/TypeScript 源码
│   └── dist/                  前端生产构建产物
├── migrations/                PostgreSQL 版本化迁移 SQL
├── deploy/systemd/            systemd service 模板
├── deploy/logrotate/           server/monitor 日志轮转模板
├── scripts/                   构建、安装和运维脚本
├── docs/                      架构、数据库和部署文档
├── bin/                       Go 编译产物
├── .weibo-profile/            Chrome 登录态和用户数据
├── logs/                      server/monitor 运行日志
├── .env                       当前环境的私密配置
├── .env.example               可提交的配置示例
├── go.mod / go.sum            Go 模块及依赖版本
├── README.md                  项目入口文档
└── CHANGELOG.md               重要变更记录
```

### 根目录文件和运行时目录

| 路径 | 作用 | 是否手动维护 |
| --- | --- | --- |
| `README.md` | 快速开始、配置、构建、运行和部署入口 | 是 |
| `CHANGELOG.md` | 记录新增、修改、修复和安全变化 | 是 |
| `.env.example` | 环境变量模板，只放示例值和说明 | 是 |
| `.env` | 当前机器的数据库密码、通知密钥和运行参数 | 是，但禁止提交 |
| `.gitignore` | 指定 Git 不应跟踪的私密文件、缓存和构建产物 | 是 |
| `go.mod` | 声明 Go 模块名、Go 版本和直接依赖 | 通过 Go 命令维护 |
| `go.sum` | 锁定依赖内容校验和，保证依赖下载一致 | 通过 Go 命令维护 |
| `bin/weibo-assistant` | `build-linux.sh` 生成的 Go 可执行文件 | 否，可重新构建 |
| `.weibo-profile/` | Chrome Cookie、localStorage 和微博登录状态 | 否，私密且禁止提交 |
| `logs/` | systemd 部署后生成的 server、monitor 日志与轮转文件 | 否，由服务和 logrotate 维护 |
| `.gomodcache/` | 本地 Go 依赖缓存 | 否，可删除后重新生成 |

`.env` 和 `.weibo-profile/` 都可能包含账号凭证。复制、备份和设置服务器权限时应把它们当作密钥处理。

### Go 程序入口

| 文件 | 作用 |
| --- | --- |
| `cmd/weibo-assistant/main.go` | 读取 `server`、`monitor`、`frontend`、`migrate`、`login` 子命令，加载配置，处理退出信号并调用 `internal/app` |

项目只生成一个 `bin/weibo-assistant`，五个子命令共享同一个入口。`cmd` 目录只负责启动，不应放具体的 SQL、HTTP handler 或微博解析逻辑。

### 后端源码 internal

Go 的 `internal` 目录具有语言级访问限制：这些包只能被当前项目内部代码导入，不能被其他仓库直接当成公共库使用。

| 目录或文件 | 作用 |
| --- | --- |
| `internal/app/app.go` | 应用装配层；创建数据库、API、monitor、指标上报和通知 worker，并处理优雅退出 |
| `internal/app/frontend.go` | 用 Go HTTP 服务托管 `frontend/dist`，包含单页应用路由回退 |
| `internal/app/image_cache.go` | 保存图片 FID 到 URL 的有界 TTL 缓存 |
| `internal/app/history_sync.go` | 执行启动补偿、断线补漏和消息回溯任务，控制分页及停止边界 |
| `internal/app/metrics.go` | 收集 monitor 进程指标并定期写入 PostgreSQL |
| `internal/browser/login.go` | 使用持久化 profile 打开 Chrome，供首次微博登录 |
| `internal/browser/monitor.go` | 启动 Chrome、探测页面、选择目标群、监听网络事件、解析消息并负责 CDP 自动重连 |
| `internal/browser/cdp.go` | 实现与 Chrome DevTools Protocol WebSocket 的连接、请求和帧读写 |
| `internal/browser/history.go` | 通过已登录页面请求微博历史接口并解析为统一消息模型 |
| `internal/config/config.go` | 从 `.env` 和系统环境变量读取配置，并按子命令分别校验 |
| `internal/httpapi/server.go` | 注册 API、健康检查、指标和开发路由，配置 CORS |
| `internal/httpapi/messages.go` | 群组、用户、消息、通知规则和通知事件的 HTTP handler |
| `internal/httpapi/history_sync.go` | 创建和查询历史同步任务的 HTTP handler |
| `internal/httpapi/message_exports.go` | 成员消息的自然日统计和 CSV、JSON、Markdown 导出 handler |
| `internal/httpapi/realtime.go` | 监听 PostgreSQL `LISTEN/NOTIFY` 并转换为前端实时事件 |
| `internal/httpapi/websocket.go` | 管理浏览器 WebSocket 客户端、握手、帧读取和广播 |
| `internal/migrate/migrate.go` | 按版本执行 `migrations/*.sql`，校验 SHA-256 并防止并发迁移 |
| `internal/notifier/wework.go` | 调用企业微信中继服务；重试调度由 app/outbox worker 负责 |
| `internal/store/store.go` | 创建 PostgreSQL 连接池、封装事务、monitor 状态和实时事件 |
| `internal/store/messages.go` | 群组、用户、消息和附件的查询、事务写入及数据库幂等去重 |
| `internal/store/history_sync.go` | 历史同步任务入队、领取、进度、完成状态和消息边界查询 |
| `internal/store/message_exports.go` | 成员消息统计和按时间区间导出查询 |
| `internal/store/notifications.go` | 通知规则匹配和通知事件查询 |
| `internal/store/outbox.go` | 通知任务入队、并发安全领取、成功和失败状态更新 |
| `internal/store/observability.go` | 运行指标、通知积压和最后消息时间的数据库读写 |
| `internal/**/*_test.go` | 与相邻实现对应的 Go 自动化测试，不参与生产二进制运行 |
| `internal/*/doc.go` | 每个包的职责说明，便于 Go 文档工具展示 |

`internal/app` 是各层的连接点：`browser` 采集消息，`store` 保存消息，`notifier` 调用第三方接口，`httpapi` 向前端提供数据。各底层包之间尽量不相互依赖。

### Vue 前端 frontend

| 文件或目录 | 作用 |
| --- | --- |
| `frontend/package.json` | npm 脚本和前端依赖声明 |
| `frontend/package-lock.json` | 锁定 npm 依赖版本，部署时应一并提交 |
| `frontend/vite.config.ts` | Vite 开发和构建配置 |
| `frontend/tsconfig.json` | TypeScript 编译配置 |
| `frontend/index.html` | Vite HTML 入口 |
| `frontend/src/main.ts` | 创建并挂载 Vue 应用 |
| `frontend/src/App.vue` | 页面数据、WebSocket 和跨组件状态编排 |
| `frontend/src/components/` | 群聊侧栏、筛选栏、消息、管理中心和消息详情组件 |
| `frontend/src/components/HistorySyncDialog.vue` | 历史消息时间范围选择和任务进度弹窗 |
| `frontend/src/utils/presentation.ts` | 共享时间、头像颜色和状态文案格式化 |
| `frontend/src/api.ts` | 后端 API、WebSocket 地址和请求封装 |
| `frontend/src/styles.css` | 全局页面样式 |
| `frontend/src/vite-env.d.ts` | Vite 环境变量的 TypeScript 类型声明 |
| `frontend/dist/` | `npm run build` 生成的 HTML、JavaScript 和 CSS，禁止手动修改 |
| `frontend/node_modules/` | npm 下载的依赖，禁止提交，可重新安装 |

开发时由 Node.js/Vite 运行 `frontend/src`；生产时 Node.js 先构建出 `frontend/dist`，再由 Go `frontend` 子命令或 Nginx 托管。

前端使用 Ant Design Vue `4.2.6` 和 `@ant-design/icons-vue`，日期计算使用 Day.js；`src/main.ts` 统一注册页面使用的组件，视觉细节由 `src/styles.css` 管理。

### 数据库迁移 migrations

| 文件 | 作用 |
| --- | --- |
| `001_init.sql` | 创建群组、用户、消息、附件、通知规则、通知事件和 monitor 状态等基础表与索引 |
| `002_notification_outbox.sql` | 增加企业微信异步通知任务表及待处理、超时任务索引 |
| `003_notification_rule_matching.sql` | 扩展通知规则匹配字段和约束 |
| `004_observability_metrics.sql` | 增加跨进程 monitor 运行指标表 |
| `005_notification_rule_archive.sql` | 为通知规则增加安全归档字段和活动规则索引 |
| `006_stable_weibo_message_id.sql` | 规范微博消息 ID 并加强稳定去重 |
| `007_classify_generic_groupchat_events.sql` | 规范通用群聊事件的消息类型 |
| `008_history_sync_jobs.sql` | 增加历史同步任务队列、进度和单群活动任务约束 |
| `009_generalize_missing_media_url_status.sql` | 将缺少媒体 URL 的状态统一为与消息来源无关的名称 |
| `010_media_resolution_jobs.sql` | 增加由前端请求、monitor 使用登录态消费的图片 FID 解析任务队列 |
| `011_repair_decorative_image_attachments.sql` | 清除误识别的身份徽章、图标和 fid 不匹配的图片 URL，并创建重新解析任务 |
| `012_repair_videopicfeed_role_icons.sql` | 清理历史 `videopicfeed` 皇冠身份图标污染并重新创建真实图片解析任务 |

迁移文件按 `NNN_name.sql` 命名。已经执行过的迁移不要修改；数据库结构继续变化时，应新增下一个编号的文件。

### 部署模板和运维脚本

| 文件 | 作用 |
| --- | --- |
| `deploy/systemd/weibo-assistant-server.service` | API server 的 systemd 模板 |
| `deploy/systemd/weibo-assistant-monitor.service` | Chrome monitor 的 systemd 模板 |
| `deploy/systemd/weibo-assistant-frontend.service` | Go 静态前端服务的 systemd 模板 |
| `deploy/logrotate/weibo-assistant` | server 和 monitor 项目日志的轮转模板 |
| `scripts/build-linux.sh` | 编译 Go，并安装、构建 Vue 前端 |
| `scripts/install-systemd.sh` | 将路径和运行用户写入模板，安装到 `/etc/systemd/system` |
| `scripts/systemd-status.sh` | 查看一个或全部项目服务的 systemd 状态 |
| `scripts/systemd-logs.sh` | 查看并跟随 server、monitor 项目日志或 frontend journal |

### 文档目录 docs

| 文件 | 作用 |
| --- | --- |
| `1.技术方案.md` | 当前架构、数据流、可靠性和安全边界 |
| `2.PostgreSQL常用命令.md` | PostgreSQL 查询、诊断、备份和恢复 |
| `3.PostgreSQL部署教程.md` | 数据库、用户、权限和连接配置 |
| `4.systemd长期运行与日志.md` | Linux 构建、部署、systemd 管理和排障 |
| `5.优化建议.md` | 尚未完成的后续改进事项 |
| `6.后端代码导读.md` | 面向前端开发者的 Go 后端阅读顺序和基础概念 |

### 推荐阅读顺序

如果准备从前端逐步学习后端，建议按下面的顺序阅读：

1. `cmd/weibo-assistant/main.go`：理解程序入口和子命令。
2. `internal/config/config.go`：理解服务依赖的环境变量。
3. `internal/app/app.go`：理解各个包怎样组合成进程。
4. `internal/httpapi/server.go` 和 `messages.go`：理解一次 HTTP 请求。
5. `internal/store/store.go` 和 `messages.go`：理解连接池、SQL 和事务。
6. `internal/store/outbox.go`：理解可靠异步任务。
7. `internal/browser/monitor.go`：最后阅读复杂度最高的 Chrome/CDP 采集流程。
8. 对照相邻的 `*_test.go`，观察每个行为如何验证。

## 环境要求

- Go `1.26+`
- PostgreSQL
- Chromium
- Node.js/npm，仅用于构建或开发 Vue 前端
- Linux 生产部署：systemd `240+` 和 logrotate
- 可选：企业微信消息中转服务

## 快速开始

### 1. 配置

```bash
cp .env.example .env
```

至少设置数据库连接：

```env
DATABASE_URL=postgresql://用户名:密码@127.0.0.1:5432/weibo_data
```

如需通知，再配置：

```env
TARGET_GROUP_ID=
TARGET_SENDER_ID=
WEWORK_RELAY_URL=http://你的中转服务:5001
WEWORK_RELAY_API_KEY=
WEWORK_RELAY_TOUSER=
```

微博原始 ID 比群名和昵称稳定，建议优先配置 ID。

### 2. 执行迁移

```bash
go run ./cmd/weibo-assistant migrate
```

迁移记录保存在 `schema_migrations`。同一版本只执行一次；已执行 SQL 的 SHA-256 发生变化时会拒绝继续迁移。

### 3. 首次登录微博

```bash
HEADLESS=false go run ./cmd/weibo-assistant login
```

登录后关闭浏览器，Cookie、localStorage 等状态保存在 `WEIBO_PROFILE_DIR`，默认 `.weibo-profile`。

### 4. 本地开发

分别在三个终端启动 API、monitor 和 Vue 开发服务器：

```bash
# 终端 1
go run ./cmd/weibo-assistant server

# 终端 2
go run ./cmd/weibo-assistant monitor

# 终端 3
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
| `FRONTEND_PORT` | `6002` | 前端静态服务和 CORS 端口 |
| `FRONTEND_DIST_DIR` | `frontend/dist` | Vue 构建产物目录 |
| `WEIBO_PROFILE_DIR` | `.weibo-profile` | Chrome 登录态目录 |
| `HEADLESS` | `true` | monitor 是否无头运行 |
| `CHROME_DEBUG_PORT` | `9222` | 仅本机使用的 CDP 端口 |
| `TARGET_GROUP_ID` | 空 | 默认规则的微博群 ID |
| `TARGET_SENDER_ID` | 空 | 默认规则的微博发送人 ID |
| `NOTIFICATION_MAX_ATTEMPTS` | `8` | 通知最大尝试次数 |
| `HEALTH_CHECK_INTERVAL_SECONDS` | `60` | monitor 每隔多少秒检查一次 Chrome、微博页面和目标群状态，并把本次心跳写入数据库 |
| `MONITOR_STALE_AFTER_SECONDS` | `180` | server 多久没有看到新的 monitor 心跳后，将 monitor 判定为 stale（可能卡死或已退出） |
| `IMAGE_CACHE_TTL_SECONDS` | `86400` | 图片 URL 缓存 TTL |
| `IMAGE_CACHE_MAX_ITEMS` | `1000` | 图片 URL 缓存上限 |
| `LOG_LEVEL` | `info` | `debug/info/warn/error` |

心跳和新消息是两个独立概念。只要 monitor 正常运行，它即使没有采集到新消息，也会每隔 `HEALTH_CHECK_INTERVAL_SECONDS` 更新一次心跳。因此群聊连续数小时没有新消息不会触发 stale；只有心跳本身超过 `MONITOR_STALE_AFTER_SECONDS` 没有更新，才会被认为异常。`lastMessageAt` 仅用于展示最后一次收到消息的时间，不参与健康判断。

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
./bin/weibo-assistant login
./bin/weibo-assistant server
./bin/weibo-assistant monitor
./bin/weibo-assistant frontend
```

- `migrate`：连接 PostgreSQL，执行尚未应用的 `migrations/*.sql`，并记录到 `schema_migrations`。
- `login`：打开 Chrome 供你手动登录微博，并将登录状态保存到 `WEIBO_PROFILE_DIR`。
- `server`：启动 API、健康检查、Prometheus 指标和实时 WebSocket，默认端口 `6001`。
- `monitor`：启动 Chrome、CDP 消息监听、数据库写入和通知 worker。
- `frontend`：用 Go HTTP 服务托管已经构建好的 `frontend/dist`，默认端口 `6002`。

这些是同一个二进制的不同子命令，不是五套程序。`server`、`monitor` 和 `frontend` 是三个长期运行的进程，手动运行时需要三个终端，生产环境则分别交给三个 systemd 服务。

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
# 首次登录或登录失效时执行：
HEADLESS=false ./bin/weibo-assistant login

sudo APP_DIR="$HOME/repos/weibo-assistant" \
  RUN_USER="$(id -un)" \
  RUN_GROUP="$(id -gn)" \
  INSTALL_FRONTEND=true \
  ./scripts/install-systemd.sh

sudo systemctl enable --now weibo-assistant-server.service
sudo systemctl enable --now weibo-assistant-monitor.service
sudo systemctl enable --now weibo-assistant-frontend.service
```

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
- `/readyz`：检查数据库，并返回 monitor freshness、最后消息时间和通知积压。
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

生产 `info` 日志不输出群聊正文、群名和昵称，只记录稳定 ID、消息类型、正文长度和短 SHA-256。`LOG_LEVEL=debug` 会输出截断正文，只应在短期排查时启用。

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
