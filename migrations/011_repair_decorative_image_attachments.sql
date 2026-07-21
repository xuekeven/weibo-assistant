with decorative_urls as (
  select message.id as message_id, decorative.url
    from weibo_messages message
   cross join lateral (
     values
       (message.raw->>'role_icon'),
       (message.raw->>'icon'),
       (message.raw->>'badge_icon'),
       (message.raw->>'verified_icon'),
       (message.raw->>'level_icon'),
       (message.raw->>'medal_icon'),
       (message.raw->>'pendant_url'),
       (message.raw->>'frame_icon'),
       (message.raw#>>'{data,info,role_icon}'),
       (message.raw#>>'{data,info,icon}'),
       (message.raw#>>'{data,info,badge_icon}'),
       (message.raw#>>'{data,info,verified_icon}'),
       (message.raw#>>'{data,info,level_icon}'),
       (message.raw#>>'{data,info,medal_icon}'),
       (message.raw#>>'{data,info,pendant_url}'),
       (message.raw#>>'{data,info,frame_icon}'),
       (message.raw#>>'{from_user,profile_image_url}'),
       (message.raw#>>'{from_user,avatar_large}'),
       (message.raw#>>'{from_user,avatar_hd}'),
       (message.raw#>>'{data,info,from_user,profile_image_url}'),
       (message.raw#>>'{data,info,from_user,avatar_large}'),
       (message.raw#>>'{data,info,from_user,avatar_hd}')
   ) as decorative(url)
   where coalesce(decorative.url, '') <> ''
), polluted as (
  select distinct attachment.id
    from weibo_message_attachments attachment
   where attachment.attachment_type = 'image'
     and coalesce(attachment.url, '') <> ''
     and (
       exists (
         select 1
           from decorative_urls decorative
          where decorative.message_id = attachment.message_id
            and decorative.url = attachment.url
       )
       or (
         attachment.url ~ '/msget(_thumbnail)?[?]'
         and coalesce(substring(attachment.url from '[?&]fid=([^&]+)'), '') <> ''
         and not exists (
           select 1
             from jsonb_array_elements_text(
                    case
                      when jsonb_typeof(coalesce(attachment.meta, '{}'::jsonb)->'fids') = 'array'
                      then attachment.meta->'fids'
                      else '[]'::jsonb
                    end
                  ) as fid(value)
            where fid.value = substring(attachment.url from '[?&]fid=([^&]+)')
         )
       )
     )
), cleared as (
  update weibo_message_attachments attachment
     set url = null,
         meta = (coalesce(attachment.meta, '{}'::jsonb) - 'resolved_url' - 'resolved_url_source') ||
                jsonb_build_object('url_status', 'discarded_decorative_or_mismatched_url')
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
