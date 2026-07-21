create table if not exists media_resolution_jobs (
  id bigserial primary key,
  message_id bigint not null references weibo_messages(id) on delete cascade,
  attachment_id bigint not null references weibo_message_attachments(id) on delete cascade,
  fid text not null,
  status text not null default 'pending',
  attempt_count integer not null default 0,
  max_attempts integer not null default 5,
  next_attempt_at timestamptz not null default now(),
  last_error text,
  locked_at timestamptz,
  completed_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  constraint media_resolution_jobs_status_check
    check (status in ('pending', 'running', 'completed', 'failed')),
  constraint media_resolution_jobs_attempts_check
    check (attempt_count >= 0 and max_attempts > 0),
  constraint media_resolution_jobs_fid_check
    check (btrim(fid) <> '')
);

create unique index if not exists idx_media_resolution_jobs_one_active
  on media_resolution_jobs (attachment_id, fid)
  where status in ('pending', 'running');

create index if not exists idx_media_resolution_jobs_claim
  on media_resolution_jobs (next_attempt_at, created_at, id)
  where status in ('pending', 'running');

create index if not exists idx_media_resolution_jobs_message
  on media_resolution_jobs (message_id, created_at desc, id desc);
