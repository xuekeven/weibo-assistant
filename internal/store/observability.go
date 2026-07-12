package store

import (
	"context"
	"time"
)

type RuntimeMetrics struct {
	ServiceName              string
	BrowserRunning           bool
	CDPConnected             bool
	CDPReconnectTotal        int64
	MessagesParsedTotal      int64
	MessagesParsedLastMinute int64
	PersistSuccessTotal      int64
	PersistFailureTotal      int64
	DedupeTotal              int64
	NotificationSentTotal    int64
	NotificationRetryTotal   int64
	NotificationFailedTotal  int64
	TrackedRequests          int64
	ImageCacheItems          int64
	AllocBytes               int64
	Goroutines               int64
	ProcessStartedAt         time.Time
	UpdatedAt                time.Time
}

type NotificationBacklog struct {
	Pending int64 `json:"pending"`
	Sending int64 `json:"sending"`
	Failed  int64 `json:"failed"`
}

func (s *Store) UpsertRuntimeMetrics(ctx context.Context, metrics RuntimeMetrics) error {
	_, err := s.pool.Exec(ctx, `
		insert into monitor_runtime_metrics (
		  service_name, browser_running, cdp_connected, cdp_reconnect_total,
		  messages_parsed_total, messages_parsed_last_minute,
		  persist_success_total, persist_failure_total, dedupe_total,
		  notification_sent_total, notification_retry_total, notification_failed_total,
		  tracked_requests, image_cache_items, alloc_bytes, goroutines,
		  process_started_at, updated_at
		)
		values (
		  $1, $2, $3, $4, $5, $6, $7, $8, $9,
		  $10, $11, $12, $13, $14, $15, $16, $17, now()
		)
		on conflict (service_name) do update set
		  browser_running = excluded.browser_running,
		  cdp_connected = excluded.cdp_connected,
		  cdp_reconnect_total = excluded.cdp_reconnect_total,
		  messages_parsed_total = excluded.messages_parsed_total,
		  messages_parsed_last_minute = excluded.messages_parsed_last_minute,
		  persist_success_total = excluded.persist_success_total,
		  persist_failure_total = excluded.persist_failure_total,
		  dedupe_total = excluded.dedupe_total,
		  notification_sent_total = excluded.notification_sent_total,
		  notification_retry_total = excluded.notification_retry_total,
		  notification_failed_total = excluded.notification_failed_total,
		  tracked_requests = excluded.tracked_requests,
		  image_cache_items = excluded.image_cache_items,
		  alloc_bytes = excluded.alloc_bytes,
		  goroutines = excluded.goroutines,
		  process_started_at = excluded.process_started_at,
		  updated_at = excluded.updated_at
	`, metrics.ServiceName, metrics.BrowserRunning, metrics.CDPConnected, metrics.CDPReconnectTotal,
		metrics.MessagesParsedTotal, metrics.MessagesParsedLastMinute,
		metrics.PersistSuccessTotal, metrics.PersistFailureTotal, metrics.DedupeTotal,
		metrics.NotificationSentTotal, metrics.NotificationRetryTotal, metrics.NotificationFailedTotal,
		metrics.TrackedRequests, metrics.ImageCacheItems, metrics.AllocBytes, metrics.Goroutines,
		metrics.ProcessStartedAt)
	return err
}

func (s *Store) GetRuntimeMetrics(ctx context.Context, serviceName string) (RuntimeMetrics, error) {
	var metrics RuntimeMetrics
	err := s.pool.QueryRow(ctx, `
		select service_name, browser_running, cdp_connected, cdp_reconnect_total,
		       messages_parsed_total, messages_parsed_last_minute,
		       persist_success_total, persist_failure_total, dedupe_total,
		       notification_sent_total, notification_retry_total, notification_failed_total,
		       tracked_requests, image_cache_items, alloc_bytes, goroutines,
		       process_started_at, updated_at
		  from monitor_runtime_metrics
		 where service_name = $1
	`, serviceName).Scan(
		&metrics.ServiceName, &metrics.BrowserRunning, &metrics.CDPConnected, &metrics.CDPReconnectTotal,
		&metrics.MessagesParsedTotal, &metrics.MessagesParsedLastMinute,
		&metrics.PersistSuccessTotal, &metrics.PersistFailureTotal, &metrics.DedupeTotal,
		&metrics.NotificationSentTotal, &metrics.NotificationRetryTotal, &metrics.NotificationFailedTotal,
		&metrics.TrackedRequests, &metrics.ImageCacheItems, &metrics.AllocBytes, &metrics.Goroutines,
		&metrics.ProcessStartedAt, &metrics.UpdatedAt,
	)
	return metrics, err
}

func (s *Store) GetNotificationBacklog(ctx context.Context) (NotificationBacklog, error) {
	var backlog NotificationBacklog
	err := s.pool.QueryRow(ctx, `
		select count(*) filter (where status in ('pending', 'retry')),
		       count(*) filter (where status = 'sending'),
		       count(*) filter (where status = 'failed')
		  from notification_outbox
	`).Scan(&backlog.Pending, &backlog.Sending, &backlog.Failed)
	return backlog, err
}

func (s *Store) MarkMonitorMessage(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		update monitor_status
		   set last_message_at = now(), updated_at = now()
		 where id = 1
	`)
	return err
}
