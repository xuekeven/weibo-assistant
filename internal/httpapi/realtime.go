package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/xuekeven/weibo-assistant/internal/store"
)

type websocketEvent struct {
	Type              string                   `json:"type"`
	Message           *store.Message           `json:"message,omitempty"`
	MonitorStatus     *monitorStatusView       `json:"monitorStatus,omitempty"`
	NotificationEvent *store.NotificationEvent `json:"notificationEvent,omitempty"`
	HistorySyncJob    *store.HistorySyncJob    `json:"historySyncJob,omitempty"`
	SentAt            time.Time                `json:"sentAt"`
}

func (s *Server) RunRealtime(ctx context.Context) error {
	for {
		slog.Info("realtime listener starting")
		err := s.store.ListenRealtimeEvents(ctx, func(eventCtx context.Context, event store.RealtimeEvent) {
			s.handleRealtimeEvent(eventCtx, event)
		})
		if err == nil || errors.Is(err, context.Canceled) {
			return err
		}
		slog.Warn("realtime listener stopped; retrying", "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

func (s *Server) handleRealtimeEvent(ctx context.Context, event store.RealtimeEvent) {
	slog.Info("realtime event received", "type", event.Type, "messageID", event.MessageID, "notificationEventID", event.NotificationEventID)
	switch event.Type {
	case "message_created", "message_updated":
		if event.MessageID <= 0 {
			return
		}
		message, err := s.store.GetMessageByID(ctx, event.MessageID, false)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				slog.Warn("load websocket message event failed", "messageID", event.MessageID, "error", err)
			}
			return
		}
		s.wsHub.broadcast(websocketEvent{
			Type:    event.Type,
			Message: &message,
			SentAt:  time.Now(),
		})
	case "monitor_status_updated":
		status, err := s.store.GetMonitorStatus(ctx)
		if err != nil {
			slog.Warn("load websocket monitor status failed", "error", err)
			return
		}
		view := s.monitorStatusView(status)
		s.wsHub.broadcast(websocketEvent{
			Type:          event.Type,
			MonitorStatus: &view,
			SentAt:        time.Now(),
		})
	case "notification_event_created":
		if event.NotificationEventID <= 0 {
			return
		}
		notificationEvent, err := s.store.GetNotificationEventByID(ctx, event.NotificationEventID)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				slog.Warn("load websocket notification event failed", "eventID", event.NotificationEventID, "error", err)
			}
			return
		}
		s.wsHub.broadcast(websocketEvent{
			Type:              event.Type,
			NotificationEvent: &notificationEvent,
			SentAt:            time.Now(),
		})
	case "history_sync_updated":
		if event.HistorySyncJobID <= 0 {
			return
		}
		job, err := s.store.GetHistorySyncJob(ctx, event.HistorySyncJobID)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				slog.Warn("load history sync event failed", "jobID", event.HistorySyncJobID, "error", err)
			}
			return
		}
		s.wsHub.broadcast(websocketEvent{
			Type:           event.Type,
			HistorySyncJob: &job,
			SentAt:         time.Now(),
		})
	default:
		s.wsHub.broadcast(websocketEvent{
			Type:   event.Type,
			SentAt: time.Now(),
		})
	}
}
