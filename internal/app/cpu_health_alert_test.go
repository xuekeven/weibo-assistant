package app

import (
	"context"
	"strings"
	"testing"

	"github.com/xuekeven/weibo-assistant/internal/browser"
	"github.com/xuekeven/weibo-assistant/internal/systemmetrics"
)

func TestCPUHealthAlertSendsPowerAndTemperatureWhenAbnormal(t *testing.T) {
	client := &fakeLoginAlertClient{}
	read := func(context.Context) systemmetrics.CPUSnapshot {
		return systemmetrics.CPUSnapshot{
			PowerWatts: 36.25, PowerAvailable: true,
			TemperatureCelsius: 80.5, TemperatureAvailable: true,
		}
	}

	if err := checkCPUHealthBeforePageReload(context.Background(), read, client, true, 25, 75); err != nil {
		t.Fatalf("checkCPUHealthBeforePageReload() error = %v", err)
	}
	if len(client.texts) != 1 {
		t.Fatalf("texts = %#v", client.texts)
	}
	for _, expected := range []string{"36.25 W", "80.5℃", "CPU功率过高", "CPU温度过高"} {
		if !strings.Contains(client.texts[0], expected) {
			t.Fatalf("alert %q does not contain %q", client.texts[0], expected)
		}
	}
}

func TestCPUHealthAlertDoesNotSendWhenNormal(t *testing.T) {
	client := &fakeLoginAlertClient{}
	read := func(context.Context) systemmetrics.CPUSnapshot {
		return systemmetrics.CPUSnapshot{
			PowerWatts: 12.75, PowerAvailable: true,
			TemperatureCelsius: 51, TemperatureAvailable: true,
		}
	}

	if err := checkCPUHealthBeforePageReload(context.Background(), read, client, true, 25, 75); err != nil {
		t.Fatalf("checkCPUHealthBeforePageReload() error = %v", err)
	}
	if len(client.texts) != 0 {
		t.Fatalf("normal CPU health sent alerts: %#v", client.texts)
	}
}

func TestCPUHealthAlertReportsUnavailableSensor(t *testing.T) {
	snapshot := systemmetrics.CPUSnapshot{
		PowerError:         "permission denied",
		TemperatureCelsius: 52, TemperatureAvailable: true,
	}
	content, abnormal := cpuHealthAlertText(snapshot, 25, 75)
	if !abnormal || !strings.Contains(content, "CPU功率读取失败") || !strings.Contains(content, "52.0℃") {
		t.Fatalf("unexpected alert: abnormal=%v content=%q", abnormal, content)
	}
}

func TestRendererCPURecoveredNotificationIncludesRecoveryDetails(t *testing.T) {
	client := &fakeLoginAlertClient{}
	read := func(context.Context) systemmetrics.CPUSnapshot {
		return systemmetrics.CPUSnapshot{
			PowerWatts: 12.36, PowerAvailable: true,
			TemperatureCelsius: 59, TemperatureAvailable: true,
		}
	}
	event := browser.RendererCPUEvent{PID: 123, CPUPercent: 0.4, LimitPercent: 50}
	if err := sendRendererCPURecoveredNotification(context.Background(), read, client, true, event, 3); err != nil {
		t.Fatalf("sendRendererCPURecoveredNotification() error = %v", err)
	}
	if len(client.texts) != 1 {
		t.Fatalf("texts = %#v", client.texts)
	}
	for _, expected := range []string{"已自动恢复", "已重载微博聊天页面", "0.4%", "12.36 W", "59.0℃", "连续 3 次"} {
		if !strings.Contains(client.texts[0], expected) {
			t.Fatalf("notification %q does not contain %q", client.texts[0], expected)
		}
	}
}

func TestRendererCPURestartNotificationIncludesEscalation(t *testing.T) {
	client := &fakeLoginAlertClient{}
	event := browser.RendererCPUEvent{PID: 123, CPUPercent: 125.5, LimitPercent: 50}
	if err := sendRendererCPURestartNotification(context.Background(), client, true, event); err != nil {
		t.Fatalf("sendRendererCPURestartNotification() error = %v", err)
	}
	if len(client.texts) != 1 || !strings.Contains(client.texts[0], "正在重启 monitor 和 Chrome") {
		t.Fatalf("texts = %#v", client.texts)
	}
}
