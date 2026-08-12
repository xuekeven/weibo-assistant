package browser

import (
	"strings"
	"time"
)

type rendererCPUAction uint8

const (
	rendererCPUActionNone rendererCPUAction = iota
	rendererCPUActionReload
	rendererCPUActionRestart
)

type rendererCPUWatchdog struct {
	limit              float64
	requiredChecks     int
	highChecks         int
	awaitingRecovery   bool
	healthyAfterReload int
}

func newRendererCPUWatchdog(limit float64, requiredChecks int) *rendererCPUWatchdog {
	return &rendererCPUWatchdog{limit: limit, requiredChecks: requiredChecks}
}

func (w *rendererCPUWatchdog) Observe(percent float64) rendererCPUAction {
	if percent < w.limit {
		w.highChecks = 0
		if w.awaitingRecovery {
			w.healthyAfterReload++
			if w.healthyAfterReload >= w.requiredChecks {
				w.awaitingRecovery = false
				w.healthyAfterReload = 0
			}
		}
		return rendererCPUActionNone
	}

	w.healthyAfterReload = 0
	w.highChecks++
	if w.highChecks < w.requiredChecks {
		return rendererCPUActionNone
	}
	w.highChecks = 0
	if w.awaitingRecovery {
		return rendererCPUActionRestart
	}
	w.awaitingRecovery = true
	return rendererCPUActionReload
}

type rendererProcessSample struct {
	pid   int
	ticks uint64
}

type rendererCPUTracker struct {
	rootPID    int
	previous   map[int]uint64
	previousAt time.Time
	now        func() time.Time
	read       func(int) ([]rendererProcessSample, error)
}

func newRendererCPUTracker(rootPID int) *rendererCPUTracker {
	return &rendererCPUTracker{
		rootPID: rootPID,
		now:     time.Now,
		read:    readRendererProcessSamples,
	}
}

func (t *rendererCPUTracker) Sample() (int, float64, bool, error) {
	now := t.now()
	samples, err := t.read(t.rootPID)
	if err != nil {
		return 0, 0, false, err
	}
	current := make(map[int]uint64, len(samples))
	for _, sample := range samples {
		current[sample.pid] = sample.ticks
	}
	if t.previous == nil || !now.After(t.previousAt) {
		t.previous = current
		t.previousAt = now
		return 0, 0, false, nil
	}

	elapsed := now.Sub(t.previousAt).Seconds()
	maxPID := 0
	maxPercent := 0.0
	available := false
	for pid, ticks := range current {
		previousTicks, found := t.previous[pid]
		if !found || ticks < previousTicks {
			continue
		}
		// Linux /proc CPU counters use USER_HZ, which is 100 on supported Linux targets.
		percent := float64(ticks-previousTicks) / elapsed
		if !available || percent > maxPercent {
			maxPID = pid
			maxPercent = percent
			available = true
		}
	}
	t.previous = current
	t.previousAt = now
	return maxPID, maxPercent, available, nil
}

func isRendererCommandLine(cmdline []byte) bool {
	// Chromium 会在启动后重写 argv，把 /proc/<pid>/cmdline 中原本的 NUL
	// 分隔符替换为空格。两种格式都必须支持，否则长期运行时会漏掉 renderer。
	normalized := strings.ReplaceAll(string(cmdline), "\x00", " ")
	for _, argument := range strings.Fields(normalized) {
		if argument == "--type=renderer" {
			return true
		}
	}
	return false
}
