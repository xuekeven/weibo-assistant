alter table history_sync_jobs drop constraint if exists history_sync_jobs_mode_check;
alter table history_sync_jobs add constraint history_sync_jobs_mode_check
  check (mode in ('startup', 'recovery', 'manual', 'daily_gap'));

create table if not exists daily_gap_audits (
  group_id bigint not null references weibo_groups(id) on delete cascade,
  audit_date date not null,
  completed_at timestamptz not null default now(),
  primary key (group_id, audit_date)
);

create table if not exists history_sync_alerts (
  job_id bigint primary key references history_sync_jobs(id) on delete cascade,
  status text not null default 'pending',
  attempts integer not null default 0,
  next_attempt_at timestamptz not null default now(),
  sent_at timestamptz,
  last_error text,
  constraint history_sync_alerts_status_check check (status in ('pending', 'sending', 'sent'))
);

create index if not exists idx_history_sync_alerts_due
  on history_sync_alerts (next_attempt_at, job_id) where status <> 'sent';

create index if not exists idx_weibo_messages_gap_hours
  on weibo_messages (group_id, coalesce(sent_at, captured_at))
  where message_type <> 'event';
