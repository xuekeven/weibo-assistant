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
		slog.Info("实时消息服务已启动，正在等待新消息")
		err := s.store.ListenRealtimeEvents(ctx, func(eventCtx context.Context, event store.RealtimeEvent) {
			s.handleRealtimeEvent(eventCtx, event)
		})
		if err == nil || errors.Is(err, context.Canceled) {
			return err
		}
		slog.Warn("实时消息服务已断开，3 秒后重新连接", "错误", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

func (s *Server) handleRealtimeEvent(ctx context.Context, event store.RealtimeEvent) {
	switch event.Type {
	case "message_created", "message_updated":
		if event.Type == "message_created" {
			slog.Info("收到新消息，准备推送到网页", "消息编号", event.MessageID)
		} else {
			slog.Info("消息内容已更新，准备推送到网页", "消息编号", event.MessageID)
		}
		if event.MessageID <= 0 {
			return
		}
		message, err := s.store.GetMessageByID(ctx, event.MessageID, false)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				slog.Warn("读取待推送消息失败", "消息编号", event.MessageID, "错误", err)
			}
			return
		}
		s.wsHub.broadcast(websocketEvent{
			Type:    event.Type,
			Message: &message,
			SentAt:  time.Now(),
		})
	case "monitor_status_updated":
		slog.Info("采集状态已更新，准备推送到网页")
		status, err := s.store.GetMonitorStatus(ctx)
		if err != nil {
			slog.Warn("读取采集状态失败", "错误", err)
			return
		}
		view := s.monitorStatusView(status)
		s.wsHub.broadcast(websocketEvent{
			Type:          event.Type,
			MonitorStatus: &view,
			SentAt:        time.Now(),
		})
	case "notification_event_created":
		slog.Info("产生新的通知记录，准备推送到网页", "通知编号", event.NotificationEventID)
		if event.NotificationEventID <= 0 {
			return
		}
		notificationEvent, err := s.store.GetNotificationEventByID(ctx, event.NotificationEventID)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				slog.Warn("读取通知记录失败", "通知编号", event.NotificationEventID, "错误", err)
			}
			return
		}
		s.wsHub.broadcast(websocketEvent{
			Type:              event.Type,
			NotificationEvent: &notificationEvent,
			SentAt:            time.Now(),
		})
	case "history_sync_updated":
		slog.Info("历史消息同步进度已更新，准备推送到网页", "同步任务编号", event.HistorySyncJobID)
		if event.HistorySyncJobID <= 0 {
			return
		}
		job, err := s.store.GetHistorySyncJob(ctx, event.HistorySyncJobID)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				slog.Warn("读取历史消息同步进度失败", "同步任务编号", event.HistorySyncJobID, "错误", err)
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
