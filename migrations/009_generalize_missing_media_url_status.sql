update weibo_message_attachments
   set meta = jsonb_set(meta, '{url_status}', '"missing_in_message_payload"'::jsonb, true)
 where meta->>'url_status' = 'missing_in_websocket_payload';
