create table if not exists weibo_groups (
  id bigserial primary key,
  weibo_group_id text unique,
  name text not null,
  avatar_url text,
  last_message_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create table if not exists weibo_users (
  id bigserial primary key,
  weibo_user_id text unique,
  nickname text not null,
  avatar_url text,
  profile_url text,
  raw jsonb,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create index if not exists idx_weibo_users_nickname
  on weibo_users (nickname);

create index if not exists idx_weibo_users_weibo_user_id
  on weibo_users (weibo_user_id);

create table if not exists weibo_messages (
  id bigserial primary key,
  weibo_message_id text,
  group_id bigint not null references weibo_groups(id),
  sender_id bigint references weibo_users(id),
  message_type text not null default 'text',
  content text,
  sent_at timestamptz,
  captured_at timestamptz not null default now(),
  source text not null,
  dedupe_key text not null unique,
  raw jsonb,
  created_at timestamptz not null default now()
);

create index if not exists idx_weibo_messages_group_sent_at
  on weibo_messages (group_id, sent_at desc, id desc);

create index if not exists idx_weibo_messages_sender_sent_at
  on weibo_messages (sender_id, sent_at desc, id desc);

create index if not exists idx_weibo_messages_timeline
  on weibo_messages (coalesce(sent_at, captured_at) desc, id desc);

create table if not exists weibo_message_attachments (
  id bigserial primary key,
  message_id bigint not null references weibo_messages(id) on delete cascade,
  attachment_type text not null,
  url text,
  local_path text,
  meta jsonb,
  created_at timestamptz not null default now()
);

create table if not exists notification_rules (
  id bigserial primary key,
  name text not null,
  group_id bigint references weibo_groups(id),
  sender_id bigint references weibo_users(id),
  sender_nickname text,
  message_type text,
  enabled boolean not null default true,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create index if not exists idx_notification_rules_enabled
  on notification_rules (enabled, updated_at desc, id desc);

create table if not exists notification_events (
  id bigserial primary key,
  rule_id bigint references notification_rules(id),
  message_id bigint not null references weibo_messages(id) on delete cascade,
  status text not null,
  relay_url text,
  touser text,
  request_payload jsonb,
  response_payload jsonb,
  error_message text,
  created_at timestamptz not null default now()
);

create index if not exists idx_notification_events_rule_id
  on notification_events (rule_id, id desc);

create index if not exists idx_notification_events_message_id
  on notification_events (message_id, id desc);

create table if not exists monitor_status (
  id integer primary key default 1,
  status text not null,
  current_url text,
  last_message_at timestamptz,
  last_heartbeat_at timestamptz,
  last_error text,
  raw jsonb,
  updated_at timestamptz not null default now(),
  constraint monitor_status_singleton check (id = 1)
);

insert into monitor_status (id, status)
values (1, 'initialized')
on conflict (id) do nothing;
