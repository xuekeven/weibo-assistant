# PostgreSQL 常用命令

这是一份 PostgreSQL 运维和开发命令手册。示例默认围绕 `weibo-assistant` 的 `weibo_data` 数据库和 `weibo_user` 应用角色；请按实际环境替换主机、端口和管理员用户名。

## 目录

- [1. 项目数据库速查](#1-项目数据库速查)
- [2. 准备 psql 命令环境](#2-准备-psql-命令环境)
- [3. 连接数据库](#3-连接数据库)
- [4. psql 内常用命令](#4-psql-内常用命令)
- [5. 角色和权限](#5-角色和权限)
- [6. 数据库管理](#6-数据库管理)
- [7. Schema 管理](#7-schema-管理)
- [8. 表和索引](#8-表和索引)
- [9. 扩展](#9-扩展)
- [10. 常用查询](#10-常用查询)
- [11. 权限授权](#11-权限授权)
- [12. 连接和进程排查](#12-连接和进程排查)
- [13. 备份和恢复](#13-备份和恢复)
- [14. 手动安装常见操作](#14-手动安装常见操作)
- [15. Docker 安装常见操作](#15-docker-安装常见操作)
- [16. 应用连接串](#16-应用连接串)
- [17. 常见问题](#17-常见问题)
- [18. weibo-assistant 诊断查询](#18-weibo-assistant-诊断查询)

## 1. 项目数据库速查

应用连接串：

```env
DATABASE_URL=postgresql://weibo_user:密码@127.0.0.1:5432/weibo_data
```

使用 `.env` 连接，避免在命令历史中重复写密码：

```bash
set -a
. ./.env
set +a
psql "$DATABASE_URL"
```

进入后优先检查：

```sql
SELECT version();
SELECT current_database(), current_user;
\dt
SELECT version, name, applied_at FROM schema_migrations ORDER BY version;
```

不要在生产环境直接执行本文中的 `DROP`、`TRUNCATE`、批量 `DELETE` 或恢复命令。执行前应确认目标库并完成备份。

## 2. 准备 psql 命令环境

查看 `psql` 是否可用：

```bash
psql --version
```

如果提示 `command not found`，先把 PostgreSQL 客户端目录加入当前 shell：

```bash
export PATH="$HOME/postgresql/bin:$PATH"
```

长期生效可写入 shell 配置：

```bash
echo 'export PATH="$HOME/postgresql/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
```

如果使用 zsh：

```bash
echo 'export PATH="$HOME/postgresql/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

常用客户端工具：

```bash
psql --version
pg_isready --version
pg_dump --version
pg_restore --version
```

## 3. 连接数据库

连接默认管理库：

```bash
psql -h localhost -p 5432 -d postgres -U postgres
```

使用当前 Linux 用户名连接：

```bash
psql -h localhost -p 5432 -d postgres -U "$(whoami)"
```

连接指定数据库：

```bash
psql -h localhost -p 5432 -d "数据库名" -U "用户名"
```

连接时直接执行 SQL：

```bash
psql -h localhost -p 5432 -d postgres -U postgres -c "SELECT version();"
```

使用连接串连接：

```bash
psql "postgresql://用户名:密码@127.0.0.1:5432/数据库名"
```

不想在命令行暴露密码时，可使用环境变量：

```bash
export PGPASSWORD='数据库密码'
psql -h localhost -p 5432 -d "数据库名" -U "用户名"
unset PGPASSWORD
```

## 4. psql 内常用命令

退出：

```sql
\q
```

查看帮助：

```sql
\?
```

查看 SQL 帮助：

```sql
\h CREATE DATABASE
```

查看当前连接：

```sql
\conninfo
```

切换数据库：

```sql
\c 数据库名
```

数据库名包含横杠等特殊字符时要加双引号：

```sql
\c "weibo-data"
```

列出数据库：

```sql
\l
```

列出角色：

```sql
\du
```

列出 Schema：

```sql
\dn
```

列出表：

```sql
\dt
```

列出所有 Schema 下的表：

```sql
\dt *.*
```

查看表结构：

```sql
\d 表名
```

查看更详细表结构：

```sql
\d+ 表名
```

列出扩展：

```sql
\dx
```

打开查询耗时：

```sql
\timing on
```

关闭分页器：

```sql
\pset pager off
```

## 5. 角色和权限

查看当前用户：

```sql
SELECT current_user;
```

查看所有角色：

```sql
SELECT rolname, rolsuper, rolcreatedb, rolcreaterole, rolcanlogin
FROM pg_roles
ORDER BY rolname;
```

查看指定角色：

```sql
SELECT rolname, rolsuper, rolcreatedb, rolcreaterole, rolcanlogin
FROM pg_roles
WHERE rolname = 'weibo_user';
```

含义：

- `rolsuper = t`：超级用户。
- `rolcreatedb = t`：可以创建数据库。
- `rolcreaterole = t`：可以创建和管理角色。
- `rolcanlogin = t`：可以登录。

创建登录用户：

```sql
CREATE ROLE app_user WITH LOGIN PASSWORD '强密码';
```

创建可建库用户：

```sql
CREATE ROLE admin_user WITH LOGIN CREATEDB PASSWORD '强密码';
```

修改密码：

```sql
ALTER ROLE app_user WITH PASSWORD '新密码';
```

允许用户创建数据库：

```sql
ALTER ROLE app_user CREATEDB;
```

禁止用户创建数据库：

```sql
ALTER ROLE app_user NOCREATEDB;
```

删除角色：

```sql
DROP ROLE app_user;
```

如果角色仍拥有对象，需要先转移或删除对象。

## 6. 数据库管理

查看数据库 owner：

```sql
\l
```

或使用 SQL：

```sql
SELECT datname, pg_catalog.pg_get_userbyid(datdba) AS owner
FROM pg_database
ORDER BY datname;
```

创建数据库：

```sql
CREATE DATABASE app_data
  WITH OWNER app_user
  ENCODING 'UTF8';
```

数据库名包含横杠时：

```sql
CREATE DATABASE "weibo-data"
  WITH OWNER weibo_user
  ENCODING 'UTF8';
```

修改数据库 owner：

```sql
ALTER DATABASE weibo_data OWNER TO weibo_user;
```

重命名数据库：

```sql
ALTER DATABASE old_name RENAME TO new_name;
```

删除数据库：

```sql
DROP DATABASE weibo_data;
```

如果数据库有连接，先断开：

```sql
SELECT pg_terminate_backend(pid)
FROM pg_stat_activity
WHERE datname = 'weibo_data'
  AND pid <> pg_backend_pid();
```

再删除：

```sql
DROP DATABASE weibo_data;
```

## 7. Schema 管理

查看当前 search path：

```sql
SHOW search_path;
```

创建 Schema：

```sql
CREATE SCHEMA app AUTHORIZATION app_user;
```

切换搜索路径：

```sql
SET search_path TO app, public;
```

为角色设置默认搜索路径：

```sql
ALTER ROLE weibo_user IN DATABASE weibo_data SET search_path TO public;
```

授权使用 Schema：

```sql
GRANT USAGE ON SCHEMA public TO app_user;
```

授权创建对象：

```sql
GRANT CREATE ON SCHEMA public TO app_user;
```

## 8. 表和索引

创建表：

```sql
CREATE TABLE users (
  id bigint generated always as identity primary key,
  name text not null,
  email text unique,
  created_at timestamptz not null default now()
);
```

添加字段：

```sql
ALTER TABLE users ADD COLUMN avatar_url text;
```

修改字段类型：

```sql
ALTER TABLE users ALTER COLUMN name TYPE varchar(100);
```

设置非空：

```sql
ALTER TABLE users ALTER COLUMN name SET NOT NULL;
```

删除字段：

```sql
ALTER TABLE users DROP COLUMN avatar_url;
```

重命名表：

```sql
ALTER TABLE users RENAME TO app_users;
```

删除表：

```sql
DROP TABLE app_users;
```

创建普通索引：

```sql
CREATE INDEX idx_users_created_at ON users (created_at DESC);
```

创建唯一索引：

```sql
CREATE UNIQUE INDEX idx_users_email ON users (email);
```

查看索引：

```sql
\di
```

删除索引：

```sql
DROP INDEX idx_users_created_at;
```

## 9. 扩展

查看扩展：

```sql
\dx
```

查看可安装扩展：

```sql
SELECT name, default_version, installed_version
FROM pg_available_extensions
ORDER BY name;
```

安装扩展：

```sql
CREATE EXTENSION IF NOT EXISTS pg_trgm;
```

删除扩展：

```sql
DROP EXTENSION pg_trgm;
```

`pg_trgm` 常用于 `ILIKE '%关键词%'` 这类模糊搜索加速：

```sql
CREATE INDEX idx_users_name_trgm
ON users USING gin (name gin_trgm_ops);
```

## 10. 常用查询

查看版本：

```sql
SELECT version();
```

查看当前数据库和用户：

```sql
SELECT current_database(), current_user;
```

查看当前时间：

```sql
SELECT now();
```

查看表行数估算：

```sql
SELECT relname, reltuples::bigint AS estimated_rows
FROM pg_class
WHERE relkind = 'r'
ORDER BY estimated_rows DESC;
```

查看某表准确行数：

```sql
SELECT count(*) FROM users;
```

查看最近 50 行：

```sql
SELECT *
FROM users
ORDER BY id DESC
LIMIT 50;
```

模糊查询：

```sql
SELECT *
FROM users
WHERE name ILIKE '%关键词%'
LIMIT 50;
```

查看执行计划：

```sql
EXPLAIN ANALYZE
SELECT *
FROM users
WHERE name ILIKE '%关键词%';
```

## 11. 权限授权

允许用户连接数据库：

```sql
GRANT CONNECT ON DATABASE weibo_data TO weibo_user;
```

允许使用 Schema：

```sql
GRANT USAGE ON SCHEMA public TO weibo_user;
```

允许在 Schema 中创建对象：

```sql
GRANT CREATE ON SCHEMA public TO weibo_user;
```

授权已有表：

```sql
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO weibo_user;
```

授权已有序列：

```sql
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO weibo_user;
```

如果由另一个迁移角色 `migration_owner` 创建对象，希望 `weibo_user` 自动获得未来新表权限，可执行：

```sql
ALTER DEFAULT PRIVILEGES FOR ROLE migration_owner IN SCHEMA public
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO weibo_user;
```

未来新序列同理：

```sql
ALTER DEFAULT PRIVILEGES FOR ROLE migration_owner IN SCHEMA public
GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO weibo_user;
```

`ALTER DEFAULT PRIVILEGES` 只影响指定创建者之后新建的对象，并应由该创建者或超级用户执行。本项目通常直接用 `weibo_user` 运行迁移；它是自己创建对象的 owner，不需要再给自己授予默认权限。

撤销权限：

```sql
REVOKE CREATE ON SCHEMA public FROM weibo_user;
```

## 12. 连接和进程排查

查看 PostgreSQL 是否监听端口：

```bash
ss -lntp | grep 5432
```

检查服务是否可连接：

```bash
pg_isready -h localhost -p 5432
```

查看 PostgreSQL 进程：

```bash
ps -ef | grep '[p]ostgres'
```

查看指定进程启动参数：

```bash
ps -p 2063 -o args=
```

查看当前数据库连接：

```sql
SELECT pid, usename, datname, client_addr, state, query_start, query
FROM pg_stat_activity
ORDER BY query_start NULLS LAST;
```

终止某个连接：

```sql
SELECT pg_terminate_backend(进程PID);
```

查看长事务：

```sql
SELECT pid, usename, datname, now() - xact_start AS duration, state, query
FROM pg_stat_activity
WHERE xact_start IS NOT NULL
ORDER BY duration DESC;
```

## 13. 备份和恢复

备份为自定义格式：

```bash
pg_dump -h localhost -p 5432 -U weibo_user -d weibo_data -Fc -f weibo_data.dump
```

恢复自定义格式：

```bash
pg_restore -h localhost -p 5432 -U weibo_user -d weibo_data --clean --if-exists weibo_data.dump
```

导出纯 SQL：

```bash
pg_dump -h localhost -p 5432 -U weibo_user -d weibo_data -f weibo_data.sql
```

执行 SQL 文件：

```bash
psql -h localhost -p 5432 -U weibo_user -d weibo_data -f weibo_data.sql
```

只备份某张表：

```bash
pg_dump -h localhost -p 5432 -U weibo_user -d weibo_data -t weibo_messages -f weibo_messages.sql
```

备份所有数据库和角色：

```bash
pg_dumpall -h localhost -p 5432 -U postgres -f all.sql
```

## 14. 手动安装常见操作

如果 PostgreSQL 是手动安装在 `~/postgresql`，常见目录可能是：

```text
~/postgresql/bin
~/postgresql/data
```

启动数据库：

```bash
pg_ctl -D "$HOME/postgresql/data" -l "$HOME/postgresql/logfile" start
```

停止数据库：

```bash
pg_ctl -D "$HOME/postgresql/data" stop
```

重启数据库：

```bash
pg_ctl -D "$HOME/postgresql/data" restart
```

查看状态：

```bash
pg_ctl -D "$HOME/postgresql/data" status
```

初始化数据库集群示例：

```bash
initdb -D "$HOME/postgresql/data" -U "$USER" --encoding=UTF8 --locale=C.UTF-8
```

如果初始化时使用 `-U "$USER"`，数据库管理员角色通常与当前 Linux 用户同名。

## 15. Docker 安装常见操作

启动 PostgreSQL 容器：

```bash
docker run -d \
  --name postgres-server \
  -e POSTGRES_PASSWORD='强密码' \
  -p 5432:5432 \
  -v "$HOME/postgresql/data:/var/lib/postgresql/data" \
  postgres:16
```

进入容器 psql：

```bash
docker exec -it postgres-server psql -U postgres
```

查看容器：

```bash
docker ps -a | grep -i postgres
```

停止容器：

```bash
docker stop postgres-server
```

启动容器：

```bash
docker start postgres-server
```

## 16. 应用连接串

标准格式：

```text
postgresql://用户名:密码@主机:端口/数据库名
```

示例：

```env
DATABASE_URL=postgresql://weibo_user:数据库密码@127.0.0.1:5432/weibo_data
```

如果密码包含 `@`、`#`、`:`、`/`、`?`、`&` 等 URL 特殊字符，需要 URL 编码。

为了减少问题，应用数据库密码建议使用字母、数字、下划线组合。

## 17. 常见问题

### 17.1 sudo -u postgres psql 报 unknown user postgres

说明当前系统没有 `postgres` 这个 Linux 用户。常见原因：

- PostgreSQL 是手动安装到用户目录。
- PostgreSQL 是 Docker 运行。
- 系统用户被删除或不是系统包安装方式。

如果 PostgreSQL 手动安装在用户目录，可使用：

```bash
export PATH="$HOME/postgresql/bin:$PATH"
psql -h localhost -p 5432 -d postgres -U "$(whoami)"
```

### 17.2 sudo psql 报 command not found

说明 `psql` 不在 `sudo` 的 PATH 中，或者 PostgreSQL 客户端没有系统级安装。

使用普通用户 PATH：

```bash
export PATH="$HOME/postgresql/bin:$PATH"
psql -h localhost -p 5432 -d postgres -U "$(whoami)"
```

### 17.3 psql 提示符变成 postgres-#

表示当前 SQL 没有结束，通常是引号、括号或分号没闭合。

取消当前输入：

```text
Ctrl+C
```

然后重新输入命令。

### 17.4 不小心 Ctrl+Z 挂起了 psql

回到 shell 后先恢复：

```bash
fg
```

再按 `Ctrl+C` 取消当前输入，最后用：

```sql
\q
```

退出。

### 17.5 数据库名包含横杠时报语法错误

SQL 标识符和 `psql` 的 `\c` 参数需要双引号：

```sql
\c "weibo-data"
DROP DATABASE "weibo-data";
```

连接串中不需要双引号：

```env
DATABASE_URL=postgresql://weibo_user:密码@127.0.0.1:5432/weibo-data
```

### 17.6 password authentication failed

常见原因：

- 用户名错。
- 密码错。
- 连接的数据库错。
- `pg_hba.conf` 认证方式不匹配。

先用管理员用户检查角色是否存在：

```sql
SELECT rolname, rolcanlogin
FROM pg_roles
WHERE rolname = 'weibo_user';
```

必要时重置密码：

```sql
ALTER ROLE weibo_user WITH PASSWORD '新密码';
```

### 17.7 role does not exist

说明连接时使用的数据库角色不存在。

创建角色：

```sql
CREATE ROLE weibo_user WITH LOGIN PASSWORD '强密码';
```

### 17.8 permission denied for schema public

说明当前用户没有在 `public` schema 创建对象的权限。

用数据库 owner 或超级用户执行：

```sql
GRANT USAGE, CREATE ON SCHEMA public TO weibo_user;
```

如果需要操作已有表：

```sql
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO weibo_user;
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO weibo_user;
```

## 18. weibo-assistant 诊断查询

查看迁移版本：

```sql
SELECT version, name, checksum, applied_at
FROM schema_migrations
ORDER BY version;
```

查看 monitor freshness：

```sql
SELECT status,
       current_url,
       last_message_at,
       last_heartbeat_at,
       now() - last_heartbeat_at AS heartbeat_age,
       last_error
FROM monitor_status
WHERE id = 1;
```

查看最近消息：

```sql
SELECT m.id,
       g.weibo_group_id,
       u.weibo_user_id,
       m.message_type,
       m.content,
       coalesce(m.sent_at, m.captured_at) AS message_at
FROM weibo_messages m
JOIN weibo_groups g ON g.id = m.group_id
LEFT JOIN weibo_users u ON u.id = m.sender_id
ORDER BY coalesce(m.sent_at, m.captured_at) DESC, m.id DESC
LIMIT 50;
```

查看通知积压：

```sql
SELECT status, count(*)
FROM notification_outbox
GROUP BY status
ORDER BY status;
```

查看等待重试的任务：

```sql
SELECT id, rule_id, message_id, attempt_count, max_attempts,
       next_attempt_at, last_error
FROM notification_outbox
WHERE status IN ('pending', 'retry', 'failed')
ORDER BY next_attempt_at, id;
```

查看通知规则：

```sql
SELECT id, name, enabled, weibo_group_id, group_name,
       weibo_sender_id, sender_nickname, message_type, updated_at
FROM notification_rules
ORDER BY updated_at DESC, id DESC;
```

查看 monitor 运行时指标快照：

```sql
SELECT *
FROM monitor_runtime_metrics
WHERE service_name = 'monitor';
```

检查重复去重键；正常应返回 0 行：

```sql
SELECT dedupe_key, count(*)
FROM weibo_messages
GROUP BY dedupe_key
HAVING count(*) > 1;
```

数据库查询可能包含群聊正文。生产排查时避免复制到公开工单或聊天工具，并在使用完成后清理临时导出文件。
