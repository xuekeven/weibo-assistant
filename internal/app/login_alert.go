package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/xuekeven/weibo-assistant/internal/browser"
	"github.com/xuekeven/weibo-assistant/internal/notifier"
)

const (
	loginRequiredNotificationText = "⚠️ 微博登录已失效，请使用微博 App 扫描下一条图片中的二维码完成登录。登录成功后 monitor 会自动恢复群聊监听。"
	loginRestoredNotificationText = "✅ 微博登录成功，monitor 正在恢复群聊监听。"
	loginRestoredMaxAttempts      = 5
	loginRestoredInitialRetryGap  = 5 * time.Second
)

type loginAlertClient interface {
	SendText(context.Context, string) (notifier.SendResponse, error)
	SendImageBytes(context.Context, string, []byte) (notifier.SendResponse, error)
}

type loginAlertSender struct {
	mu        sync.Mutex
	client    loginAlertClient
	enabled   bool
	textSent  bool
	imageSent bool
}

func newLoginAlertSender(client loginAlertClient, enabled bool) *loginAlertSender {
	return &loginAlertSender{
		client:  client,
		enabled: enabled,
	}
}

func (s *loginAlertSender) Notify(ctx context.Context, event browser.LoginRequiredEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.enabled || s.client == nil {
		return errors.New("weibo login notification is disabled; configure WEWORK_RELAY_URL and WEWORK_RELAY_API_KEY")
	}
	if s.imageSent {
		return nil
	}
	if !s.textSent {
		if _, err := s.client.SendText(ctx, loginRequiredNotificationText); err != nil {
			return fmt.Errorf("send login text notification: %w", err)
		}
		s.textSent = true
	}
	if !s.imageSent {
		if len(event.ScreenshotPNG) == 0 {
			return errors.New("send login image notification: screenshot is empty")
		}
		if _, err := s.client.SendImageBytes(ctx, "weibo-login.png", event.ScreenshotPNG); err != nil {
			return fmt.Errorf("send login image notification: %w", err)
		}
		s.imageSent = true
		return nil
	}
	return nil
}

func (s *loginAlertSender) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetLocked()
}

func (s *loginAlertSender) NotifyRestored(ctx context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 只有本进程确实发送过二维码，才把页面恢复解释为一次扫码登录成功。
	if !s.enabled || s.client == nil || !s.imageSent {
		return false, nil
	}
	if _, err := s.client.SendText(ctx, loginRestoredNotificationText); err != nil {
		return false, fmt.Errorf("send login restored notification: %w", err)
	}
	s.resetLocked()
	return true, nil
}

func (s *loginAlertSender) resetLocked() {
	s.textSent = false
	s.imageSent = false
}

func sendLoginRestoredNotification(ctx context.Context, sender *loginAlertSender) {
	if sender == nil {
		return
	}
	retryGap := loginRestoredInitialRetryGap
	for attempt := 1; attempt <= loginRestoredMaxAttempts; attempt++ {
		sent, err := sender.NotifyRestored(ctx)
		if err == nil {
			if sent {
				slog.Info("weibo login restored notification sent")
			}
			return
		}
		slog.Warn("send weibo login restored notification failed",
			"attempt", attempt,
			"maxAttempts", loginRestoredMaxAttempts,
			"error", err,
		)
		if attempt == loginRestoredMaxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryGap):
		}
		if retryGap < time.Minute {
			retryGap *= 2
			if retryGap > time.Minute {
				retryGap = time.Minute
			}
		}
	}
	// 避免一次永久失败污染下一次登录事件的文字/二维码发送状态。
	sender.Reset()
}
