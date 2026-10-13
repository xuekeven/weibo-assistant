package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/xuekeven/weibo-assistant/internal/notifier"
	"github.com/xuekeven/weibo-assistant/internal/store"
)

type gapWindow struct{ from, to time.Time }

func shanghaiLocation() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*60*60)
	}
	return location
}

func dailyAuditBounds(now time.Time) (string, time.Time, time.Time, bool) {
	local := now.In(shanghaiLocation())
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	if local.Before(day.Add(3 * time.Hour)) {
		return "", time.Time{}, time.Time{}, false
	}
	return day.Format("2006-01-02"), day.AddDate(0, 0, -1).Add(7 * time.Hour), day.Add(3 * time.Hour), true
}

func missingHourWindows(from, to time.Time, present map[string]bool) []gapWindow {
	var windows []gapWindow
	for hour := from; hour.Before(to); hour = hour.Add(time.Hour) {
		if present[hour.In(shanghaiLocation()).Format("2006-01-02 15")] {
			continue
		}
		if len(windows) > 0 && windows[len(windows)-1].to.Equal(hour) {
			windows[len(windows)-1].to = hour.Add(time.Hour)
		} else {
			windows = append(windows, gapWindow{hour, hour.Add(time.Hour)})
		}
	}
	return windows
}

func runDailyGapAudit(ctx context.Context, st *store.Store, group store.Group) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	lastAttempt := ""
	for {
		day, from, to, due := dailyAuditBounds(time.Now())
		if due && day != lastAttempt {
			if err := auditMessageGaps(ctx, st, group, day, from, to); err != nil {
				if ctx.Err() == nil {
					slog.Error("daily message gap audit failed; will retry", "day", day, "error", err)
				}
			} else {
				lastAttempt = day
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func auditMessageGaps(ctx context.Context, st *store.Store, group store.Group, day string, from, to time.Time) error {
	completed, err := st.DailyGapAuditCompleted(ctx, group.ID, day)
	if err != nil || completed {
		return err
	}
	present, err := st.MessageHours(ctx, group.ID, from, to)
	if err != nil {
		return err
	}
	windows := missingHourWindows(from, to, present)
	slog.Info("daily message gap audit", "day", day, "groupID", group.WeiboGroupID, "missingRanges", len(windows))
	for _, window := range windows {
		end := window.to.Add(-time.Microsecond)
		job, err := st.GetDailyGapJob(ctx, group.ID, window.from, end)
		if errors.Is(err, pgx.ErrNoRows) {
			for {
				job, err = st.EnqueueHistorySync(ctx, store.EnqueueHistorySyncParams{
					GroupID: group.ID, Mode: "daily_gap", RequestedFrom: &window.from,
					RequestedTo: &end, MaxPages: 1000,
				})
				if err != nil {
					return err
				}
				if job.Mode == "daily_gap" && job.RequestedFrom != nil && job.RequestedTo != nil && job.RequestedFrom.Equal(window.from) && job.RequestedTo.Equal(end) {
					break
				}
				if _, err = st.WaitHistorySyncJob(ctx, job.ID); err != nil {
					return err
				}
			}
		} else if err != nil {
			return err
		}
		if _, err := st.WaitHistorySyncJob(ctx, job.ID); err != nil {
			return err
		}
	}
	return st.CompleteDailyGapAudit(ctx, group.ID, day)
}

func runHistorySyncAlertWorker(ctx context.Context, st *store.Store, client *notifier.WeworkClient, enabled bool) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		if enabled {
			job, err := st.ClaimHistorySyncAlert(ctx)
			switch {
			case err == nil:
				content := dailyGapAlertText(job)
				_, sendErr := client.SendTextWithIdempotency(ctx, content, fmt.Sprintf("weibo-daily-gap-%d-%s", job.ID, job.Status))
				if sendErr == nil {
					sendErr = st.CompleteHistorySyncAlert(ctx, job.ID)
				} else {
					_ = st.RetryHistorySyncAlert(ctx, job.ID, sendErr)
				}
				if sendErr != nil {
					slog.Warn("daily gap WeCom alert failed", "jobID", job.ID, "error", sendErr)
				}
				continue
			case !errors.Is(err, pgx.ErrNoRows):
				slog.Warn("claim daily gap WeCom alert failed", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func dailyGapAlertText(job store.HistorySyncJob) string {
	from, to := "?", "?"
	if job.RequestedFrom != nil {
		from = job.RequestedFrom.In(shanghaiLocation()).Format("2006-01-02 15:04")
	}
	if job.RequestedTo != nil {
		to = job.RequestedTo.In(shanghaiLocation()).Add(time.Microsecond).Format("2006-01-02 15:04")
	}
	result := "成功"
	detail := fmt.Sprintf("补入 %d 条；结束原因：%s", job.MessagesInserted, job.StopReason)
	if job.Status == "failed" {
		result = "失败"
		detail = strings.TrimSpace(job.LastError)
	}
	return fmt.Sprintf("微博消息空小时回溯%s\n群聊：%s\n范围：%s 至 %s（北京时间）\n任务：%d\n%s", result, job.GroupName, from, to, job.ID, detail)
}
