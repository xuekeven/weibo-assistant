package app

import (
	"strings"
	"testing"
	"time"

	"github.com/xuekeven/weibo-assistant/internal/store"
)

func TestDailyAuditBoundsAndMissingHourWindows(t *testing.T) {
	zone := shanghaiLocation()
	at := time.Date(2026, 10, 14, 3, 0, 0, 0, zone)
	if _, _, _, due := dailyAuditBounds(at.Add(-time.Second)); due {
		t.Fatal("audit ran before 03:00")
	}
	day, from, to, due := dailyAuditBounds(at)
	if !due || day != "2026-10-14" || from.Format("2006-01-02 15") != "2026-10-13 07" || to.Format("2006-01-02 15") != "2026-10-14 03" {
		t.Fatalf("bounds = %q %v %v %v", day, from, to, due)
	}
	present := make(map[string]bool)
	for hour := from; hour.Before(to); hour = hour.Add(time.Hour) {
		present[hour.Format("2006-01-02 15")] = true
	}
	delete(present, "2026-10-13 18")
	delete(present, "2026-10-13 19")
	delete(present, "2026-10-14 02")
	windows := missingHourWindows(from, to, present)
	if len(windows) != 2 || windows[0].from.Hour() != 18 || windows[0].to.Hour() != 20 || windows[1].from.Hour() != 2 || windows[1].to.Hour() != 3 {
		t.Fatalf("windows = %+v", windows)
	}
}

func TestDailyGapHistoryDoesNotStopAtExistingMessage(t *testing.T) {
	if reached, _ := historySyncBoundaryReached("daily_gap", "", "mid", true); reached {
		t.Fatal("existing messages must not hide a later missing hour")
	}
}

func TestDailyGapAlertReportsBothOutcomes(t *testing.T) {
	zone := shanghaiLocation()
	from := time.Date(2026, 10, 13, 18, 0, 0, 0, zone)
	to := from.Add(time.Hour - time.Microsecond)
	job := store.HistorySyncJob{ID: 7, GroupName: "测试群", RequestedFrom: &from, RequestedTo: &to, Status: "completed", MessagesInserted: 12, StopReason: "requested_start_reached"}
	if got := dailyGapAlertText(job); !containsAll(got, "成功", "18:00", "19:00", "补入 12 条") {
		t.Fatalf("success alert = %q", got)
	}
	job.Status, job.LastError = "failed", "network timeout"
	if got := dailyGapAlertText(job); !containsAll(got, "失败", "network timeout") {
		t.Fatalf("failure alert = %q", got)
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
