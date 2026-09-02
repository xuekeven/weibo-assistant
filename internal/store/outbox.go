package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type EnqueueNotificationParams struct {
	RuleID      *int64
	Content     string
	RelayURL    string
	ToUser      string
	MaxAttempts int
}

type NotificationOutboxJob struct {
	ID             int64
	RuleID         *int64
	MessageID      int64
	Content        string
	RelayURL       string
	ToUser         string
	AttemptCount   int
	MaxAttempts    int
	TextSentAt     *time.Time
	ImageSentCount int
}

type NotificationOutboxItem struct {
	ID            int64      `json:"id"`
	RuleID        *int64     `json:"ruleId,omitempty"`
	MessageID     int64      `json:"messageId"`
	Content       string     `json:"content"`
	ToUser        string     `json:"touser"`
	Status        string     `json:"status"`
	AttemptCount  int        `json:"attemptCount"`
	MaxAttempts   int        `json:"maxAttempts"`
	NextAttemptAt time.Time  `json:"nextAttemptAt"`
	LastError     string     `json:"lastError,omitempty"`
	SentAt        *time.Time `json:"sentAt,omitempty"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

func (s *Store) ListNotificationOutbox(ctx context.Context, limit int) ([]NotificationOutboxItem, error) {
	limit = normalizeLimit(limit, 50, 200)
	rows, err := s.pool.Query(ctx, `
		select id, rule_id, message_id, content, touser, status, attempt_count, max_attempts,
		       next_attempt_at, coalesce(last_error, ''), sent_at, updated_at
		  from notification_outbox
		 order by case status when 'failed' then 0 when 'retry' then 1 when 'pending' then 2 else 3 end,
		          updated_at desc
		 limit $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]NotificationOutboxItem, 0)
	for rows.Next() {
		var item NotificationOutboxItem
		if err := rows.Scan(&item.ID, &item.RuleID, &item.MessageID, &item.Content, &item.ToUser,
			&item.Status, &item.AttemptCount,
			&item.MaxAttempts, &item.NextAttemptAt, &item.LastError, &item.SentAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) RetryNotificationOutbox(ctx context.Context, id int64) error {
	commandTag, err := s.pool.Exec(ctx, `
		update notification_outbox
		   set status = 'retry', attempt_count = 0, next_attempt_at = now(),
		       locked_at = null, last_error = null, updated_at = now()
		 where id = $1 and status in ('failed', 'retry')
	`, id)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() != 1 {
		return errors.New("notification job is not retryable")
	}
	return nil
}

func enqueueNotification(ctx context.Context, db messageDB, messageID int64, params EnqueueNotificationParams) (bool, error) {
	// 该函数由消息事务内部调用：消息和通知任务要么一起成功，要么一起回滚。
	content := strings.TrimSpace(params.Content)
	relayURL := strings.TrimRight(strings.TrimSpace(params.RelayURL), "/")
	if messageID <= 0 || content == "" || relayURL == "" {
		return false, errors.New("notification outbox requires message id, content, and relay URL")
	}
	maxAttempts := params.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 8
	}
	commandTag, err := db.Exec(ctx, `
		insert into notification_outbox (
		  rule_id, message_id, content, relay_url, touser, max_attempts
		)
		values ($1, $2, $3, $4, nullif($5, ''), $6)
		on conflict (message_id, (coalesce(rule_id, 0))) do nothing
	`, params.RuleID, messageID, content, relayURL, strings.TrimSpace(params.ToUser), maxAttempts)
	return commandTag.RowsAffected() == 1, err
}

func enqueueMatchingNotifications(ctx context.Context, db messageDB, messageID int64, group Group, user User, messageType string, params EnqueueNotificationParams) (int, error) {
	ruleIDs, err := matchingNotificationRuleIDs(ctx, db, group, user, messageType)
	if err != nil {
		return 0, err
	}
	queued := 0
	for _, ruleID := range ruleIDs {
		params.RuleID = &ruleID
		inserted, err := enqueueNotification(ctx, db, messageID, params)
		if err != nil {
			return 0, err
		}
		if inserted {
			queued++
		}
	}
	return queued, nil
}

