package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/xuekeven/weibo-assistant/internal/browser"
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

func sendRendererCPURecoveredNotification(
	ctx context.Context,
	read cpuSnapshotReader,
	client textNotificationClient,
	weworkEnabled bool,
	event browser.RendererCPUEvent,
	requiredHealthyChecks int,
) error {
	if !weworkEnabled || client == nil {
		return nil
	}
	snapshot := read(ctx)
	content := fmt.Sprintf(
		"微博群聊 monitor 已自动恢复\n处理方式：已重载微博聊天页面\nChrome renderer CPU：%.1f%%（告警阈值：%d%%）\nCPU Package 功率：%s\nCPU Package 温度：%s\n恢复判定：连续 %d 次采样正常",
		event.CPUPercent,
		event.LimitPercent,
		cpuPowerText(snapshot),
		cpuTemperatureText(snapshot),
		requiredHealthyChecks,
	)
	if _, err := client.SendText(ctx, content); err != nil {
		return fmt.Errorf("send renderer CPU recovery notification: %w", err)
	}
	return nil
}

func sendRendererCPURestartNotification(ctx context.Context, client textNotificationClient, weworkEnabled bool, event browser.RendererCPUEvent) error {
	if !weworkEnabled || client == nil {
		return nil
	}
	content := fmt.Sprintf(
		"微博群聊 monitor 页面重载后 CPU 仍未恢复\nChrome renderer CPU：%.1f%%（告警阈值：%d%%）\n处理方式：正在重启 monitor 和 Chrome",
		event.CPUPercent,
		event.LimitPercent,
	)
	if _, err := client.SendText(ctx, content); err != nil {
		return fmt.Errorf("send renderer CPU restart notification: %w", err)
	}
	return nil
}

func cpuPowerText(snapshot systemmetrics.CPUSnapshot) string {
	if !snapshot.PowerAvailable {
		return "读取失败（" + snapshot.PowerError + "）"
	}
	return fmt.Sprintf("%.2f W", snapshot.PowerWatts)
}

func cpuTemperatureText(snapshot systemmetrics.CPUSnapshot) string {
	if !snapshot.TemperatureAvailable {
		return "读取失败（" + snapshot.TemperatureError + "）"
	}
	return fmt.Sprintf("%.1f℃", snapshot.TemperatureCelsius)
}
