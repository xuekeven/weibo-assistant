package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type MediaResolutionJob struct {
	ID            int64
	MessageID     int64
	AttachmentID  int64
	FID           string
	Status        string
	AttemptCount  int
	MaxAttempts   int
	NextAttemptAt time.Time
	LastError     string
}

// EnqueueMediaResolutionForMessage creates one durable job for every unresolved
// image fid on the message. The partial unique index makes repeated frontend
// renders idempotent while still allowing a failed job to be requested again.
func (s *Store) EnqueueMediaResolutionForMessage(ctx context.Context, messageID int64) (int64, error) {
	command, err := s.pool.Exec(ctx, `
		insert into media_resolution_jobs (message_id, attachment_id, fid)
		select attachment.message_id, attachment.id, btrim(fid.value)
		  from weibo_message_attachments attachment
		 cross join lateral jsonb_array_elements_text(
		       case
		         when jsonb_typeof(coalesce(attachment.meta, '{}'::jsonb)->'fids') = 'array'
		         then attachment.meta->'fids'
		         else '[]'::jsonb
		       end
		 ) as fid(value)
		 where attachment.message_id = $1
		   and attachment.attachment_type = 'image'
		   and coalesce(attachment.url, '') = ''
		   and btrim(fid.value) <> ''
		on conflict (attachment_id, fid) where status in ('pending', 'running') do nothing
	`, messageID)
	if err != nil {
		return 0, err
	}
	return command.RowsAffected(), nil
}

func (s *Store) ClaimMediaResolution(ctx context.Context, staleAfter time.Duration) (MediaResolutionJob, error) {
	if staleAfter <= 0 {
		staleAfter = 2 * time.Minute
	}
	return scanMediaResolutionJob(s.pool.QueryRow(ctx, `
		with candidate as (
		  select id
		    from media_resolution_jobs
		   where (status = 'pending' and next_attempt_at <= now())
		      or (status = 'running' and locked_at < now() - ($1 * interval '1 millisecond'))
		   order by next_attempt_at, created_at, id
		   for update skip locked
		   limit 1
		)
		update media_resolution_jobs job
		   set status = 'running', attempt_count = attempt_count + 1,
		       locked_at = now(), updated_at = now()
		  from candidate
		 where job.id = candidate.id
		returning job.id, job.message_id, job.attachment_id, job.fid, job.status,
		          job.attempt_count, job.max_attempts, job.next_attempt_at,
		          coalesce(job.last_error, '')
	`, staleAfter.Milliseconds()))
}

func (s *Store) CompleteMediaResolution(ctx context.Context, id int64) error {
	command, err := s.pool.Exec(ctx, `
		update media_resolution_jobs
		   set status = 'completed', locked_at = null, completed_at = now(),
		       last_error = null, updated_at = now()
		 where id = $1 and status = 'running'
	`, id)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) FailMediaResolution(ctx context.Context, job MediaResolutionJob, resolutionErr error) error {
	message := "image metadata resolution failed"
	if resolutionErr != nil {
		message = resolutionErr.Error()
	}
	retryDelay := mediaResolutionRetryDelay(job.AttemptCount)
	command, err := s.pool.Exec(ctx, `
		update media_resolution_jobs
		   set status = case when attempt_count >= max_attempts then 'failed' else 'pending' end,
		       next_attempt_at = case
		         when attempt_count >= max_attempts then next_attempt_at
		         else now() + ($3 * interval '1 millisecond')
		       end,
		       last_error = $2, locked_at = null, updated_at = now()
		 where id = $1 and status = 'running'
	`, job.ID, message, retryDelay.Milliseconds())
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}

func mediaResolutionRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Second << min(attempt-1, 9)
	return min(delay, 5*time.Minute)
}

func scanMediaResolutionJob(row rowScanner) (MediaResolutionJob, error) {
	var job MediaResolutionJob
	err := row.Scan(&job.ID, &job.MessageID, &job.AttachmentID, &job.FID, &job.Status,
		&job.AttemptCount, &job.MaxAttempts, &job.NextAttemptAt, &job.LastError)
	return job, err
}

func IsNoMediaResolutionJob(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
