create table if not exists notification_outbox (
  id bigserial primary key,
  message_id bigint not null references weibo_messages(id) on delete cascade,
  content text not null,
  relay_url text not null,
  touser text,
  status text not null default 'pending',
  attempt_count integer not null default 0,
  max_attempts integer not null default 8,
  next_attempt_at timestamptz not null default now(),
  locked_at timestamptz,
  last_error text,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  sent_at timestamptz,
  constraint notification_outbox_message_unique unique (message_id),
  constraint notification_outbox_status_check
    check (status in ('pending', 'sending', 'retry', 'sent', 'failed')),
  constraint notification_outbox_attempts_check
    check (attempt_count >= 0 and max_attempts > 0)
);

create index if not exists idx_notification_outbox_due
  on notification_outbox (next_attempt_at, id)
  where status in ('pending', 'retry');

create index if not exists idx_notification_outbox_stale
  on notification_outbox (locked_at, id)
  where status = 'sending';
