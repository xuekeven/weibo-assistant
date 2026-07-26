package app

import (
	"context"
	"errors"
	"testing"

	"github.com/xuekeven/weibo-assistant/internal/browser"
	"github.com/xuekeven/weibo-assistant/internal/notifier"
)

type fakeLoginAlertClient struct {
	calls         []string
	imageFailures int
	lastText      string
	lastImageName string
	lastImage     []byte
}

func (c *fakeLoginAlertClient) SendText(_ context.Context, content string) (notifier.SendResponse, error) {
	c.calls = append(c.calls, "text")
	c.lastText = content
	return notifier.SendResponse{ErrCode: 0, ErrMsg: "ok"}, nil
}

func (c *fakeLoginAlertClient) SendImageBytes(_ context.Context, filename string, image []byte) (notifier.SendResponse, error) {
	c.calls = append(c.calls, "image")
	c.lastImageName = filename
	c.lastImage = append([]byte(nil), image...)
	if c.imageFailures > 0 {
		c.imageFailures--
		return notifier.SendResponse{}, errors.New("temporary image failure")
	}
	return notifier.SendResponse{ErrCode: 0, ErrMsg: "ok", MediaID: "media-1"}, nil
}

func TestLoginAlertSendsTextThenImageAndRetriesOnlyFailedImage(t *testing.T) {
	client := &fakeLoginAlertClient{imageFailures: 1}
	sender := newLoginAlertSender(client, true)
	event := browser.LoginRequiredEvent{ScreenshotPNG: []byte("png")}

	if err := sender.Notify(context.Background(), event); err == nil {
		t.Fatal("expected first image send to fail")
	}
	if got := joinLoginAlertCalls(client.calls); got != "text,image" {
		t.Fatalf("first calls = %q", got)
	}

	if err := sender.Notify(context.Background(), event); err != nil {
		t.Fatalf("retry login alert: %v", err)
	}
	if got := joinLoginAlertCalls(client.calls); got != "text,image,image" {
		t.Fatalf("retry calls = %q", got)
	}
	if client.lastText != loginRequiredNotificationText {
		t.Fatalf("text = %q", client.lastText)
	}
	if client.lastImageName != "weibo-login.png" || string(client.lastImage) != "png" {
		t.Fatalf("unexpected image: name=%q bytes=%q", client.lastImageName, string(client.lastImage))
	}
}

func TestLoginAlertResetAllowsFutureLoginEpisode(t *testing.T) {
	client := &fakeLoginAlertClient{}
	sender := newLoginAlertSender(client, true)
	event := browser.LoginRequiredEvent{ScreenshotPNG: []byte("png")}

	if err := sender.Notify(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	sender.Reset()
	if err := sender.Notify(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if got := joinLoginAlertCalls(client.calls); got != "text,image,text,image" {
		t.Fatalf("calls after reset = %q", got)
	}
}

func TestLoginAlertOnlySendsChangedQRCode(t *testing.T) {
	client := &fakeLoginAlertClient{}
	sender := newLoginAlertSender(client, true)

	for _, image := range [][]byte{[]byte("qr-1"), []byte("qr-1"), []byte("qr-2")} {
		if err := sender.Notify(context.Background(), browser.LoginRequiredEvent{ScreenshotPNG: image}); err != nil {
			t.Fatal(err)
		}
	}
	if got := joinLoginAlertCalls(client.calls); got != "text,image,image" {
		t.Fatalf("calls = %q, want changed QR image only", got)
	}
	if string(client.lastImage) != "qr-2" {
		t.Fatalf("last image = %q", string(client.lastImage))
	}
}

func joinLoginAlertCalls(calls []string) string {
	result := ""
	for index, call := range calls {
		if index > 0 {
			result += ","
		}
		result += call
	}
	return result
}
