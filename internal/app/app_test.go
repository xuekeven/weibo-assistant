package app

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/xuekeven/weibo-assistant/internal/browser"
	"github.com/xuekeven/weibo-assistant/internal/config"
	"github.com/xuekeven/weibo-assistant/internal/store"
)

func TestArchiveOnlyEventDoesNotCreateNotification(t *testing.T) {
	notification, matched := notificationForNetworkMessage(config.Config{}, browser.NetworkMessage{
		MessageType: "event",
		SenderID:    "1401527553",
	})
	if notification != nil || matched {
		t.Fatalf("archive-only event must not create notification: notification=%#v matched=%v", notification, matched)
	}
}

func TestSystemMessageDoesNotMatchTargetSender(t *testing.T) {
	notification, matched := notificationForNetworkMessage(config.Config{
		TargetSenderID:          "1401527553",
		WeworkRelayURL:          "https://relay.example.test/send",
		WeworkRelayAPIKey:       "test-key",
		WeworkRelayToUser:       "XueKai",
		NotificationMaxAttempts: 3,
	}, browser.NetworkMessage{
		MessageType: "system",
		SenderID:    "1401527553",
		SenderName:  "tombkeeper",
		Content:     "关耳儿贝 加入了群",
	})
	if notification == nil {
		t.Fatal("system messages should still be available for explicit system/group notification rules")
	}
	if matched {
		t.Fatal("system message must not match target sender notification")
	}
}

func TestBuildTargetNotificationContent(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	sentAt := time.Date(2026, 7, 9, 18, 25, 0, 0, location)

	content := buildTargetNotificationContent(store.Message{
		WeiboSenderID: "1401527553",
		SenderName:    "tombkeeper",
		MessageType:   "text",
		Content:       "半导体跌到底了",
		SentAt:        &sentAt,
	})

	want := "@tombkeeper 在7月9日18点25分在群里发消息：\n半导体跌到底了"
	if content != want {
		t.Fatalf("notification content mismatch\nwant: %q\n got: %q", want, content)
	}
}

func TestBuildTargetNotificationContentFallbacks(t *testing.T) {
	capturedAt := time.Date(2026, 7, 9, 8, 5, 0, 0, time.FixedZone("CST", 8*60*60))

	content := buildTargetNotificationContent(store.Message{
		WeiboSenderID: "1401527553",
		MessageType:   "image",
		CapturedAt:    capturedAt,
	})

	want := "@1401527553 在7月9日08点05分在群里发消息：\n[image]"
	if content != want {
		t.Fatalf("notification content mismatch\nwant: %q\n got: %q", want, content)
	}
}

func TestNotificationRetryDelay(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 1, want: 5 * time.Second},
		{attempt: 2, want: 10 * time.Second},
		{attempt: 5, want: 80 * time.Second},
		{attempt: 20, want: 15 * time.Minute},
	}
	for _, tt := range tests {
		if got := notificationRetryDelay(tt.attempt); got != tt.want {
			t.Fatalf("notificationRetryDelay(%d) = %s, want %s", tt.attempt, got, tt.want)
		}
	}
}

func TestEnrichNetworkAttachmentsWithImageCacheHandlesEveryImage(t *testing.T) {
	cache := newImageURLCache(time.Hour, 10)
	cache.Set("fid-1", "https://example.test/image-1")
	cache.Set("fid-2", "https://example.test/image-2")
	attachments := []browser.NetworkAttachment{
		{AttachmentType: "image", Meta: json.RawMessage(`{"fids":["fid-1"]}`)},
		{AttachmentType: "image", Meta: json.RawMessage(`{"fids":["fid-2"]}`)},
	}

	enrichNetworkAttachmentsWithImageCache(attachments, cache)

	if attachments[0].URL != "https://example.test/image-1" {
		t.Fatalf("first URL = %q", attachments[0].URL)
	}
	if attachments[1].URL != "https://example.test/image-2" {
		t.Fatalf("second URL = %q", attachments[1].URL)
	}
}
