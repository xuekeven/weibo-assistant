package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	// pgxpool.Pool 是并发安全的连接池，一个进程共享一个即可。
	pool        *pgxpool.Pool
	databaseURL string
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool, databaseURL: databaseURL}, nil
}

func (s *Store) Close() {
	if s == nil || s.pool == nil {
		return
	}
	s.pool.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// GetCurrentWeiboUserID returns the account that owns the latest private IM
// channel observed by the monitor. Every realtime group-chat event is delivered
// on /im/<logged-in uid>, while recovered history records may not contain that
// channel, so the latest realtime event is the durable source of truth.
func (s *Store) GetCurrentWeiboUserID(ctx context.Context) (string, error) {
	var channel string
	err := s.pool.QueryRow(ctx, `
		select coalesce((
			select raw->>'channel'
			  from weibo_messages
			 where raw->>'channel' ~ '^/im/[0-9]+$'
			 order by coalesce(sent_at, captured_at) desc, id desc
			 limit 1
		), '')
	`).Scan(&channel)
	if err != nil {
		return "", err
	}
	return currentWeiboUserIDFromChannel(channel), nil
}

func currentWeiboUserIDFromChannel(channel string) string {
	uid := strings.TrimPrefix(strings.TrimSpace(channel), "/im/")
	if uid == "" || len(uid) == len(strings.TrimSpace(channel)) {
		return ""
	}
	for _, char := range uid {
		if char < '0' || char > '9' {
			return ""
		}
	}
	return uid
}

func (s *Store) Exec(ctx context.Context, sql string) error {
	_, err := s.pool.Exec(ctx, sql)
	return err
}

func (s *Store) WithTransaction(ctx context.Context, fn func(pgx.Tx) error) error {
	// defer Rollback 是事务保护：Commit 成功后再次 Rollback 不会产生副作用。
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type MonitorStatus struct {
	Status          string     `json:"status"`
	CurrentURL      string     `json:"currentUrl,omitempty"`
	LastMessageAt   *time.Time `json:"lastMessageAt,omitempty"`
	LastHeartbeatAt *time.Time `json:"lastHeartbeatAt,omitempty"`
	LastError       string     `json:"lastError,omitempty"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

func (s *Store) GetMonitorStatus(ctx context.Context) (MonitorStatus, error) {
	var status MonitorStatus
	err := s.pool.QueryRow(ctx, `
		select status,
		       coalesce(current_url, ''),
		       last_message_at,
		       last_heartbeat_at,
		       coalesce(last_error, ''),
		       updated_at
		  from monitor_status
		 where id = 1
	`).Scan(
		&status.Status,
		&status.CurrentURL,
		&status.LastMessageAt,
		&status.LastHeartbeatAt,
		&status.LastError,
		&status.UpdatedAt,
	)
	return status, err
}

func (s *Store) UpdateMonitorStatus(ctx context.Context, status string, currentURL string, lastErr string) error {
	_, err := s.pool.Exec(ctx, `
		insert into monitor_status (id, status, current_url, last_heartbeat_at, last_error, updated_at)
		values (1, $1, $2, now(), nullif($3, ''), now())
		on conflict (id) do update set
		  status = excluded.status,
		  current_url = excluded.current_url,
		  last_heartbeat_at = excluded.last_heartbeat_at,
		  last_error = excluded.last_error,
		  updated_at = excluded.updated_at
	`, status, currentURL, lastErr)
	if err != nil {
		return err
	}
	return s.NotifyRealtimeEvent(ctx, RealtimeEvent{Type: "monitor_status_updated"})
}

const RealtimeChannel = "weibo_assistant_events"

type RealtimeEvent struct {
	Type                string `json:"type"`
	MessageID           int64  `json:"messageId,omitempty"`
	NotificationEventID int64  `json:"notificationEventId,omitempty"`
	HistorySyncJobID    int64  `json:"historySyncJobId,omitempty"`
}

func (s *Store) NotifyRealtimeEvent(ctx context.Context, event RealtimeEvent) error {
	if s == nil || s.pool == nil || event.Type == "" {
		return nil
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `select pg_notify($1, $2)`, RealtimeChannel, string(payload))
	return err
}

func (s *Store) ListenRealtimeEvents(ctx context.Context, handler func(context.Context, RealtimeEvent)) error {
	conn, err := pgx.Connect(ctx, s.databaseURL)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx, `listen `+RealtimeChannel); err != nil {
		return err
	}
	for {
		notification, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		var event RealtimeEvent
		if err := json.Unmarshal([]byte(notification.Payload), &event); err != nil || event.Type == "" {
			continue
		}
		handler(ctx, event)
	}
}
