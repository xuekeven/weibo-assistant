package store

import (
	"context"
	"time"
)

func (s *Store) ClaimHistorySyncAlert(ctx context.Context) (HistorySyncJob, error) {
	// Reconcile terminal jobs first, including jobs completed just before a
	// process restart. The primary key prevents duplicate alert deliveries.
	_, err := s.pool.Exec(ctx, `
		insert into history_sync_alerts (job_id)
		select id from history_sync_jobs
		 where mode = 'daily_gap' and status in ('completed', 'failed')
		on conflict do nothing
	`)
	if err != nil {
		return HistorySyncJob{}, err
	}
	var id int64
	err = s.pool.QueryRow(ctx, `
		with candidate as (
		  select job_id from history_sync_alerts
		   where (status = 'pending' and next_attempt_at <= now())
		      or (status = 'sending' and next_attempt_at <= now())
		   order by next_attempt_at, job_id
		   for update skip locked limit 1
		)
		update history_sync_alerts a
		   set status = 'sending', attempts = attempts + 1,
		       next_attempt_at = now() + interval '5 minutes'
		  from candidate where a.job_id = candidate.job_id
		returning a.job_id
	`).Scan(&id)
	if err != nil {
		return HistorySyncJob{}, err
	}
	return s.GetHistorySyncJob(ctx, id)
}

func (s *Store) CompleteHistorySyncAlert(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `update history_sync_alerts set status = 'sent', sent_at = now(), last_error = null where job_id = $1`, id)
	return err
}

func (s *Store) RetryHistorySyncAlert(ctx context.Context, id int64, sendErr error) error {
	_, err := s.pool.Exec(ctx, `
		update history_sync_alerts
		   set status = 'pending', next_attempt_at = now() + interval '1 minute' * least(attempts, 30),
		       last_error = $2
		 where job_id = $1
	`, id, sendErr.Error())
	return err
}

func (s *Store) WaitHistorySyncJob(ctx context.Context, id int64) (HistorySyncJob, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		job, err := s.GetHistorySyncJob(ctx, id)
		if err != nil {
			return HistorySyncJob{}, err
		}
		if job.Status == "completed" || job.Status == "failed" {
			return job, nil
		}
		select {
		case <-ctx.Done():
			return HistorySyncJob{}, ctx.Err()
		case <-ticker.C:
		}
	}
}
