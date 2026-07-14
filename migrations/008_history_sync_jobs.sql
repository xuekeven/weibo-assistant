create table if not exists history_sync_jobs (
  id bigserial primary key,
  group_id bigint not null references weibo_groups(id) on delete cascade,
  mode text not null,
  requested_from timestamptz,
  requested_to timestamptz,
  cursor_mid text,
  stop_mid text,
  status text not null default 'pending',
  max_pages integer not null default 100,
  pages_fetched integer not null default 0,
  messages_seen integer not null default 0,
  messages_inserted integer not null default 0,
  stop_reason text,
  last_error text,
  locked_at timestamptz,
  started_at timestamptz,
  completed_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  constraint history_sync_jobs_mode_check
    check (mode in ('startup', 'recovery', 'manual')),
  constraint history_sync_jobs_status_check
    check (status in ('pending', 'running', 'completed', 'failed')),
  constraint history_sync_jobs_max_pages_check
    check (max_pages > 0 and max_pages <= 1000),
  constraint history_sync_jobs_time_range_check
    check (requested_from is null or requested_to is null or requested_from <= requested_to)
);

create unique index if not exists idx_history_sync_jobs_one_active_per_group
  on history_sync_jobs (group_id)
  where status in ('pending', 'running');

create index if not exists idx_history_sync_jobs_claim
  on history_sync_jobs (created_at, id)
  where status in ('pending', 'running');

create index if not exists idx_history_sync_jobs_group_created
  on history_sync_jobs (group_id, created_at desc, id desc);
