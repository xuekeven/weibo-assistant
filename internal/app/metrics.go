package app

import (
	"context"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xuekeven/weibo-assistant/internal/store"
)

type monitorMetrics struct {
	// atomic 字段可被采集 goroutine、通知 goroutine和上报 goroutine并发读写。
	startedAt time.Time

	browserRunning  atomic.Bool
	cdpConnected    atomic.Bool
	cdpReconnects   atomic.Int64
	parsedTotal     atomic.Int64
	persistSuccess  atomic.Int64
	persistFailure  atomic.Int64
	dedupeTotal     atomic.Int64
	notifySent      atomic.Int64
	notifyRetry     atomic.Int64
	notifyFailed    atomic.Int64
	trackedRequests atomic.Int64
	imageCacheItems atomic.Int64

	// 60 个桶分别保存最近 60 秒的解析量，避免保存无限增长的事件列表。
	minuteMu      sync.Mutex
	secondEpochs  [60]int64
	secondBuckets [60]int64
}

func newMonitorMetrics() *monitorMetrics {
	return &monitorMetrics{startedAt: time.Now()}
}

func (m *monitorMetrics) recordParsed(count int) {
	if count <= 0 {
		return
	}
	m.parsedTotal.Add(int64(count))
	epoch := time.Now().Unix()
	index := epoch % int64(len(m.secondBuckets))
	m.minuteMu.Lock()
	if m.secondEpochs[index] != epoch {
		m.secondEpochs[index] = epoch
		m.secondBuckets[index] = 0
	}
	m.secondBuckets[index] += int64(count)
	m.minuteMu.Unlock()
}

func (m *monitorMetrics) snapshot() store.RuntimeMetrics {
	// snapshot 把进程内计数器复制成可写入数据库的普通结构体。
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	nowEpoch := time.Now().Unix()
	m.minuteMu.Lock()
	var lastMinute int64
	for index, epoch := range m.secondEpochs {
		if epoch >= nowEpoch-59 && epoch <= nowEpoch {
			lastMinute += m.secondBuckets[index]
		}
	}
	m.minuteMu.Unlock()
	return store.RuntimeMetrics{
		ServiceName:              "monitor",
		BrowserRunning:           m.browserRunning.Load(),
		CDPConnected:             m.cdpConnected.Load(),
		CDPReconnectTotal:        m.cdpReconnects.Load(),
		MessagesParsedTotal:      m.parsedTotal.Load(),
		MessagesParsedLastMinute: lastMinute,
		PersistSuccessTotal:      m.persistSuccess.Load(),
		PersistFailureTotal:      m.persistFailure.Load(),
		DedupeTotal:              m.dedupeTotal.Load(),
		NotificationSentTotal:    m.notifySent.Load(),
		NotificationRetryTotal:   m.notifyRetry.Load(),
		NotificationFailedTotal:  m.notifyFailed.Load(),
		TrackedRequests:          m.trackedRequests.Load(),
		ImageCacheItems:          m.imageCacheItems.Load(),
		AllocBytes:               int64(memory.Alloc),
		Goroutines:               int64(runtime.NumGoroutine()),
		ProcessStartedAt:         m.startedAt,
	}
}

func runMetricsReporter(ctx context.Context, st *store.Store, metrics *monitorMetrics) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	write := func(writeCtx context.Context) {
		if err := st.UpsertRuntimeMetrics(writeCtx, metrics.snapshot()); err != nil {
			slog.Warn("persist monitor runtime metrics failed", "error", err)
		}
	}
	write(ctx)
	for {
		select {
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			write(flushCtx)
			cancel()
			return
		case <-ticker.C:
			write(ctx)
		}
	}
}
