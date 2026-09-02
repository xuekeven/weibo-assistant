alter table notification_outbox
  add column if not exists text_sent_at timestamptz,
  add column if not exists image_sent_count integer not null default 0;

alter table notification_outbox
  drop constraint if exists notification_outbox_image_sent_count_check;

alter table notification_outbox
  add constraint notification_outbox_image_sent_count_check
  check (image_sent_count >= 0);
