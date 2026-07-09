# weibo-assistant

微博群聊监控服务。后端使用 Go，监听 `6001`；后续前端使用 Vue，监听 `6002`。

## 当前阶段

先开发后端和数据库：

1. 数据库 schema / migration
2. Go API 骨架
3. 微博登录态复用
4. 消息采集与入库
5. `wework-send` 通知集成

## 本地配置

```bash
cp .env.example .env
```

编辑 `.env`：

- 执行 `migrate` 建表时，只需要填 `DATABASE_URL`。
- 启动 `server` / `monitor` 并需要企业微信通知时，还需要填 `WEWORK_RELAY_API_KEY`。

## 命令

```bash
go run ./cmd/weibo-assistant server
go run ./cmd/weibo-assistant login
go run ./cmd/weibo-assistant migrate
```

`login` 会使用 `WEIBO_PROFILE_DIR` 打开一个独立的 Chrome / Chromium 用户目录。登录微博后关闭浏览器窗口，cookies 和 localStorage 会保存在该目录中，后续监控命令复用这个登录态。

如果程序找不到浏览器，在 `.env` 中设置：

```env
CHROME_EXECUTABLE=/usr/bin/google-chrome
```

`monitor` 会复用同一个 `WEIBO_PROFILE_DIR` 打开微博聊天页，并按 `HEALTH_CHECK_INTERVAL_MS` 持续写入心跳。当前版本会优先按 `TARGET_GROUP_ID` 自动选中目标群聊，再通过 Chrome DevTools Network 监听 WebSocket 帧，解析群聊消息并写入 PostgreSQL；DOM 探测仅保留为后续调试兜底。

如果配置了 `TARGET_SENDER_ID` 或 `TARGET_SENDER_NAME`，新入库消息匹配目标发送人时会调用 `WEWORK_RELAY_URL` 对应的企业微信中转服务发送文本通知。重复 WebSocket 帧只会返回已有消息，不会重复通知。

```bash
go run ./cmd/weibo-assistant monitor
```

查看监控状态：

```bash
curl "http://localhost:6001/api/monitor/status"
```

## 后端 API

服务默认监听 `6001`。

```bash
curl "http://localhost:6001/healthz"
curl "http://localhost:6001/api/groups"
curl "http://localhost:6001/api/users?q=测试"
curl "http://localhost:6001/api/messages?limit=50"
curl "http://localhost:6001/api/messages?groupId=1&senderId=1&q=hello"
curl "http://localhost:6001/api/messages?weiboGroupId=4761715839862414&weiboSenderId=6252670367&limit=50&includeRaw=false"
curl "http://localhost:6001/api/monitor/status"
curl "http://localhost:6001/api/notify/probe"
```

`groupId` / `senderId` 是本系统数据库里的内部自增 ID；`weiboGroupId` / `weiboSenderId` 是微博原始 ID，更适合用于稳定筛选目标群聊和目标发言人。

`includeRaw=false` 会从响应里去掉较大的原始 WebSocket payload，适合前端列表页使用。

采集器后续直接调用下面的正式接口写入消息；重复消息会按 `dedupeKey` 或微博消息 ID 去重：

```bash
curl -X POST "http://localhost:6001/api/messages" \
  -H "Content-Type: application/json" \
  -d '{
    "group": {
      "weiboGroupId": "dev-group-1",
      "name": "测试群"
    },
    "sender": {
      "weiboUserId": "dev-user-1",
      "nickname": "测试用户"
    },
    "messageType": "text",
    "content": "hello weibo group chat",
    "source": "weibo-web"
  }'
```

查询某个群聊的消息：

```bash
curl "http://localhost:6001/api/groups/1/messages?limit=50"
```

开发阶段也保留了 `POST /api/dev/messages`，未传 `source` 时默认写成 `dev`。

通知规则和通知事件：

```bash
curl -X POST "http://localhost:6001/api/notification-rules" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "测试用户提醒",
    "groupId": 1,
    "senderId": 1,
    "messageType": "text",
    "enabled": true
  }'

curl "http://localhost:6001/api/notification-rules?enabled=true"
curl "http://localhost:6001/api/notification-events?limit=50"
```

当前 monitor 的目标发送人通知优先走 `.env` 里的 `TARGET_SENDER_ID` / `TARGET_SENDER_NAME`，并会把发送结果写入 `notification_events`。上面的通知规则 API 先保留给后续前端管理多规则使用。

## 前端

前端在 `frontend/` 目录下，使用 Vue + Vite，默认监听 `6002`。

```bash
cd frontend
npm install
npm run dev
```

如果后端不在本机 `6001`，可以创建 `frontend/.env.local`：

```env
VITE_API_BASE=http://你的后端地址:6001
```
