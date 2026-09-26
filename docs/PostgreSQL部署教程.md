# PostgreSQL 部署教程

本文档记录 `weibo-assistant` 项目如何在 PostgreSQL 中创建独立数据库和应用用户。

目标：

- 数据库名：`weibo_data`
- 数据库拥有者：`weibo_user`
- 项目使用 `weibo_user` 连接 `weibo_data`

## 目录

- [1. 前提](#1-前提)
- [2. 进入 PostgreSQL](#2-进入-postgresql)
- [3. 创建用户和数据库](#3-创建用户和数据库)
- [4. 验证数据库和角色](#4-验证数据库和角色)
- [5. 配置项目](#5-配置项目)
- [6. 执行建表](#6-执行建表)
- [7. 常见问题](#7-常见问题)

## 1. 前提

先确认 PostgreSQL 客户端可用：

```bash
psql --version
```

如果 PostgreSQL 手动安装在用户目录且 `psql` 不在 `PATH` 中，可执行：

```bash
export PATH="$HOME/postgresql/bin:$PATH"
```

下文用 `postgres` 表示数据库管理员角色；若实际管理员角色不同，请自行替换。

## 2. 进入 PostgreSQL

连接默认管理库：

```bash
export PATH="$HOME/postgresql/bin:$PATH"
psql -h localhost -p 5432 -d postgres -U postgres
```

进入后会看到类似：

```text
postgres=#
```

退出 `psql` 使用：

```sql
\q
```

## 3. 创建用户和数据库

创建应用用户 `weibo_user`：

```sql
CREATE ROLE weibo_user WITH LOGIN PASSWORD '换成强密码';
```

创建数据库 `weibo_data`，并指定 owner 为 `weibo_user`：

```sql
CREATE DATABASE weibo_data
  WITH OWNER weibo_user
  ENCODING 'UTF8';
```

说明：

- `weibo_data` 使用下划线，不需要双引号。
- `weibo_user` 是项目后端连接数据库使用的应用用户。
- 不建议项目直接使用数据库管理员角色连接数据库。

## 4. 验证数据库和角色

进入 `weibo_data`：

```sql
\c weibo_data
```

查看数据库列表：

```sql
\l
```

应该能看到：

```text
weibo_data | weibo_user
```

查看当前连接：

```sql
\conninfo
```

查看角色：

```sql
\du weibo_user
```

退出：

```sql
\q
```

也可以用应用用户测试连接：

```bash
psql -h localhost -p 5432 -d weibo_data -U weibo_user
```

## 5. 配置项目

进入项目目录：

```bash
cd ~/repos/weibo-assistant
```

如果还没有 `.env`，先复制模板：

```bash
cp .env.example .env
```

编辑 `.env`：

```env
DATABASE_URL=postgresql://weibo_user:换成强密码@127.0.0.1:5432/weibo_data
```

注意：

- 密码如果包含 `@`、`#`、`:`、`/`、`?`、`&` 等特殊字符，需要 URL 编码。
- 为了减少问题，建议数据库密码使用字母、数字、下划线组合。
- 只执行建表时，不需要配置 `WEWORK_RELAY_API_KEY`。
- 不要把真实 `.env` 提交到仓库。

## 6. 执行建表

在项目目录执行：

```bash
go run ./cmd/weibo-assistant migrate
```

迁移器会：

- 创建并维护 `schema_migrations`。
- 按 `NNN_name.sql` 顺序执行未应用版本。
- 使用事务和 advisory lock 避免并发迁移。
- 保存 SQL 的 SHA-256；已执行文件被修改时拒绝继续迁移。

不要修改已经执行过的迁移文件。数据库变更应新增更高版本的 SQL。

成功后，可以用 `psql` 查看表：

```bash
psql -h localhost -p 5432 -d weibo_data -U weibo_user
```

```sql
\dt
```

应该能看到类似表：

```text
weibo_groups
weibo_users
weibo_messages
weibo_message_attachments
notification_rules
notification_outbox
notification_events
monitor_status
monitor_runtime_metrics
history_sync_jobs
media_resolution_jobs
schema_migrations
```

`server` 不要求企业微信密钥，但除了数据库和端口配置，还需要可读取的 `FRONTEND_DIST_DIR`（先运行 `pnpm --dir frontend run build` 或 `./scripts/build-linux.sh`）。企业微信配置由 `monitor` 的通知 worker 使用。启动 API：

```bash
go run ./cmd/weibo-assistant server
```

健康检查：

```bash
curl "http://localhost:6001/livez"
curl "http://localhost:6001/readyz"
```

`/livez` 只检查 API 进程，server 启动后应返回 `200`。`/readyz` 还要求 monitor 心跳新鲜；尚未启动 monitor 时返回 `503` 是预期行为。

## 7. 常见问题

### 7.1 role weibo_user already exists

说明用户已经创建过。

如需修改密码：

```sql
ALTER ROLE weibo_user WITH PASSWORD '新的强密码';
```

### 7.2 database weibo_data already exists

说明数据库已经创建过。

查看 owner：

```sql
\l
```

如果 owner 不是 `weibo_user`，可以修改：

```sql
ALTER DATABASE weibo_data OWNER TO weibo_user;
```

### 7.3 permission denied for schema public

如果执行 migration 时提示 `permission denied for schema public`，用管理员用户进入 `weibo_data`：

```bash
psql -h localhost -p 5432 -d weibo_data -U postgres
```

授权：

```sql
GRANT USAGE, CREATE ON SCHEMA public TO weibo_user;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO weibo_user;
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO weibo_user;
```

### 7.4 pg_trgm 不存在

`pg_trgm` 是 PostgreSQL 的 contrib 扩展，常用于加速 `ILIKE '%关键词%'` 这类模糊搜索。

当前项目的首个建表迁移不依赖 `pg_trgm`。如果后续要给消息内容、昵称等字段增加模糊搜索索引，再检查服务器是否安装了这个扩展：

```sql
\dx
```

如果执行下面命令时报 `pg_trgm.control` 不存在：

```sql
CREATE EXTENSION IF NOT EXISTS pg_trgm;
```

说明当前 PostgreSQL 安装目录缺少 contrib 扩展文件，需要先在系统层面安装对应 PostgreSQL 版本的 contrib 包，或者从源码编译安装 `contrib/pg_trgm`。

### 7.5 psql 提示符变成 postgres-#

说明当前 SQL 没有结束，通常是引号、括号或分号没闭合。

按 `Ctrl+C` 取消当前输入。

### 7.6 不小心 Ctrl+Z 挂起了 psql

回到 shell 后执行：

```bash
fg
```

再按 `Ctrl+C` 取消当前输入，最后用：

```sql
\q
```

退出。
