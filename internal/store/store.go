package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
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
	return &Store{pool: pool}, nil
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

func (s *Store) Exec(ctx context.Context, sql string) error {
	_, err := s.pool.Exec(ctx, sql)
	return err
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
	return err
}
