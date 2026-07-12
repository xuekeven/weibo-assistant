package app

import "testing"

func TestMonitorMetricsSnapshot(t *testing.T) {
	metrics := newMonitorMetrics()
	metrics.browserRunning.Store(true)
	metrics.cdpConnected.Store(true)
	metrics.recordParsed(3)
	metrics.persistSuccess.Add(2)
	metrics.dedupeTotal.Add(1)
	metrics.trackedRequests.Store(7)

	snapshot := metrics.snapshot()
	if !snapshot.BrowserRunning || !snapshot.CDPConnected {
		t.Fatal("expected browser and CDP gauges")
	}
	if snapshot.MessagesParsedTotal != 3 || snapshot.MessagesParsedLastMinute != 3 {
		t.Fatalf("unexpected parsed metrics: %#v", snapshot)
	}
	if snapshot.PersistSuccessTotal != 2 || snapshot.DedupeTotal != 1 || snapshot.TrackedRequests != 7 {
		t.Fatalf("unexpected counters: %#v", snapshot)
	}
	if snapshot.AllocBytes <= 0 || snapshot.Goroutines <= 0 {
		t.Fatalf("expected runtime metrics: %#v", snapshot)
	}
}