func (s *Store) ClaimNotificationOutbox(ctx context.Context, staleAfter time.Duration) (NotificationOutboxJob, error) {
	if staleAfter <= 0 {
		staleAfter = 5 * time.Minute
	}
	// “查询候选任务 + 标记 sending”必须在同一事务内完成，避免两个 worker 领取同一任务。
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return NotificationOutboxJob{}, err
	}
	defer tx.Rollback(ctx)

	var job NotificationOutboxJob
	// SKIP LOCKED 不等待其他 worker 已锁定的行，而是直接寻找下一条任务。
	// sending 超时的任务会被重新领取，用于进程发送途中崩溃后的自动恢复。
	err = tx.QueryRow(ctx, `
		with candidate as (
		  select id
		    from notification_outbox
		   where (
		         status in ('pending', 'retry') and next_attempt_at <= now()
		       ) or (
		         status = 'sending' and locked_at < now() - ($1 * interval '1 millisecond')
		       )
		   order by next_attempt_at asc, id asc
		   for update skip locked
		   limit 1
		)
		update notification_outbox o
		   set status = 'sending',
		       attempt_count = attempt_count + 1,
		       locked_at = now(),
		       updated_at = now()
		  from candidate
		 where o.id = candidate.id
		returning o.id,
		          o.rule_id,
		          o.message_id,
		          o.content,
		          o.relay_url,
		          coalesce(o.touser, ''),
		          o.attempt_count,
		          o.max_attempts,
		          o.text_sent_at,
		          o.image_sent_count
	`, staleAfter.Milliseconds()).Scan(
		&job.ID,
		&job.RuleID,
		&job.MessageID,
		&job.Content,
		&job.RelayURL,
		&job.ToUser,
		&job.AttemptCount,
		&job.MaxAttempts,
		&job.TextSentAt,
		&job.ImageSentCount,
	)
	if err != nil {
		return NotificationOutboxJob{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return NotificationOutboxJob{}, err
	}
	return job, nil
}

func (s *Store) MarkNotificationOutboxSent(ctx context.Context, id int64) error {
	commandTag, err := s.pool.Exec(ctx, `
		update notification_outbox
		   set status = 'sent',
		       sent_at = now(),
		       locked_at = null,
		       last_error = null,
		       updated_at = now()
		 where id = $1 and status = 'sending'
	`, id)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) MarkNotificationOutboxTextSent(ctx context.Context, id int64) error {
	commandTag, err := s.pool.Exec(ctx, `
		update notification_outbox
		   set text_sent_at = coalesce(text_sent_at, now()),
		       updated_at = now()
		 where id = $1 and status = 'sending'
	`, id)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) MarkNotificationOutboxImageSent(ctx context.Context, id int64) error {
	commandTag, err := s.pool.Exec(ctx, `
		update notification_outbox
		   set image_sent_count = image_sent_count + 1,
		       updated_at = now()
		 where id = $1 and status = 'sending'
	`, id)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) MarkNotificationOutboxFailed(ctx context.Context, job NotificationOutboxJob, sendErr error, nextAttemptAt time.Time) (bool, error) {
	finalFailure := job.AttemptCount >= job.MaxAttempts
	status := "retry"
	if finalFailure {
		status = "failed"
	}
	errorMessage := "notification delivery failed"
	if sendErr != nil {
		errorMessage = sendErr.Error()
	}
	commandTag, err := s.pool.Exec(ctx, `
		update notification_outbox
		   set status = $2,
		       next_attempt_at = $3,
		       locked_at = null,
		       last_error = $4,
		       updated_at = now()
		 where id = $1 and status = 'sending'
	`, job.ID, status, nextAttemptAt, errorMessage)
	if err != nil {
		return finalFailure, err
	}
	if commandTag.RowsAffected() != 1 {
		return finalFailure, pgx.ErrNoRows
	}
	return finalFailure, nil
}
