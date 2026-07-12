alter table notification_rules
  add column if not exists weibo_group_id text,
  add column if not exists group_name text,
  add column if not exists weibo_sender_id text;

create index if not exists idx_notification_rules_weibo_group_id
  on notification_rules (weibo_group_id)
  where weibo_group_id is not null;

create index if not exists idx_notification_rules_weibo_sender_id
  on notification_rules (weibo_sender_id)
  where weibo_sender_id is not null;

create unique index if not exists idx_notification_rules_environment_default
  on notification_rules (name)
  where name = '环境变量默认通知规则';

alter table notification_outbox
  add column if not exists rule_id bigint references notification_rules(id);

alter table notification_outbox
  drop constraint if exists notification_outbox_message_unique;

create unique index if not exists idx_notification_outbox_message_rule_unique
  on notification_outbox (message_id, coalesce(rule_id, 0));

create index if not exists idx_notification_outbox_rule_id
  on notification_outbox (rule_id, id desc);
