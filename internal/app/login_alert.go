package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"

	"github.com/xuekeven/weibo-assistant/internal/browser"
	"github.com/xuekeven/weibo-assistant/internal/notifier"
)

const loginRequiredNotificationText = "⚠️ 微博登录已失效，请使用微博 App 扫描下一条图片中的二维码完成登录。登录成功后 monitor 会自动恢复群聊监听。"

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
	imageHash [sha256.Size]byte
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
		s.imageHash = sha256.Sum256(event.ScreenshotPNG)
		return nil
	}
	imageHash := sha256.Sum256(event.ScreenshotPNG)
	if imageHash != s.imageHash {
		if _, err := s.client.SendImageBytes(ctx, "weibo-login.png", event.ScreenshotPNG); err != nil {
			return fmt.Errorf("refresh login image notification: %w", err)
		}
		s.imageHash = imageHash
	}
	return nil
}

func (s *loginAlertSender) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.textSent = false
	s.imageSent = false
	s.imageHash = [sha256.Size]byte{}
}
