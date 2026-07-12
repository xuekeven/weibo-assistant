# Changelog

本文件记录 `weibo-assistant` 的重要变化。格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，项目当前尚未发布正式版本。

## [Unreleased]

### Added

- 新增 PostgreSQL 通知 outbox，支持异步企业微信投递。
- 新增通知失败指数退避重试，默认最多尝试 8 次，最大间隔 15 分钟。
- 新增 outbox 租约恢复，异常退出后可重新领取超时 sending 任务。
- 通知规则支持微博群 ID、群名、微博发送人 ID、昵称、内部 ID 和消息类型。
- monitor 启动时自动同步“环境变量默认通知规则”。
- 新增 `schema_migrations` 版本表、迁移 SHA-256 校验和和 PostgreSQL advisory lock。
- 新增 `weibo-assistant frontend` Go 静态文件服务。
- 新增 `/livez`、`/readyz` 和 Prometheus `/metrics`。
- monitor 状态新增 freshness、心跳年龄、最后消息时间和 stale 阈值。
- 新增跨进程 `monitor_runtime_metrics`，覆盖浏览器、CDP、消息、通知、缓存、内存和 goroutine。
- 新增线程安全、带 TTL 与容量上限的图片 URL 缓存。
- 新增网络请求追踪硬上限和批量淘汰。
- 新增通知重试中前端状态及 monitor 心跳过期展示。
- 新增 systemd 构建、安装、状态和日志脚本。
- 新增配置、HTTP、迁移、缓存、指标和静态文件服务测试。

### Changed

- monitor 心跳间隔配置由毫秒制的 `HEALTH_CHECK_INTERVAL_MS` 改为秒制的 `HEALTH_CHECK_INTERVAL_SECONDS`；默认每 60 秒检查一次，连续 180 秒没有心跳时判定 stale。
- 为 Go 后端补充包级职责、关键调用链和并发/事务设计注释，并新增面向前端开发者的代码导读。
- 消息创建改为单个 PostgreSQL 事务，覆盖群组、用户、消息、附件、时间线、规则匹配和 outbox。
- `pg_notify` 改为事务提交后的 best-effort 实时刷新。
- 页面定时重载改为当前 target 内的 `Page.reload`。
- 健康检查每次读取真实 Chrome target；target 变化时重新绑定网络监听。
- 企业微信发送从 CDP 读取循环移到独立 worker。
- outbox 唯一性调整为“消息 + 通知规则”，允许一条消息触发多条规则。
- `server`、`monitor` 和 `frontend` 使用独立配置校验。
- `LOG_LEVEL` 正式接入 Go `slog`。
- `APP_ENV=production` 时不再注册开发消息接口。
- 生产前端 systemd 从 `vite preview` 改为 Go 静态文件服务。
- HTTP API 增加读取、写入、空闲和 Header 超时。
- JSON 请求体限制为 1 MiB，并拒绝未知字段和多个顶层值。
- 列表 limit、ID、时间和布尔参数改为严格校验。
- WebSocket 增加版本 13、客户端 masking、帧大小和读写 deadline 校验。
- README 和 docs 重写为当前实现，优化建议只保留未完成事项。

### Fixed

- CDP 网络监听异常退出后改为在当前 monitor/Chrome 进程内自动重连，并使用最高 30 秒的指数退避；登录失效或 Chrome 退出仍会终止 monitor。
- 修复页面重载创建新 target 后仍监听旧 CDP target 的假健康问题。
- 修复消息部分写入成功、后续步骤失败造成的数据不一致。
- 修复 `pg_notify` 失败导致已入库消息被错误视为写入失败。
- 修复同步企业微信请求阻塞微博网络事件读取。
- 修复网络请求 map 在长期运行中无界增长。
- 修复图片 FID 缓存无界增长和并发读写普通 map。
- 修复数据库通知规则 API 存在但不参与实际通知匹配。
- 修复 server 启动时覆盖真实 monitor 状态。
- 修复 `monitor_status.last_message_at` 未随成功入库更新。
- 修复前端无法识别 monitor 心跳过期。

### Security

- 生产 info 日志不再输出群聊正文、群名或昵称。
- info 日志仅保留稳定 ID、消息类型、正文长度和短 SHA-256。
- 原始 WebSocket payload、DOM 文本和截断正文降为 debug 日志。
- Chrome Profile 目录按 `0700` 创建，CDP 端口固定绑定 `127.0.0.1`。
- 生产环境关闭 `/api/dev/messages`。

### Removed

- 删除未使用配置 `MONITOR_POLL_INTERVAL_MS`。
- 删除未使用配置 `DEDUPE_TTL_SECONDS` 和 `DEDUPE_MAX_ITEMS`。
- 删除生产环境对 `npm run preview` 的依赖。

## [0.1.0] - Initial MVP

### Added

- Go API、Vue 前端和 PostgreSQL 初始数据模型。
- Chrome 持久化登录态和微博 WebSocket 监听。
- 群聊消息入库、查询和企业微信目标用户通知。
- 图片消息 FID 识别和初步 URL 补全。
