package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type HistorySyncJob struct {
	ID               int64      `json:"id"`
	GroupID          int64      `json:"groupId"`
	WeiboGroupID     string     `json:"weiboGroupId"`
	GroupName        string     `json:"groupName"`
	Mode             string     `json:"mode"`
	RequestedFrom    *time.Time `json:"requestedFrom,omitempty"`
	RequestedTo      *time.Time `json:"requestedTo,omitempty"`
	CursorMID        string     `json:"cursorMid,omitempty"`
	StopMID          string     `json:"stopMid,omitempty"`
	Status           string     `json:"status"`
	MaxPages         int        `json:"maxPages"`
	PagesFetched     int        `json:"pagesFetched"`
	MessagesSeen     int        `json:"messagesSeen"`
	MessagesInserted int        `json:"messagesInserted"`
	StopReason       string     `json:"stopReason,omitempty"`
	LastError        string     `json:"lastError,omitempty"`
	LockedAt         *time.Time `json:"lockedAt,omitempty"`
	StartedAt        *time.Time `json:"startedAt,omitempty"`
	CompletedAt      *time.Time `json:"completedAt,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

type EnqueueHistorySyncParams struct {
	GroupID       int64
	Mode          string
	RequestedFrom *time.Time
	RequestedTo   *time.Time
	MaxPages      int
	StopMID       string
}

type HistorySyncProgress struct {
	CursorMID        string
	PagesFetched     int
	MessagesSeen     int
	MessagesInserted int
}

func (s *Store) GetGroupByID(ctx context.Context, id int64) (Group, error) {
	var group Group
	err := s.pool.QueryRow(ctx, `
		select id, coalesce(weibo_group_id, ''), name, coalesce(avatar_url, ''),
		       last_message_at, created_at, updated_at
		  from weibo_groups
		 where id = $1
	`, id).Scan(&group.ID, &group.WeiboGroupID, &group.Name, &group.AvatarURL,
		&group.LastMessageAt, &group.CreatedAt, &group.UpdatedAt)
	return group, err
}

func (s *Store) EnqueueHistorySync(ctx context.Context, params EnqueueHistorySyncParams) (HistorySyncJob, error) {
	mode := strings.TrimSpace(params.Mode)
	if mode != "startup" && mode != "recovery" && mode != "manual" {
		return HistorySyncJob{}, errors.New("invalid history sync mode")
	}
	if params.MaxPages <= 0 {
		params.MaxPages = 100
	}
	if params.MaxPages > 1000 {
		params.MaxPages = 1000
	}
	if params.RequestedFrom != nil && params.RequestedTo != nil && params.RequestedFrom.After(*params.RequestedTo) {
		return HistorySyncJob{}, errors.New("history sync start time must not be after end time")
	}

	var id int64
	err := s.pool.QueryRow(ctx, `
		insert into history_sync_jobs (group_id, mode, requested_from, requested_to, max_pages, stop_mid)
		values ($1, $2, $3, $4, $5, nullif($6, ''))
		on conflict do nothing
		returning id
	`, params.GroupID, mode, params.RequestedFrom, params.RequestedTo, params.MaxPages, strings.TrimSpace(params.StopMID)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = s.pool.QueryRow(ctx, `
			select id from history_sync_jobs
			 where group_id = $1 and status in ('pending', 'running')
			 order by id desc limit 1
		`, params.GroupID).Scan(&id)
	}
	if err != nil {
		return HistorySyncJob{}, err
	}
	job, err := s.GetHistorySyncJob(ctx, id)
	if err == nil {
		s.notifyRealtimeBestEffort(ctx, RealtimeEvent{Type: "history_sync_updated", HistorySyncJobID: job.ID})
	}
	return job, err
}

func (s *Store) ClaimHistorySync(ctx context.Context, staleAfter time.Duration) (HistorySyncJob, error) {
	if staleAfter <= 0 {
		staleAfter = 5 * time.Minute
	}
	var id int64
	err := s.pool.QueryRow(ctx, `
		with candidate as (
		  select id from history_sync_jobs
		   where status = 'pending'
		      or (status = 'running' and locked_at < now() - ($1 * interval '1 millisecond'))
		   order by created_at, id
		   for update skip locked
		   limit 1
		)
		update history_sync_jobs j
		   set status = 'running', locked_at = now(), started_at = coalesce(started_at, now()),
		       last_error = null, updated_at = now()
		  from candidate
		 where j.id = candidate.id
		returning j.id
	`, staleAfter.Milliseconds()).Scan(&id)
	if err != nil {
		return HistorySyncJob{}, err
	}
	job, err := s.GetHistorySyncJob(ctx, id)
	if err == nil {
		s.notifyRealtimeBestEffort(ctx, RealtimeEvent{Type: "history_sync_updated", HistorySyncJobID: job.ID})
	}
	return job, err
}

func (s *Store) GetHistorySyncJob(ctx context.Context, id int64) (HistorySyncJob, error) {
	return scanHistorySyncJob(s.pool.QueryRow(ctx, historySyncSelect+` where j.id = $1`, id))
}

func (s *Store) GetLatestHistorySyncForGroup(ctx context.Context, groupID int64) (HistorySyncJob, error) {
	return scanHistorySyncJob(s.pool.QueryRow(ctx, historySyncSelect+`
		where j.group_id = $1 order by j.created_at desc, j.id desc limit 1
	`, groupID))
}

func (s *Store) UpdateHistorySyncProgress(ctx context.Context, id int64, progress HistorySyncProgress) error {
	command, err := s.pool.Exec(ctx, `
		update history_sync_jobs
		   set cursor_mid = nullif($2, ''), pages_fetched = $3, messages_seen = $4,
		       messages_inserted = $5, locked_at = now(), updated_at = now()
		 where id = $1 and status = 'running'
	`, id, progress.CursorMID, progress.PagesFetched, progress.MessagesSeen, progress.MessagesInserted)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return s.NotifyRealtimeEvent(ctx, RealtimeEvent{Type: "history_sync_updated", HistorySyncJobID: id})
}

func (s *Store) CompleteHistorySync(ctx context.Context, id int64, progress HistorySyncProgress, stopReason string) error {
	command, err := s.pool.Exec(ctx, `
		update history_sync_jobs
		   set status = 'completed', cursor_mid = nullif($2, ''), pages_fetched = $3,
		       messages_seen = $4, messages_inserted = $5, stop_reason = nullif($6, ''),
		       locked_at = null, completed_at = now(), updated_at = now()
		 where id = $1 and status = 'running'
	`, id, progress.CursorMID, progress.PagesFetched, progress.MessagesSeen, progress.MessagesInserted, stopReason)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return s.NotifyRealtimeEvent(ctx, RealtimeEvent{Type: "history_sync_updated", HistorySyncJobID: id})
}

func (s *Store) FailHistorySync(ctx context.Context, id int64, syncErr error) error {
	message := "history sync failed"
	if syncErr != nil {
		message = syncErr.Error()
	}
	command, err := s.pool.Exec(ctx, `
		update history_sync_jobs
		   set status = 'failed', last_error = $2, locked_at = null,
		       completed_at = now(), updated_at = now()
		 where id = $1 and status = 'running'
	`, id, message)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return s.NotifyRealtimeEvent(ctx, RealtimeEvent{Type: "history_sync_updated", HistorySyncJobID: id})
}

func (s *Store) MessageExistenceAt(ctx context.Context, weiboMessageID string, snapshot time.Time) (exists bool, existedAtSnapshot bool, err error) {
	var createdAt time.Time
	err = s.pool.QueryRow(ctx, `
		select created_at from weibo_messages where dedupe_key = $1 limit 1
	`, "weibo_message:"+strings.TrimSpace(weiboMessageID)).Scan(&createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return true, !createdAt.After(snapshot), nil
}

func (s *Store) GetOldestMessageMID(ctx context.Context, groupID int64) (string, error) {
	var mid string
	err := s.pool.QueryRow(ctx, `
		select coalesce(weibo_message_id, '')
		  from weibo_messages
		 where group_id = $1 and coalesce(weibo_message_id, '') <> ''
		 order by coalesce(sent_at, captured_at), id
		 limit 1
	`, groupID).Scan(&mid)
	return mid, err
}

func (s *Store) GetNewestMessageMID(ctx context.Context, groupID int64) (string, error) {
	var mid string
	err := s.pool.QueryRow(ctx, `
		select coalesce(weibo_message_id, '')
		  from weibo_messages
		 where group_id = $1 and coalesce(weibo_message_id, '') <> ''
		 order by coalesce(sent_at, captured_at) desc, id desc
		 limit 1
	`, groupID).Scan(&mid)
	return mid, err
}

const historySyncSelect = `
	select j.id, j.group_id, coalesce(g.weibo_group_id, ''), g.name, j.mode,
	       j.requested_from, j.requested_to, coalesce(j.cursor_mid, ''), coalesce(j.stop_mid, ''), j.status,
	       j.max_pages, j.pages_fetched, j.messages_seen, j.messages_inserted,
	       coalesce(j.stop_reason, ''), coalesce(j.last_error, ''), j.locked_at,
	       j.started_at, j.completed_at, j.created_at, j.updated_at
	  from history_sync_jobs j
	  join weibo_groups g on g.id = j.group_id
`

type rowScanner interface {
	Scan(...any) error
}

func scanHistorySyncJob(row rowScanner) (HistorySyncJob, error) {
	var job HistorySyncJob
	err := row.Scan(&job.ID, &job.GroupID, &job.WeiboGroupID, &job.GroupName, &job.Mode,
		&job.RequestedFrom, &job.RequestedTo, &job.CursorMID, &job.StopMID, &job.Status, &job.MaxPages,
		&job.PagesFetched, &job.MessagesSeen, &job.MessagesInserted, &job.StopReason,
		&job.LastError, &job.LockedAt, &job.StartedAt, &job.CompletedAt, &job.CreatedAt, &job.UpdatedAt)
	return job, err
}
