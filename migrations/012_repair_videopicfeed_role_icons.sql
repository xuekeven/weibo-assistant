with polluted as (
  select attachment.id
    from weibo_message_attachments attachment
   where attachment.attachment_type = 'image'
     and lower(split_part(coalesce(attachment.url, ''), '?', 1)) ~ '/videopicfeed[^/]*[.]png$'
), cleared as (
  update weibo_message_attachments attachment
     set url = null,
         meta = (coalesce(attachment.meta, '{}'::jsonb) - 'resolved_url' - 'resolved_url_source') ||
                jsonb_build_object('url_status', 'discarded_decorative_role_icon')
    from polluted
   where attachment.id = polluted.id
  returning attachment.id, attachment.message_id, attachment.meta
)
insert into media_resolution_jobs (message_id, attachment_id, fid)
select cleared.message_id, cleared.id, btrim(fid.value)
  from cleared
 cross join lateral jsonb_array_elements_text(
       case
         when jsonb_typeof(coalesce(cleared.meta, '{}'::jsonb)->'fids') = 'array'
         then cleared.meta->'fids'
         else '[]'::jsonb
       end
 ) as fid(value)
 where btrim(fid.value) <> ''
on conflict (attachment_id, fid) where status in ('pending', 'running') do nothing;
