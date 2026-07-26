package app

import (
	"context"
	"testing"
)

func TestMonitorLifecycleNotificationsUseExpectedText(t *testing.T) {
	client := &fakeLoginAlertClient{}

	sendMonitorLifecycleNotification(context.Background(), client, true, monitorStartedNotificationText)
	sendMonitorLifecycleNotification(context.Background(), client, true, monitorStoppedNotificationText)

	if len(client.texts) != 2 {
		t.Fatalf("texts = %#v", client.texts)
	}
	if client.texts[0] != "微博群聊monitor已开启" || client.texts[1] != "微博群聊monitor已关闭" {
		t.Fatalf("texts = %#v", client.texts)
	}
}

func TestMonitorLifecycleNotificationsCanBeDisabled(t *testing.T) {
	client := &fakeLoginAlertClient{}
	sendMonitorLifecycleNotification(context.Background(), client, false, monitorStartedNotificationText)
	if len(client.calls) != 0 {
		t.Fatalf("disabled lifecycle notification made calls: %#v", client.calls)
	}
}
