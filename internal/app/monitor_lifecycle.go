package app

import (
	"context"
	"log/slog"

	"github.com/xuekeven/weibo-assistant/internal/notifier"
)

const (
	monitorStartedNotificationText = "微博群聊monitor已开启"
	monitorStoppedNotificationText = "微博群聊monitor已关闭"
)

type textNotificationClient interface {
	SendText(context.Context, string) (notifier.SendResponse, error)
}

func sendMonitorLifecycleNotification(ctx context.Context, client textNotificationClient, enabled bool, content string) {
	if !enabled || client == nil {
		return
	}
	if _, err := client.SendText(ctx, content); err != nil {
		slog.Warn("send monitor lifecycle notification failed", "content", content, "error", err)
		return
	}
	slog.Info("monitor lifecycle notification sent", "content", content)
}
