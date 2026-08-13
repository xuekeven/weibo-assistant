package browser

import (
	"testing"
	"time"
)

func TestRendererCPUTrackerUsesHighestRenderer(t *testing.T) {
	now := time.Unix(100, 0)
	samples := [][]rendererProcessSample{
		{{pid: 10, ticks: 100}, {pid: 11, ticks: 200}},
		{{pid: 10, ticks: 700}, {pid: 11, ticks: 500}},
	}
	index := 0
	tracker := &rendererCPUTracker{
		rootPID: 1,
		now:     func() time.Time { return now },
		read: func(int) ([]rendererProcessSample, error) {
			result := samples[index]
			index++
			return result, nil
		},
	}
	if _, _, available, err := tracker.Sample(); err != nil || available {
		t.Fatalf("first sample should only establish a baseline: available=%v err=%v", available, err)
	}
	now = now.Add(10 * time.Second)
	pid, percent, available, err := tracker.Sample()
	if err != nil || !available || pid != 10 || percent != 60 {
		t.Fatalf("unexpected sample: pid=%d percent=%v available=%v err=%v", pid, percent, available, err)
	}
}

func TestRendererCPUWatchdogReloadsThenRestartsOnlyIfStillHigh(t *testing.T) {
	watchdog := newRendererCPUWatchdog(80, 2)
	if action := watchdog.Observe(90); action != rendererCPUActionNone {
		t.Fatalf("first high sample action = %v", action)
	}
	if action := watchdog.Observe(95); action != rendererCPUActionReload {
		t.Fatalf("second high sample action = %v, want reload", action)
	}
	if action := watchdog.Observe(90); action != rendererCPUActionNone {
		t.Fatalf("first post-reload high sample action = %v", action)
	}
	if action := watchdog.Observe(90); action != rendererCPUActionRestart {
		t.Fatalf("second post-reload high sample action = %v, want restart", action)
	}
}

func TestRendererCPUWatchdogClearsEscalationAfterRecovery(t *testing.T) {
	watchdog := newRendererCPUWatchdog(80, 2)
	watchdog.Observe(90)
	if action := watchdog.Observe(90); action != rendererCPUActionReload {
		t.Fatalf("action = %v, want reload", action)
	}
	if action := watchdog.Observe(1); action != rendererCPUActionNone {
		t.Fatalf("first recovery sample action = %v", action)
	}
	if action := watchdog.Observe(2); action != rendererCPUActionRecovered {
		t.Fatalf("second recovery sample action = %v, want recovered", action)
	}
	watchdog.Observe(90)
	if action := watchdog.Observe(90); action != rendererCPUActionReload {
		t.Fatalf("recurrent incident action = %v, want a fresh reload", action)
	}
}

func TestRendererCPUWatchdogDoesNotReportRecoveryWithoutReload(t *testing.T) {
	watchdog := newRendererCPUWatchdog(80, 2)
	if action := watchdog.Observe(1); action != rendererCPUActionNone {
		t.Fatalf("normal sample action = %v", action)
	}
	if action := watchdog.Observe(2); action != rendererCPUActionNone {
		t.Fatalf("normal sample action = %v", action)
	}
}

func TestRendererCommandLineDetectionRequiresExactProcessType(t *testing.T) {
	if !isRendererCommandLine([]byte("chrome\x00--type=renderer\x00--headless\x00")) {
		t.Fatal("expected renderer command line to match")
	}
	if !isRendererCommandLine([]byte("chrome --type=renderer --headless")) {
		t.Fatal("expected Chromium's space-rewritten command line to match")
	}
	if isRendererCommandLine([]byte("chrome\x00--type=renderer-helper\x00")) {
		t.Fatal("renderer-helper must not match")
	}
}
