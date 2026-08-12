package app

import (
	"context"
	"strings"
	"testing"

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
