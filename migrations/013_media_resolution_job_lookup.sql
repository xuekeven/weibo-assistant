create index if not exists idx_media_resolution_jobs_attachment_latest
  on media_resolution_jobs (attachment_id, fid, created_at desc, id desc);
