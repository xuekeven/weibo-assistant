package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/xuekeven/weibo-assistant/internal/systemmetrics"
)

type cpuSnapshotReader func(context.Context) systemmetrics.CPUSnapshot

func checkCPUHealthBeforePageReload(
	ctx context.Context,
	read cpuSnapshotReader,
	client textNotificationClient,
	weworkEnabled bool,
	powerThresholdWatts int,
	temperatureThresholdCelsius int,
) error {
	snapshot := read(ctx)
	content, abnormal := cpuHealthAlertText(snapshot, powerThresholdWatts, temperatureThresholdCelsius)
	if !abnormal {
		slog.Info("CPU health check before page reload is normal",
			"powerWatts", snapshot.PowerWatts,
			"temperatureCelsius", snapshot.TemperatureCelsius,
		)
		return nil
	}

	slog.Warn("CPU health check before page reload is abnormal", "alert", content)
	if !weworkEnabled || client == nil {
		slog.Warn("CPU health alert not sent because WeCom is disabled")
		return nil
	}
	if _, err := client.SendText(ctx, content); err != nil {
		return fmt.Errorf("send CPU health alert: %w", err)
	}
	return nil
}

func cpuHealthAlertText(snapshot systemmetrics.CPUSnapshot, powerThresholdWatts int, temperatureThresholdCelsius int) (string, bool) {
	var reasons []string
	powerText := fmt.Sprintf("%.2f W", snapshot.PowerWatts)
	if !snapshot.PowerAvailable {
		powerText = "读取失败（" + snapshot.PowerError + "）"
		reasons = append(reasons, "CPU功率读取失败")
	} else if snapshot.PowerWatts >= float64(powerThresholdWatts) {
		reasons = append(reasons, "CPU功率过高")
	}

	temperatureText := fmt.Sprintf("%.1f℃", snapshot.TemperatureCelsius)
	if !snapshot.TemperatureAvailable {
		temperatureText = "读取失败（" + snapshot.TemperatureError + "）"
		reasons = append(reasons, "CPU温度读取失败")
	} else if snapshot.TemperatureCelsius >= float64(temperatureThresholdCelsius) {
		reasons = append(reasons, "CPU温度过高")
	}

	if len(reasons) == 0 {
		return "", false
	}
	return fmt.Sprintf(
		"微博群聊 monitor 页面恢复前检测到 CPU 异常\nCPU Package 功率：%s（告警阈值：%d W）\nCPU Package 温度：%s（告警阈值：%d℃）\n异常项：%s",
		powerText,
		powerThresholdWatts,
		temperatureText,
		temperatureThresholdCelsius,
		strings.Join(reasons, "、"),
	), true
}
