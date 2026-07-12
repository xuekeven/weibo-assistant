package app

import (
	"testing"
	"time"

	"github.com/xuekeven/weibo-assistant/internal/store"
)

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
