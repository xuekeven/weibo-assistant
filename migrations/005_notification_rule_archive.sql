alter table notification_rules
  add column if not exists archived_at timestamptz;

create index if not exists idx_notification_rules_active
  on notification_rules (updated_at desc, id desc)
  where archived_at is null;
