-- WebSocket current_version can change when Weibo redelivers the same message.
-- Keep the first row for each stable info.id and merge any attachments before cleanup.
create temporary table duplicate_weibo_messages on commit drop as
with ranked as (
  select id,
         raw #>> '{data,info,id}' as stable_id,
         first_value(id) over (
           partition by raw #>> '{data,info,id}'
           order by captured_at asc, id asc
         ) as keeper_id,
         row_number() over (
           partition by raw #>> '{data,info,id}'
           order by captured_at asc, id asc
         ) as duplicate_rank
    from weibo_messages
   where source = 'weibo_websocket'
     and coalesce(raw #>> '{data,info,id}', '') <> ''
)
select id as duplicate_id, keeper_id, stable_id
  from ranked
 where duplicate_rank > 1;

insert into weibo_message_attachments (
  message_id, attachment_type, url, local_path, meta
)
select duplicate.keeper_id,
       attachment.attachment_type,
       attachment.url,
       attachment.local_path,
       attachment.meta
  from duplicate_weibo_messages duplicate
  join weibo_message_attachments attachment
    on attachment.message_id = duplicate.duplicate_id
 where not exists (
   select 1
     from weibo_message_attachments existing
    where existing.message_id = duplicate.keeper_id
      and existing.attachment_type = attachment.attachment_type
      and existing.url is not distinct from attachment.url
      and existing.local_path is not distinct from attachment.local_path
      and existing.meta is not distinct from attachment.meta
 );

delete from weibo_messages message
 using duplicate_weibo_messages duplicate
 where message.id = duplicate.duplicate_id;

update weibo_messages
   set weibo_message_id = raw #>> '{data,info,id}',
       dedupe_key = 'weibo_message:' || (raw #>> '{data,info,id}')
 where source = 'weibo_websocket'
   and coalesce(raw #>> '{data,info,id}', '') <> '';
