package browser

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"math"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type MonitorOptions struct {
	// MonitorOptions 既包含启动参数，也包含采集事件回调。
	// browser 通过回调报告结果，避免反向依赖 store 或 notifier。
	URL                    string
	ProfileDir             string
	Executable             string
	Headless               bool
	NoSandbox              bool
	DebugPort              int
	RendererCPULimit       int
	RendererCPUChecks      int
	RendererCPUInterval    time.Duration
	HealthCheckInterval    time.Duration
	PageReloadInterval     time.Duration
	TargetGroupID          string
	TargetGroupName        string
	AuthCheckRequests      <-chan string
	OnProbe                func(context.Context, PageProbe) error
	OnMessage              func(context.Context, NetworkMessage) error
	OnImageReference       func(context.Context, NetworkImageReference) error
	OnBrowserState         func(bool)
	OnCDPState             func(connected bool, reconnected bool)
	OnPageTarget           func(webSocketDebuggerURL string)
	OnWebSocketState       func(connected bool, reconnected bool)
	OnMessagesParsed       func(int)
	OnTrackedRequests      func(int)
	OnBeforePageReload     func(context.Context) error
	OnRendererCPURecovered func(context.Context, RendererCPUEvent) error
	OnRendererCPURestart   func(context.Context, RendererCPUEvent) error
	OnLoginRequired        func(context.Context, LoginRequiredEvent) error
	OnLoginRestored        func()
}

type RendererCPUEvent struct {
	PID          int
	CPUPercent   float64
	LimitPercent int
}

type LoginRequiredEvent struct {
	URL           string
	Title         string
	ScreenshotPNG []byte
}

var ErrLoginNotRequired = errors.New("Weibo login is not required")

type PageProbe struct {
	Status            string
	URL               string
	Title             string
	Error             string
	CandidateError    string
	GroupSelectStatus string
	GroupSelectError  string
	MessageCandidates []MessageCandidate
	CheckedAt         time.Time
}

type MessageCandidate struct {
	Index     int      `json:"index"`
	Tag       string   `json:"tag"`
	ID        string   `json:"id,omitempty"`
	ClassName string   `json:"className,omitempty"`
	Role      string   `json:"role,omitempty"`
	Text      string   `json:"text"`
	Images    []string `json:"images,omitempty"`
	Rect      Rect     `json:"rect"`
	Score     int      `json:"score"`
}

type NetworkMessage struct {
	WeiboMessageID string              `json:"weiboMessageId"`
	GroupID        string              `json:"groupId"`
	GroupName      string              `json:"groupName"`
	SenderID       string              `json:"senderId"`
	SenderName     string              `json:"senderName"`
	SenderAvatar   string              `json:"senderAvatar,omitempty"`
	MessageType    string              `json:"messageType"`
	Content        string              `json:"content"`
	SentAt         *time.Time          `json:"sentAt,omitempty"`
	Source         string              `json:"source"`
	DedupeKey      string              `json:"dedupeKey"`
	Attachments    []NetworkAttachment `json:"attachments,omitempty"`
	Raw            json.RawMessage     `json:"raw"`
}

type NetworkAttachment struct {
	AttachmentType string          `json:"attachmentType"`
	URL            string          `json:"url,omitempty"`
	LocalPath      string          `json:"localPath,omitempty"`
	Meta           json.RawMessage `json:"meta,omitempty"`
}

type NetworkImageReference struct {
	FIDs      []string `json:"fids"`
	URLs      []string `json:"urls"`
	SourceURL string   `json:"sourceUrl,omitempty"`
}

type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type chromeTarget struct {
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	Title                string `json:"title"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func RunMonitor(ctx context.Context, opts MonitorOptions) error {
	// 流程分为四个阶段：校验配置、启动 Chromium、连接 CDP、循环健康检查。
	if strings.TrimSpace(opts.URL) == "" {
		return errors.New("browser monitor URL is required")
	}
	if strings.TrimSpace(opts.ProfileDir) == "" {
		return errors.New("browser profile dir is required")
	}
	if opts.DebugPort <= 0 || opts.DebugPort > 65535 {
		return errors.New("browser debug port must be between 1 and 65535")
	}
	if opts.HealthCheckInterval <= 0 {
		opts.HealthCheckInterval = 60 * time.Second
	}
	if opts.RendererCPULimit <= 0 {
		opts.RendererCPULimit = 50
	}
	if opts.RendererCPUChecks <= 0 {
		opts.RendererCPUChecks = 3
	}
	if opts.RendererCPUInterval <= 0 {
		opts.RendererCPUInterval = time.Minute
	}

	profileDir, err := filepath.Abs(opts.ProfileDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return fmt.Errorf("create browser profile dir: %w", err)
	}

	executable, err := findExecutable(opts.Executable)
	if err != nil {
		return err
	}

	// CommandContext 会在 ctx 取消时终止 Chromium，停服时不会残留子进程。
	cmd := exec.CommandContext(ctx, executable, monitorArgs(opts, profileDir)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	slog.Info("starting browser monitor", "executable", executable, "profileDir", profileDir, "url", networkURLLogValue(opts.URL), "headless", opts.Headless, "debugPort", opts.DebugPort)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start browser monitor: %w", err)
	}
	if opts.OnBrowserState != nil {
		opts.OnBrowserState(true)
		defer opts.OnBrowserState(false)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- cmd.Wait()
	}()

	if err := waitForDebugEndpoint(ctx, opts.DebugPort, 15*time.Second); err != nil {
		stopProcess(cmd)
		return err
	}
	rendererCPU := newRendererCPUTracker(cmd.Process.Pid)
	_, _, _, _ = rendererCPU.Sample()
	rendererWatchdog := newRendererCPUWatchdog(float64(opts.RendererCPULimit), opts.RendererCPUChecks)
	type rendererCPUReading struct {
		pid       int
		percent   float64
		available bool
		err       error
	}
	rendererCPUReadings := make(chan rendererCPUReading, 1)
	go func() {
		ticker := time.NewTicker(opts.RendererCPUInterval)
		defer ticker.Stop()
		baselineLogged := false
		sampleLogged := false
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pid, percent, available, sampleErr := rendererCPU.Sample()
				if sampleErr != nil {
					slog.Warn("sample Chrome renderer CPU failed", "error", sampleErr)
				} else if !available && !baselineLogged {
					slog.Info("Chrome renderer CPU sampler is waiting for a comparable baseline")
					baselineLogged = true
				} else if available && !sampleLogged {
					slog.Info("Chrome renderer CPU sampling is active",
						"pid", pid,
						"cpuPercent", percent,
					)
					sampleLogged = true
				}
				reading := rendererCPUReading{pid: pid, percent: percent, available: available, err: sampleErr}
				select {
				case rendererCPUReadings <- reading:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	slog.Info("Chrome renderer CPU watchdog enabled",
		"interval", opts.RendererCPUInterval,
		"limitPercent", opts.RendererCPULimit,
		"consecutiveChecks", opts.RendererCPUChecks,
	)

	if opts.OnProbe != nil {
		_ = opts.OnProbe(ctx, PageProbe{
			Status:    "browser_started",
			URL:       opts.URL,
			CheckedAt: time.Now(),
		})
	}

	target, probe, err := waitForAuthenticatedChatPage(ctx, opts)
	if err != nil {
		stopProcess(cmd)
		return err
	}
	if probe.Status == "page_ok" && target.WebSocketDebuggerURL != "" {
		var selectStatus string
		target, probe, selectStatus, err = ensureTargetGroupSelected(ctx, opts, target, probe, TargetGroup{
			ID:   opts.TargetGroupID,
			Name: opts.TargetGroupName,
		})
		probe.GroupSelectStatus = selectStatus
		if err != nil {
			probe.GroupSelectError = err.Error()
			stopProcess(cmd)
			return err
		}
		if opts.OnPageTarget != nil {
			opts.OnPageTarget(target.WebSocketDebuggerURL)
		}
	}
	logMonitorHeartbeat(probe)
	if opts.OnProbe != nil {
		if err := opts.OnProbe(ctx, probe); err != nil {
			slog.Warn("update monitor status failed", "error", err)
		}
	}

	type networkMonitorResult struct {
		generation int
		err        error
	}
	networkErrCh := make(chan networkMonitorResult, 1)
	webSocketClosedCh := make(chan struct{}, 16)
	var networkCancel context.CancelFunc
	networkGeneration := 0
	reconnectAttempt := 0
	var reconnectTimer *time.Timer
	var reconnectC <-chan time.Time

	stopReconnectTimer := func() {
		if reconnectTimer == nil {
			return
		}
		if !reconnectTimer.Stop() {
			select {
			case <-reconnectTimer.C:
			default:
			}
		}
		reconnectC = nil
	}
	scheduleNetworkReconnect := func(reason error) {
		reconnectAttempt++
		delay := networkReconnectDelay(reconnectAttempt)
		stopReconnectTimer()
		if reconnectTimer == nil {
			reconnectTimer = time.NewTimer(delay)
		} else {
			reconnectTimer.Reset(delay)
		}
		reconnectC = reconnectTimer.C
		slog.Warn("network monitor disconnected; scheduling in-process reconnect",
			"attempt", reconnectAttempt,
			"retryIn", delay,
			"error", reason,
		)
	}
	// 页面重载后 debugger URL 可能变化。新监听启动前先取消旧监听，
	// generation 用来识别已经过期的 goroutine 返回结果。
	startNetworkMonitor := func(webSocketDebuggerURL string) {
		stopReconnectTimer()
		if networkCancel != nil {
			networkCancel()
		}
		networkGeneration++
		generation := networkGeneration
		networkCtx, cancel := context.WithCancel(ctx)
		networkCancel = cancel
		if opts.OnCDPState != nil {
			opts.OnCDPState(true, generation > 1)
		}
		go func() {
			err := monitorNetworkEvents(networkCtx, webSocketDebuggerURL, opts.OnMessage, opts.OnImageReference, opts.OnMessagesParsed, opts.OnTrackedRequests, opts.OnWebSocketState, func() {
				select {
				case webSocketClosedCh <- struct{}{}:
				default:
				}
			})
			select {
			case networkErrCh <- networkMonitorResult{generation: generation, err: err}:
			case <-ctx.Done():
			}
		}()
	}
	startNetworkMonitor(target.WebSocketDebuggerURL)
	defer func() {
		stopReconnectTimer()
		if networkCancel != nil {
			networkCancel()
		}
		if opts.OnCDPState != nil {
			opts.OnCDPState(false, false)
		}
	}()

	ticker := time.NewTicker(opts.HealthCheckInterval)
	defer ticker.Stop()

	var reloadTicker *time.Ticker
	var reloadC <-chan time.Time
	if opts.PageReloadInterval > 0 {
		reloadTicker = time.NewTicker(opts.PageReloadInterval)
		defer reloadTicker.Stop()
		reloadC = reloadTicker.C
	}

	reloadCurrentPage := func(reason string) {
		if opts.OnBeforePageReload != nil {
			if err := opts.OnBeforePageReload(ctx); err != nil {
				slog.Warn("page reload preflight check failed", "reason", reason, "error", err)
			}
		}
		slog.Info("reloading current chat page", "reason", reason, "url", networkURLLogValue(target.URL))
		if err := reloadPage(ctx, target.WebSocketDebuggerURL); err != nil {
			slog.Warn("page reload failed", "reason", reason, "error", err)
		}
	}
	lastAuthCheckAt := time.Time{}
	checkAuthenticationAfterAnomaly := func(reason string) {
		if strings.TrimSpace(opts.TargetGroupID) == "" || time.Since(lastAuthCheckAt) < time.Minute {
			return
		}
		currentTarget, probe := getCurrentPageProbe(ctx, opts.DebugPort)
		if probe.Status != "page_ok" || currentTarget.WebSocketDebuggerURL == "" {
			// The normal heartbeat handles an already-visible login page.
			return
		}
		lastAuthCheckAt = time.Now()
		checkCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		_, err := FetchGroupHistoryPage(checkCtx, currentTarget.WebSocketDebuggerURL, GroupHistoryRequest{
			GroupID: opts.TargetGroupID,
			Count:   1,
		}, "weibo_auth_check")
		cancel()
		if err == nil {
			slog.Info("active weibo login verification succeeded", "reason", reason)
			return
		}
		// A failed history request alone does not prove login expiry. Reloading
		// makes the actual login page visible, so the normal probe can decide.
		slog.Warn("active weibo login verification failed; reloading chat page", "reason", reason, "error", err)
		target = currentTarget
		reloadCurrentPage("auth_verification_failed")
	}
	var recentWebSocketCloses []time.Time

	checkRendererCPU := func(reading rendererCPUReading) error {
		if reading.err != nil {
			return nil
		}
		if !reading.available {
			return nil
		}
		action := rendererWatchdog.Observe(reading.percent)
		if reading.percent >= float64(opts.RendererCPULimit) {
			consecutive := rendererWatchdog.highChecks
			if action != rendererCPUActionNone {
				consecutive = opts.RendererCPUChecks
			}
			slog.Warn("Chrome renderer CPU is high",
				"pid", reading.pid,
				"cpuPercent", reading.percent,
				"limitPercent", opts.RendererCPULimit,
				"consecutive", consecutive,
			)
		}
		switch action {
		case rendererCPUActionReload:
			reloadCurrentPage("renderer_cpu_high")
		case rendererCPUActionRecovered:
			slog.Info("Chrome renderer CPU recovered after page reload",
				"pid", reading.pid,
				"cpuPercent", reading.percent,
				"limitPercent", opts.RendererCPULimit,
			)
			if opts.OnRendererCPURecovered != nil {
				event := RendererCPUEvent{PID: reading.pid, CPUPercent: reading.percent, LimitPercent: opts.RendererCPULimit}
				if err := opts.OnRendererCPURecovered(ctx, event); err != nil {
					slog.Warn("send Chrome renderer CPU recovery notification failed", "error", err)
				}
			}
		case rendererCPUActionRestart:
			slog.Error("Chrome renderer CPU remained high after page reload; restarting browser process",
				"pid", reading.pid,
				"cpuPercent", reading.percent,
			)
			if opts.OnRendererCPURestart != nil {
				event := RendererCPUEvent{PID: reading.pid, CPUPercent: reading.percent, LimitPercent: opts.RendererCPULimit}
				if err := opts.OnRendererCPURestart(ctx, event); err != nil {
					slog.Warn("send Chrome renderer CPU restart notification failed", "error", err)
				}
			}
			return fmt.Errorf("Chrome renderer CPU remained high after page reload: pid=%d cpu=%.1f%%", reading.pid, reading.percent)
		}
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			stopProcess(cmd)
			return nil
		case err := <-errCh:
			if ctx.Err() != nil {
				return nil
			}
			if err != nil {
				return fmt.Errorf("browser monitor exited: %w", err)
			}
			return errors.New("browser monitor exited")
		case reason := <-opts.AuthCheckRequests:
			checkAuthenticationAfterAnomaly(reason)
		case <-webSocketClosedCh:
			recentWebSocketCloses = appendRecentWebSocketClose(recentWebSocketCloses, time.Now(), 2*time.Minute)
			if len(recentWebSocketCloses) >= 2 {
				checkAuthenticationAfterAnomaly("repeated_websocket_close")
			}
		case <-ticker.C:
			currentTarget, probe := getCurrentPageProbe(ctx, opts.DebugPort)
			if probe.Status == "login_required" {
				var err error
				currentTarget, probe, err = waitForAuthenticatedChatPage(ctx, opts)
				if err != nil {
					stopProcess(cmd)
					if ctx.Err() != nil {
						return nil
					}
					return err
				}
			}
			if probe.Status == "page_ok" && currentTarget.WebSocketDebuggerURL != "" {
				selectCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
				selectStatus, selectErr := selectTargetGroup(selectCtx, currentTarget.WebSocketDebuggerURL, TargetGroup{
					ID:   opts.TargetGroupID,
					Name: opts.TargetGroupName,
				})
				cancel()
				probe.GroupSelectStatus = selectStatus
				if selectErr != nil {
					probe.GroupSelectError = selectErr.Error()
				}
				if currentTarget.WebSocketDebuggerURL != target.WebSocketDebuggerURL {
					slog.Warn("chat page target changed; rebinding network monitor",
						"oldURL", networkURLLogValue(target.URL),
						"newURL", networkURLLogValue(currentTarget.URL),
					)
					target = currentTarget
					startNetworkMonitor(target.WebSocketDebuggerURL)
				}
				if selectErr == nil {
					probe.Status = "network_listening"
					if opts.OnPageTarget != nil {
						opts.OnPageTarget(currentTarget.WebSocketDebuggerURL)
					}
				}
			}
			logMonitorHeartbeat(probe)
			if opts.OnProbe != nil {
				if err := opts.OnProbe(ctx, probe); err != nil {
					slog.Warn("update monitor status failed", "error", err)
				}
			}
		case reading := <-rendererCPUReadings:
			if err := checkRendererCPU(reading); err != nil {
				stopProcess(cmd)
				return err
			}
		case result := <-networkErrCh:
			if result.generation != networkGeneration {
				continue
			}
			if networkCancel != nil {
				networkCancel()
				networkCancel = nil
			}
			if opts.OnCDPState != nil {
				opts.OnCDPState(false, false)
			}
			if ctx.Err() != nil {
				stopProcess(cmd)
				return nil
			}
			disconnectErr := result.err
			if disconnectErr == nil {
				disconnectErr = errors.New("network monitor exited without an error")
			}
			scheduleNetworkReconnect(disconnectErr)
		case <-reconnectC:
			reconnectC = nil
			currentTarget, reconnectProbe := getCurrentPageProbe(ctx, opts.DebugPort)
			if reconnectProbe.Status == "login_required" {
				var err error
				currentTarget, reconnectProbe, err = waitForAuthenticatedChatPage(ctx, opts)
				if err != nil {
					stopProcess(cmd)
					if ctx.Err() != nil {
						return nil
					}
					return err
				}
			}
			if reconnectProbe.Status != "page_ok" || currentTarget.WebSocketDebuggerURL == "" {
				scheduleNetworkReconnect(fmt.Errorf("chat page is not ready: status=%s error=%s", reconnectProbe.Status, reconnectProbe.Error))
				continue
			}
			target = currentTarget
			slog.Info("reconnecting network monitor in current browser process",
				"attempt", reconnectAttempt,
				"url", networkURLLogValue(target.URL),
			)
			startNetworkMonitor(target.WebSocketDebuggerURL)
			if opts.OnPageTarget != nil {
				opts.OnPageTarget(target.WebSocketDebuggerURL)
			}
		case <-reloadC:
			reloadCurrentPage("scheduled")
		}
	}
}

// networkReconnectDelay 使用有上限的指数退避：1s、2s、4s……最多 30s。
func networkReconnectDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Second
	for current := 1; current < attempt && delay < 30*time.Second; current++ {
		delay *= 2
	}
	if delay > 30*time.Second {
		return 30 * time.Second
	}
	return delay
}

func appendRecentWebSocketClose(closes []time.Time, now time.Time, window time.Duration) []time.Time {
	cutoff := now.Add(-window)
	kept := closes[:0]
	for _, closedAt := range closes {
		if !closedAt.Before(cutoff) {
			kept = append(kept, closedAt)
		}
	}
	return append(kept, now)
}

func monitorArgs(opts MonitorOptions, profileDir string) []string {
	args := []string{
		"--user-data-dir=" + profileDir,
		"--profile-directory=Default",
		"--remote-debugging-address=127.0.0.1",
		fmt.Sprintf("--remote-debugging-port=%d", opts.DebugPort),
		"--window-size=1440,900",
		"--force-device-scale-factor=1",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-dev-shm-usage",
		// 长期运行的 headless renderer 不需要为交互延迟主动忙等。显式关闭该
		// Chromium 实验，避免主线程与 foreground worker 高频互相唤醒。
		"--disable-features=BusyLoopOnMainThread",
	}
	if opts.Headless {
		args = append(args, "--headless=new")
	}
	if opts.NoSandbox {
		args = append(args, "--no-sandbox")
	}
	return append(args, opts.URL)
}

func waitForDebugEndpoint(ctx context.Context, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, debugURL(port, "/json/version"), nil)
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("chrome debug endpoint is not ready on 127.0.0.1:%d", port)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

const (
	loginRequiredError        = "微博登录已失效，请扫描企业微信收到的二维码"
	loginScreenshotDelay      = 3 * time.Second
	loginNotificationRetryGap = time.Minute
	chatPageReadinessDelay    = 2 * time.Second
)

func waitForAuthenticatedChatPage(ctx context.Context, opts MonitorOptions) (chromeTarget, PageProbe, error) {
	loginRequired := false
	alertSent := false
	alertError := ""
	nextAlertAt := time.Time{}
	lastProbeReportedAt := time.Time{}
	pageOKSince := time.Time{}

	for {
		target, probe := getCurrentPageProbe(ctx, opts.DebugPort)
		if probe.Status == "page_ok" && target.WebSocketDebuggerURL != "" {
			if pageOKSince.IsZero() {
				pageOKSince = time.Now()
			}
			if time.Since(pageOKSince) >= chatPageReadinessDelay {
				if loginRequired {
					slog.Info("weibo login restored; resuming monitor", "url", networkURLLogValue(probe.URL))
					reportPageProbe(ctx, opts, probe)
					if opts.OnLoginRestored != nil {
						opts.OnLoginRestored()
					}
				}
				return target, probe, nil
			}
		} else {
			pageOKSince = time.Time{}
		}
		if probe.Status == "login_required" {
			probe.Error = loginRequiredError
			if alertError != "" {
				probe.Error += "；发送登录提醒失败：" + alertError
			}
			now := time.Now()
			if !loginRequired {
				loginRequired = true
				nextAlertAt = now.Add(loginScreenshotDelay)
				reportPageProbe(ctx, opts, probe)
				lastProbeReportedAt = now
				slog.Warn("weibo login is required; waiting for QR code login", "url", networkURLLogValue(probe.URL))
			} else if now.Sub(lastProbeReportedAt) >= opts.HealthCheckInterval {
				reportPageProbe(ctx, opts, probe)
				lastProbeReportedAt = now
			}

			if opts.OnLoginRequired != nil && !alertSent && !now.Before(nextAlertAt) && target.WebSocketDebuggerURL != "" {
				screenshot, screenshotErr := capturePageScreenshot(ctx, target.WebSocketDebuggerURL)
				if screenshotErr == nil {
					screenshotErr = validateLoginQRCodeImage(screenshot)
				}
				if screenshotErr != nil {
					slog.Warn("capture weibo login screenshot failed", "error", screenshotErr)
					// The QR may have expired while the login page still shows an
					// overlay. Reload it rather than sending that page as a QR image.
					if reloadErr := reloadPage(ctx, target.WebSocketDebuggerURL); reloadErr != nil {
						slog.Warn("reload weibo login page after invalid QR failed", "error", reloadErr)
					}
					alertError = screenshotErr.Error()
					probe.Error = loginRequiredError + "；发送登录提醒失败：" + alertError
					reportPageProbe(ctx, opts, probe)
					lastProbeReportedAt = now
					nextAlertAt = now.Add(loginNotificationRetryGap)
				} else {
					notifyErr := opts.OnLoginRequired(ctx, LoginRequiredEvent{
						URL:           probe.URL,
						Title:         probe.Title,
						ScreenshotPNG: screenshot,
					})
					if notifyErr != nil {
						slog.Warn("send weibo login notification failed", "error", notifyErr)
						alertError = notifyErr.Error()
						probe.Error = loginRequiredError + "；发送登录提醒失败：" + alertError
						reportPageProbe(ctx, opts, probe)
						lastProbeReportedAt = now
						nextAlertAt = now.Add(loginNotificationRetryGap)
					} else {
						alertError = ""
						alertSent = true
						probe.Error = loginRequiredError
						reportPageProbe(ctx, opts, probe)
						lastProbeReportedAt = now
						slog.Info("weibo login notification sent", "screenshotBytes", len(screenshot))
					}
				}
			}
		} else if lastProbeReportedAt.IsZero() || time.Since(lastProbeReportedAt) >= opts.HealthCheckInterval {
			reportPageProbe(ctx, opts, probe)
			lastProbeReportedAt = time.Now()
		}
		select {
		case <-ctx.Done():
			return chromeTarget{}, probe, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func logMonitorHeartbeat(probe PageProbe) {
	if probe.Status == "network_listening" && strings.TrimSpace(probe.Error) == "" {
		slog.Debug("微博实时监听正常", "页面", networkURLLogValue(probe.URL))
		return
	}
	slog.Info("微博监听状态更新", "状态", probe.Status, "页面", networkURLLogValue(probe.URL), "错误", clipLogText(probe.Error, 500))
}

func reportPageProbe(ctx context.Context, opts MonitorOptions, probe PageProbe) {
	logMonitorHeartbeat(probe)
	if opts.OnProbe == nil {
		return
	}
	if err := opts.OnProbe(ctx, probe); err != nil {
		slog.Warn("update monitor status failed", "error", err)
	}
}

func capturePageScreenshot(ctx context.Context, webSocketDebuggerURL string) ([]byte, error) {
	captureCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	client, err := newCDPClient(captureCtx, webSocketDebuggerURL)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	var qrRefreshed bool
	if err := client.evaluate(captureCtx, refreshExpiredLoginQRCodeExpression, &qrRefreshed); err != nil {
		return nil, fmt.Errorf("refresh expired login QR code: %w", err)
	}
	if qrRefreshed {
		slog.Info("expired weibo login QR code refreshed")
		select {
		case <-captureCtx.Done():
			return nil, captureCtx.Err()
		case <-time.After(time.Second):
		}
	}

	var qr loginQRCodeTarget
	if err := client.evaluate(captureCtx, findLoginQRCodeExpression, &qr); err != nil {
		return nil, fmt.Errorf("locate login QR code: %w", err)
	}
	if qr.valid() && qr.Source != "" {
		qrImage, resourceErr := readPageImageResource(captureCtx, client, qr.Source)
		if resourceErr == nil {
			slog.Info("captured weibo login screenshot",
				"mode", "qr_resource",
				"qrWidth", qr.Width,
				"qrHeight", qr.Height,
				"bytes", len(qrImage),
			)
			return qrImage, nil
		}
		slog.Warn("read weibo login QR resource failed; falling back to element screenshot", "error", resourceErr)
	}

	var overlayDismissed bool
	if err := client.evaluate(captureCtx, dismissLoginOverlayExpression, &overlayDismissed); err != nil {
		return nil, fmt.Errorf("prepare login screenshot: %w", err)
	}
	if overlayDismissed {
		select {
		case <-captureCtx.Done():
			return nil, captureCtx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}

	if err := client.evaluate(captureCtx, findLoginQRCodeExpression, &qr); err != nil {
		return nil, fmt.Errorf("locate login QR code: %w", err)
	}

	screenshotParams := map[string]any{
		"format":      "png",
		"fromSurface": true,
	}
	captureMode := "page"
	if qr.valid() {
		screenshotParams["captureBeyondViewport"] = true
		screenshotParams["clip"] = map[string]any{
			"x":      qr.X,
			"y":      qr.Y,
			"width":  qr.Width,
			"height": qr.Height,
			"scale":  qr.screenshotScale(),
		}
		captureMode = "qr_element"
	} else {
		screenshotParams["captureBeyondViewport"] = false
	}

	var result struct {
		Data string `json:"data"`
	}
	if err := client.call(captureCtx, "Page.captureScreenshot", screenshotParams, &result); err != nil {
		return nil, err
	}
	screenshot, err := base64.StdEncoding.DecodeString(result.Data)
	if err != nil {
		return nil, fmt.Errorf("decode login screenshot: %w", err)
	}
	if len(screenshot) == 0 {
		return nil, errors.New("login screenshot is empty")
	}
	slog.Info("captured weibo login screenshot",
		"mode", captureMode,
		"qrWidth", qr.Width,
		"qrHeight", qr.Height,
		"bytes", len(screenshot),
	)
	return screenshot, nil
}

// RefreshLoginPageAndCaptureQRCode reloads the page owned by the monitor and
// returns the newly rendered login QR code. The status check prevents this
// server-side CDP entry point from reloading an authenticated chat page.
func RefreshLoginPageAndCaptureQRCode(ctx context.Context, port int) ([]byte, error) {
	target, probe := getCurrentPageProbe(ctx, port)
	if probe.Status != "login_required" {
		return nil, ErrLoginNotRequired
	}
	if target.WebSocketDebuggerURL == "" {
		return nil, errors.New("Weibo login page target is unavailable")
	}
	if err := reloadPage(ctx, target.WebSocketDebuggerURL); err != nil {
		return nil, fmt.Errorf("reload Weibo login page: %w", err)
	}

	deadline := time.Now().Add(12 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}

		currentTarget, currentProbe := getCurrentPageProbe(ctx, port)
		if currentProbe.Status != "login_required" || currentTarget.WebSocketDebuggerURL == "" {
			lastErr = errors.New("Weibo login page is still loading")
			continue
		}
		image, err := capturePageScreenshot(ctx, currentTarget.WebSocketDebuggerURL)
		if err == nil {
			if validationErr := validateLoginQRCodeImage(image); validationErr != nil {
				lastErr = validationErr
				continue
			}
			return image, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("Weibo login QR code did not become ready")
	}
	return nil, fmt.Errorf("capture refreshed Weibo login QR code: %w", lastErr)
}

// validateLoginQRCodeImage prevents the login endpoint from returning Weibo's
// grey lazy-loading placeholder (or a full-page screenshot) as a QR code. The
// actual Weibo QR is square and contains substantial light/dark pixel regions;
// the placeholder has neither property even though it uses the same DOM node.
func validateLoginQRCodeImage(data []byte) error {
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("decode login QR image: %w", err)
	}
	bounds := decoded.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width < 80 || height < 80 || width > 2048 || height > 2048 {
		return fmt.Errorf("unexpected login QR dimensions: %dx%d", width, height)
	}
	ratio := float64(width) / float64(height)
	if ratio < 0.8 || ratio > 1.25 {
		return fmt.Errorf("login QR image is not square: %dx%d", width, height)
	}

	var darkPixels, lightPixels int
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := decoded.At(x, y).RGBA()
			// ITU-R BT.601 luminance, with 16-bit color values reduced to 8-bit.
			luminance := (299*(r>>8) + 587*(g>>8) + 114*(b>>8)) / 1000
			if luminance < 96 {
				darkPixels++
			}
			if luminance > 192 {
				lightPixels++
			}
		}
	}
	totalPixels := width * height
	const minimumContrastFraction = 0.05
	if float64(darkPixels)/float64(totalPixels) < minimumContrastFraction ||
		float64(lightPixels)/float64(totalPixels) < minimumContrastFraction {
		return errors.New("login QR image is still a loading placeholder")
	}
	return nil
}

func readPageImageResource(ctx context.Context, client *cdpClient, source string) ([]byte, error) {
	if strings.HasPrefix(source, "data:") {
		data, err := decodeDataURL(source)
		if err != nil {
			return nil, err
		}
		return normalizePNG(data)
	}

	if err := client.call(ctx, "Page.enable", nil, nil); err != nil {
		return nil, fmt.Errorf("enable page agent: %w", err)
	}

	var frameResult struct {
		FrameTree struct {
			Frame struct {
				ID string `json:"id"`
			} `json:"frame"`
		} `json:"frameTree"`
	}
	if err := client.call(ctx, "Page.getFrameTree", nil, &frameResult); err != nil {
		return nil, fmt.Errorf("get page frame: %w", err)
	}
	frameID := frameResult.FrameTree.Frame.ID
	if frameID == "" {
		return nil, errors.New("get page frame: frame id is empty")
	}

	var resource struct {
		Content       string `json:"content"`
		Base64Encoded bool   `json:"base64Encoded"`
	}
	if err := client.call(ctx, "Page.getResourceContent", map[string]any{
		"frameId": frameID,
		"url":     source,
	}, &resource); err != nil {
		return nil, fmt.Errorf("get QR resource content: %w", err)
	}
	var data []byte
	var err error
	if resource.Base64Encoded {
		data, err = base64.StdEncoding.DecodeString(resource.Content)
		if err != nil {
			return nil, fmt.Errorf("decode QR resource: %w", err)
		}
	} else {
		data = []byte(resource.Content)
	}
	return normalizePNG(data)
}

func decodeDataURL(source string) ([]byte, error) {
	header, payload, ok := strings.Cut(source, ",")
	if !ok {
		return nil, errors.New("decode QR data URL: missing payload")
	}
	if strings.HasSuffix(strings.ToLower(header), ";base64") {
		data, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return nil, fmt.Errorf("decode QR data URL: %w", err)
		}
		return data, nil
	}
	data, err := neturl.PathUnescape(payload)
	if err != nil {
		return nil, fmt.Errorf("decode QR data URL: %w", err)
	}
	return []byte(data), nil
}

func normalizePNG(data []byte) ([]byte, error) {
	const maxQRCodeBytes = 5 << 20
	if len(data) == 0 {
		return nil, errors.New("QR image is empty")
	}
	if len(data) > maxQRCodeBytes {
		return nil, fmt.Errorf("QR image is too large: %d bytes", len(data))
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode QR image config: %w", err)
	}
	if config.Width < 80 || config.Height < 80 || config.Width > 2048 || config.Height > 2048 {
		return nil, fmt.Errorf("unexpected QR image dimensions: %dx%d", config.Width, config.Height)
	}
	if bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		return data, nil
	}

	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode QR image: %w", err)
	}
	var output bytes.Buffer
	if err := png.Encode(&output, decoded); err != nil {
		return nil, fmt.Errorf("encode QR image as PNG: %w", err)
	}
	return output.Bytes(), nil
}

type loginQRCodeTarget struct {
	Found  bool    `json:"found"`
	Source string  `json:"source"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

func (target loginQRCodeTarget) valid() bool {
	return target.Found &&
		!math.IsNaN(target.X) &&
		!math.IsNaN(target.Y) &&
		!math.IsNaN(target.Width) &&
		!math.IsNaN(target.Height) &&
		target.X >= 0 &&
		target.Y >= 0 &&
		target.Width >= 80 &&
		target.Height >= 80 &&
		target.Width <= 2048 &&
		target.Height <= 2048
}

func (target loginQRCodeTarget) screenshotScale() float64 {
	const (
		preferredScale = 2
		maxOutputSize  = 1024
	)
	scale := math.Min(preferredScale, maxOutputSize/math.Max(target.Width, target.Height))
	if scale < 1 {
		return 1
	}
	return scale
}

const dismissLoginOverlayExpression = `(() => {
  const visible = (element) => {
    if (!(element instanceof HTMLElement)) return false;
    const style = window.getComputedStyle(element);
    const rect = element.getBoundingClientRect();
    return style.display !== 'none' &&
      style.visibility !== 'hidden' &&
      rect.width > 0 &&
      rect.height > 0;
  };
  const candidates = [...document.querySelectorAll('button, [role="button"], a, div, span')];
  const confirm = candidates.find((element) =>
    visible(element) && String(element.textContent || '').trim() === '我知道了'
  );
  if (!(confirm instanceof HTMLElement)) return false;
  confirm.click();
  return true;
})()`

const refreshExpiredLoginQRCodeExpression = `(() => {
  const normalize = (value) => String(value || '').replace(/\s+/g, ' ').trim();
  const bodyText = normalize(document.body?.innerText || '');
  const expired = bodyText.includes('二维码已过期') ||
    bodyText.includes('二维码已失效') ||
    bodyText.includes('二维码失效');
  if (!expired) return false;
  const visible = (element) => {
    if (!(element instanceof HTMLElement)) return false;
    const style = window.getComputedStyle(element);
    const rect = element.getBoundingClientRect();
    return style.display !== 'none' &&
      style.visibility !== 'hidden' &&
      rect.width > 0 &&
      rect.height > 0;
  };
  const candidates = [...document.querySelectorAll('button, [role="button"], a, div, span')];
  const refresh = candidates.find((element) => {
    const text = normalize(element.textContent || '');
    return visible(element) && (text === '重新刷新' || text === '刷新二维码');
  });
  if (!(refresh instanceof HTMLElement)) return false;
  refresh.click();
  return true;
})()`

const findLoginQRCodeExpression = `(() => {
  const visible = (element) => {
    if (!(element instanceof Element)) return false;
    const style = window.getComputedStyle(element);
    const rect = element.getBoundingClientRect();
    return style.display !== 'none' &&
      style.visibility !== 'hidden' &&
      Number(style.opacity || 1) > 0 &&
      rect.width >= 80 &&
      rect.height >= 80;
  };
  const selectors = [
    'img.qrcode_img',
    '.qrcode img',
    'img[src*="qr.weibo.cn"]',
    '[class*="qrcode" i] img',
    '[class*="qrcode" i] canvas'
  ];
  const qr = selectors
    .flatMap((selector) => [...document.querySelectorAll(selector)])
    .find(visible);
  if (!qr) return { found: false, source: '', x: 0, y: 0, width: 0, height: 0 };
  const rect = qr.getBoundingClientRect();
  return {
    found: true,
    source: qr instanceof HTMLImageElement ? String(qr.currentSrc || qr.src || '') : '',
    x: Math.max(0, rect.left + window.scrollX),
    y: Math.max(0, rect.top + window.scrollY),
    width: rect.width,
    height: rect.height
  };
})()`

func getCurrentPageProbe(ctx context.Context, port int) (chromeTarget, PageProbe) {
	probe := PageProbe{
		Status:    "page_probe_failed",
		CheckedAt: time.Now(),
	}

	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, debugURL(port, "/json/list"), nil)
	if err != nil {
		probe.Error = err.Error()
		return chromeTarget{}, probe
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		probe.Error = err.Error()
		return chromeTarget{}, probe
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		probe.Error = fmt.Sprintf("debug endpoint returned HTTP %d", resp.StatusCode)
		return chromeTarget{}, probe
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		probe.Error = err.Error()
		return chromeTarget{}, probe
	}

	var targets []chromeTarget
	if err := json.Unmarshal(body, &targets); err != nil {
		probe.Error = err.Error()
		return chromeTarget{}, probe
	}

	target, ok := selectPageTarget(targets)
	if !ok {
		probe.Error = "no page target found"
		return chromeTarget{}, probe
	}

	probe.URL = target.URL
	probe.Title = target.Title
	probe.Status = classifyPage(target.URL, target.Title)
	if probe.Status == "page_ok" && target.WebSocketDebuggerURL != "" {
		loginRequired, err := detectLoginRequiredInPage(ctx, target.WebSocketDebuggerURL)
		if err != nil {
			probe.Status = "page_probe_failed"
			probe.Error = "detect embedded login page: " + err.Error()
			return target, probe
		}
		if loginRequired {
			probe.Status = "login_required"
		}
	}
	return target, probe
}

func ensureTargetGroupSelected(
	ctx context.Context,
	opts MonitorOptions,
	target chromeTarget,
	probe PageProbe,
	targetGroup TargetGroup,
) (chromeTarget, PageProbe, string, error) {
	targetConfigured := strings.TrimSpace(targetGroup.ID) != "" || strings.TrimSpace(targetGroup.Name) != ""
	if !targetConfigured {
		return target, probe, "no_target_configured", nil
	}
	lastProbeReportedAt := time.Now()
	for {
		currentTarget, currentProbe := getCurrentPageProbe(ctx, opts.DebugPort)
		if currentProbe.Status == "login_required" {
			var err error
			currentTarget, currentProbe, err = waitForAuthenticatedChatPage(ctx, opts)
			if err != nil {
				return target, currentProbe, "login_required", err
			}
			lastProbeReportedAt = time.Now()
		}
		if currentProbe.Status != "page_ok" || currentTarget.WebSocketDebuggerURL == "" {
			selectStatus := "page_not_ready:" + currentProbe.Status
			currentProbe.GroupSelectStatus = selectStatus
			if time.Since(lastProbeReportedAt) >= opts.HealthCheckInterval {
				reportPageProbe(ctx, opts, currentProbe)
				lastProbeReportedAt = time.Now()
			}
			target = currentTarget
			probe = currentProbe
			slog.Info("chat page not ready while selecting target group; retrying",
				"status", currentProbe.Status,
				"error", currentProbe.Error,
				"retryIn", "3s",
			)
			select {
			case <-ctx.Done():
				return target, probe, selectStatus, ctx.Err()
			case <-time.After(3 * time.Second):
			}
			continue
		}

		target = currentTarget
		probe = currentProbe
		selectCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		selectStatus, selectErr := selectTargetGroup(selectCtx, target.WebSocketDebuggerURL, targetGroup)
		cancel()
		probe.GroupSelectStatus = selectStatus
		if selectErr != nil {
			probe.GroupSelectError = selectErr.Error()
		}
		if selectErr == nil && (strings.HasPrefix(selectStatus, "clicked") || strings.HasPrefix(selectStatus, "already_selected")) {
			if strings.HasPrefix(selectStatus, "clicked") {
				select {
				case <-ctx.Done():
					return target, probe, selectStatus, ctx.Err()
				case <-time.After(2 * time.Second):
				}
			}
			return target, probe, selectStatus, nil
		}
		if time.Since(lastProbeReportedAt) >= opts.HealthCheckInterval {
			reportPageProbe(ctx, opts, probe)
			lastProbeReportedAt = time.Now()
		}
		errorText := ""
		if selectErr != nil {
			errorText = selectErr.Error()
		}
		slog.Info("target group not ready; retrying", "status", selectStatus, "error", errorText, "retryIn", "3s")
		select {
		case <-ctx.Done():
			return target, probe, selectStatus, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

func probePage(ctx context.Context, port int, targetGroup TargetGroup) PageProbe {
	probe := PageProbe{
		Status:    "page_probe_failed",
		CheckedAt: time.Now(),
	}

	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, debugURL(port, "/json/list"), nil)
	if err != nil {
		probe.Error = err.Error()
		return probe
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		probe.Error = err.Error()
		return probe
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		probe.Error = fmt.Sprintf("debug endpoint returned HTTP %d", resp.StatusCode)
		return probe
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		probe.Error = err.Error()
		return probe
	}

	var targets []chromeTarget
	if err := json.Unmarshal(body, &targets); err != nil {
		probe.Error = err.Error()
		return probe
	}

	target, ok := selectPageTarget(targets)
	if !ok {
		probe.Error = "no page target found"
		return probe
	}

	probe.URL = target.URL
	probe.Title = target.Title
	probe.Status = classifyPage(target.URL, target.Title)
	if probe.Status == "page_ok" && target.WebSocketDebuggerURL != "" {
		targetConfigured := strings.TrimSpace(targetGroup.ID) != "" || strings.TrimSpace(targetGroup.Name) != ""
		if targetConfigured {
			for {
				selectStatus, selectErr := selectTargetGroup(ctx, target.WebSocketDebuggerURL, targetGroup)
				probe.GroupSelectStatus = selectStatus
				probe.GroupSelectError = ""
				if selectErr != nil {
					probe.GroupSelectError = selectErr.Error()
				}

				targetReady := selectErr == nil && (strings.HasPrefix(selectStatus, "clicked") || strings.HasPrefix(selectStatus, "already_selected"))
				if targetReady {
					break
				}

				slog.Info("target group not ready; retrying", "status", selectStatus, "error", probe.GroupSelectError, "retryIn", "3s")
				select {
				case <-ctx.Done():
					probe.GroupSelectError = ctx.Err().Error()
					return probe
				case <-time.After(3 * time.Second):
				}
			}
		} else {
			selectStatus, selectErr := selectTargetGroup(ctx, target.WebSocketDebuggerURL, targetGroup)
			probe.GroupSelectStatus = selectStatus
			if selectErr != nil {
				probe.GroupSelectError = selectErr.Error()
			}
		}
		if strings.HasPrefix(probe.GroupSelectStatus, "clicked") {
			time.Sleep(2 * time.Second)
		}

		candidates, err := extractMessageCandidates(ctx, target.WebSocketDebuggerURL)
		if err != nil {
			probe.CandidateError = err.Error()
		} else {
			probe.MessageCandidates = candidates
		}
	}
	return probe
}

func selectPageTarget(targets []chromeTarget) (chromeTarget, bool) {
	var firstPage chromeTarget
	for _, target := range targets {
		if target.Type != "page" {
			continue
		}
		if firstPage.URL == "" {
			firstPage = target
		}
		if strings.Contains(target.URL, "api.weibo.com/chat") {
			return target, true
		}
	}
	if firstPage.URL != "" {
		return firstPage, true
	}
	return chromeTarget{}, false
}

func classifyPage(url string, title string) string {
	lowerURL := strings.ToLower(url)
	if strings.Contains(lowerURL, "passport.weibo") ||
		strings.Contains(lowerURL, "login.sina") ||
		strings.Contains(lowerURL, "login") ||
		strings.Contains(title, "登录") {
		return "login_required"
	}
	if strings.Contains(lowerURL, "api.weibo.com/chat") {
		return "page_ok"
	}
	return "page_open"
}

func detectLoginRequiredInPage(ctx context.Context, webSocketDebuggerURL string) (bool, error) {
	detectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client, err := newCDPClient(detectCtx, webSocketDebuggerURL)
	if err != nil {
		return false, err
	}
	defer client.Close()

	var required bool
	if err := client.evaluate(detectCtx, embeddedLoginDetectionExpression, &required); err != nil {
		return false, err
	}
	return required, nil
}

const embeddedLoginDetectionExpression = `(() => {
  const visible = (element) => {
    if (!(element instanceof Element)) return false;
    const style = window.getComputedStyle(element);
    const rect = element.getBoundingClientRect();
    return style.display !== 'none' &&
      style.visibility !== 'hidden' &&
      Number(style.opacity || 1) > 0 &&
      rect.width > 10 &&
      rect.height > 10;
  };
  const qr = document.querySelector(
    'img.qrcode_img, .qrcode img, img[src*="qr.weibo.cn"], [class*="qrcode" i] img'
  );
  if (visible(qr)) return true;
  const login = document.querySelector('.login, [class*="login" i]');
  const text = String(login?.innerText || document.body?.innerText || '');
  const bodyText = String(document.body?.innerText || '');
  if (bodyText.includes('二维码已过期') ||
      bodyText.includes('二维码已失效') ||
      bodyText.includes('二维码失效')) {
    return true;
  }
  return visible(login) &&
    (text.includes('用微博手机版扫描登录聊天') || text.includes('扫描登录'));
})()`

func reloadPage(ctx context.Context, webSocketDebuggerURL string) error {
	reloadCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	client, err := newCDPClient(reloadCtx, webSocketDebuggerURL)
	if err != nil {
		return err
	}
	defer client.Close()
	return client.call(reloadCtx, "Page.reload", map[string]any{"ignoreCache": false}, nil)
}

func debugURL(port int, path string) string {
	return fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
}

func stopProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(os.Interrupt)
	time.Sleep(500 * time.Millisecond)
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		_ = cmd.Process.Kill()
	}
}

func extractMessageCandidates(ctx context.Context, webSocketDebuggerURL string) ([]MessageCandidate, error) {
	evalCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	client, err := newCDPClient(evalCtx, webSocketDebuggerURL)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	var candidates []MessageCandidate
	if err := client.evaluate(evalCtx, messageCandidateExpression, &candidates); err != nil {
		return nil, err
	}
	return candidates, nil
}

type networkRequestInfo struct {
	URL          string
	RequestURL   string
	ResourceType string
	Status       int
	MimeType     string
	LastSeenAt   time.Time
}

const maxTrackedNetworkRequests = 10000

var monitoredPageBlockedURLs = []string{
	"*conchfairy.sinajs.cn/lib/chat.mp3*",
	"*upload.api.weibo.com/2/mss/msget?*",
}

const suppressMonitoredPageMediaExpression = `(() => {
  if (globalThis.__weiboMonitorMediaSuppressionInstalled) {
    return 0;
  }
  globalThis.__weiboMonitorMediaSuppressionInstalled = true;

  const suppress = (root) => {
    const nodes = [];
    if (root instanceof HTMLMediaElement) nodes.push(root);
    if (root instanceof HTMLSourceElement && root.parentElement instanceof HTMLMediaElement) {
      nodes.push(root.parentElement);
    }
    if (root?.querySelectorAll) nodes.push(...root.querySelectorAll('audio,video'));
    for (const media of new Set(nodes)) {
      try { media.pause(); } catch (_) {}
      media.autoplay = false;
      media.muted = true;
      media.removeAttribute('autoplay');
      media.removeAttribute('src');
      for (const source of media.querySelectorAll('source')) source.removeAttribute('src');
      try { media.load(); } catch (_) {}
    }
    return nodes.length;
  };

  HTMLMediaElement.prototype.play = function () {
    try { this.pause(); } catch (_) {}
    return Promise.resolve();
  };
  const observer = new MutationObserver((records) => {
    for (const record of records) {
      suppress(record.target);
      for (const node of record.addedNodes) suppress(node);
    }
  });
  observer.observe(document, {
    subtree: true,
    childList: true,
    attributes: true,
    attributeFilter: ['src', 'autoplay'],
  });
  return suppress(document);
})()`

func suppressMonitoredPageMedia(ctx context.Context, client *cdpClient) error {
	if err := client.call(ctx, "Network.setBlockedURLs", map[string]any{
		"urls": monitoredPageBlockedURLs,
	}, nil); err != nil {
		return fmt.Errorf("block monitored page media URLs: %w", err)
	}
	if err := client.call(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]any{
		"source": suppressMonitoredPageMediaExpression,
	}, nil); err != nil {
		return fmt.Errorf("install monitored page media suppression: %w", err)
	}
	var suppressed int
	if err := client.evaluate(ctx, suppressMonitoredPageMediaExpression, &suppressed); err != nil {
		return fmt.Errorf("suppress current monitored page media: %w", err)
	}
	slog.Info("monitored page media playback disabled",
		"currentMediaElements", suppressed,
		"blockedURLPatterns", len(monitoredPageBlockedURLs),
	)
	return nil
}

func monitorNetworkEvents(
	ctx context.Context,
	webSocketDebuggerURL string,
	onMessage func(context.Context, NetworkMessage) error,
	onImageReference func(context.Context, NetworkImageReference) error,
	onMessagesParsed func(int),
	onTrackedRequests func(int),
	onWebSocketState func(connected bool, reconnected bool),
	onWebSocketClosed func(),
) error {
	if strings.TrimSpace(webSocketDebuggerURL) == "" {
		return errors.New("websocket debugger URL is required")
	}

	client, err := newCDPClient(ctx, webSocketDebuggerURL)
	if err != nil {
		return err
	}
	defer client.Close()

	if err := client.call(ctx, "Network.enable", map[string]any{
		"maxTotalBufferSize":    20 * 1024 * 1024,
		"maxResourceBufferSize": 5 * 1024 * 1024,
	}, nil); err != nil {
		return fmt.Errorf("enable network domain: %w", err)
	}
	if err := suppressMonitoredPageMedia(ctx, client); err != nil {
		return err
	}
	slog.Info("network monitor enabled")

	requests := make(map[string]networkRequestInfo)
	activeWebSockets := make(map[string]struct{})
	hadWebSocket := false
	for {
		envelope, err := client.readEnvelope(ctx)
		if err != nil {
			return err
		}
		if envelope.Method == "" {
			continue
		}

		switch envelope.Method {
		case "Network.requestWillBeSent":
			var params struct {
				RequestID string `json:"requestId"`
				Type      string `json:"type"`
				Request   struct {
					URL string `json:"url"`
				} `json:"request"`
			}
			if json.Unmarshal(envelope.Params, &params) == nil {
				info := requests[params.RequestID]
				info.URL = params.Request.URL
				info.RequestURL = params.Request.URL
				if params.Type != "" {
					info.ResourceType = params.Type
				}
				rememberNetworkRequest(requests, params.RequestID, info, maxTrackedNetworkRequests)
				if onTrackedRequests != nil {
					onTrackedRequests(len(requests))
				}
			}
		case "Network.webSocketCreated":
			var params struct {
				RequestID string `json:"requestId"`
				URL       string `json:"url"`
			}
			if json.Unmarshal(envelope.Params, &params) == nil {
				wasDisconnected := hadWebSocket && len(activeWebSockets) == 0
				activeWebSockets[params.RequestID] = struct{}{}
				hadWebSocket = true
				rememberNetworkRequest(requests, params.RequestID, networkRequestInfo{URL: params.URL, RequestURL: params.URL, ResourceType: "WebSocket"}, maxTrackedNetworkRequests)
				if onTrackedRequests != nil {
					onTrackedRequests(len(requests))
				}
				slog.Info("network websocket created", "请求ID", params.RequestID, "url", networkURLLogValue(params.URL))
				if onWebSocketState != nil && len(activeWebSockets) == 1 {
					onWebSocketState(true, wasDisconnected)
				}
			}
		case "Network.webSocketClosed":
			var params struct {
				RequestID string `json:"requestId"`
			}
			if json.Unmarshal(envelope.Params, &params) == nil {
				info := requests[params.RequestID]
				delete(requests, params.RequestID)
				delete(activeWebSockets, params.RequestID)
				if onTrackedRequests != nil {
					onTrackedRequests(len(requests))
				}
				slog.Info("network websocket closed", "requestID", params.RequestID, "url", networkURLLogValue(info.URL))
				if onWebSocketClosed != nil && (strings.Contains(info.URL, "web.im.weibo.com/im") || (info.URL == "" && hadWebSocket)) {
					onWebSocketClosed()
				}
				if onWebSocketState != nil && hadWebSocket && len(activeWebSockets) == 0 {
					onWebSocketState(false, false)
				}
			}
		case "Network.webSocketFrameReceived":
			var params struct {
				RequestID string `json:"requestId"`
				Response  struct {
					Opcode      int    `json:"opcode"`
					PayloadData string `json:"payloadData"`
				} `json:"response"`
			}
			if json.Unmarshal(envelope.Params, &params) != nil {
				continue
			}
			if !looksLikeMessagePayload(params.Response.PayloadData) {
				continue
			}
			info := requests[params.RequestID]
			messages := parseNetworkMessages(params.Response.PayloadData)
			if onMessagesParsed != nil && len(messages) > 0 {
				onMessagesParsed(len(messages))
			}
			for _, message := range messages {
				if message.MessageType == "recall" {
					slog.Info("network recall event parsed",
						"groupID", message.GroupID,
						"senderID", message.SenderID,
						"contentLength", len([]rune(message.Content)),
						"contentSHA256", contentLogHash(message.Content),
					)
					slog.Debug("network recall content", "content", clipLogText(message.Content, 240))
				} else {
					slog.Info("已收到微博消息",
						"微博消息ID", message.WeiboMessageID,
						"微博群ID", message.GroupID,
						"发送人ID", message.SenderID,
						"消息类型", message.MessageType,
						"正文长度", len([]rune(message.Content)),
					)
					slog.Debug("network message content", "weiboMessageID", message.WeiboMessageID, "content", clipLogText(message.Content, 240))
				}
				if onMessage != nil {
					if err := onMessage(ctx, message); err != nil {
						slog.Warn("persist network message failed",
							"weiboMessageID", message.WeiboMessageID,
							"groupID", message.GroupID,
							"senderID", message.SenderID,
							"error", err,
						)
					}
				}
			}
			if len(messages) == 0 {
				slog.Debug("network websocket frame received",
					"requestID", params.RequestID,
					"url", networkURLLogValue(info.URL),
					"opcode", params.Response.Opcode,
					"payload", clipLogText(params.Response.PayloadData, 800),
				)
			}
		case "Network.responseReceived":
			var params struct {
				RequestID string `json:"requestId"`
				Type      string `json:"type"`
				Response  struct {
					URL      string `json:"url"`
					Status   int    `json:"status"`
					MimeType string `json:"mimeType"`
				} `json:"response"`
			}
			if json.Unmarshal(envelope.Params, &params) != nil {
				continue
			}
			info := requests[params.RequestID]
			if info.RequestURL == "" {
				info.RequestURL = params.Response.URL
			}
			info.URL = params.Response.URL
			info.ResourceType = params.Type
			info.Status = params.Response.Status
			info.MimeType = params.Response.MimeType
			rememberNetworkRequest(requests, params.RequestID, info, maxTrackedNetworkRequests)
			if onTrackedRequests != nil {
				onTrackedRequests(len(requests))
			}
			if looksLikeMessageURL(params.Response.URL) {
				slog.Debug("微博网络请求",
					"requestID", params.RequestID,
					"资源类型", params.Type,
					"状态码", params.Response.Status,
					"内容类型", params.Response.MimeType,
					"url", networkURLLogValue(params.Response.URL),
				)
			}
		case "Network.loadingFinished":
			var params struct {
				RequestID         string  `json:"requestId"`
				EncodedDataLength float64 `json:"encodedDataLength"`
			}
			if json.Unmarshal(envelope.Params, &params) != nil {
				continue
			}
			info := requests[params.RequestID]
			delete(requests, params.RequestID)
			if onTrackedRequests != nil {
				onTrackedRequests(len(requests))
			}
			if onImageReference == nil {
				continue
			}
			for _, ref := range extractImageReferencesFromURL(info.RequestURL, info.URL) {
				slog.Info("network image url reference parsed",
					"sourceURL", networkURLLogValue(ref.SourceURL),
					"fids", strings.Join(ref.FIDs, ","),
					"urls", networkURLsLogValue(ref.URLs),
				)
				if err := onImageReference(ctx, ref); err != nil {
					slog.Warn("resolve image url reference failed", "sourceURL", networkURLLogValue(ref.SourceURL), "error", err)
				}
			}
			if !shouldInspectImageReferenceResponse(info, params.EncodedDataLength) {
				continue
			}
			body, err := getNetworkResponseBody(ctx, client, params.RequestID)
			if err != nil {
				slog.Debug("read network response body failed", "requestID", params.RequestID, "url", networkURLLogValue(info.URL), "error", err)
				continue
			}
			refs := extractImageReferencesFromBody(body, info.URL)
			for _, ref := range refs {
				slog.Info("network image reference parsed",
					"sourceURL", networkURLLogValue(ref.SourceURL),
					"fids", strings.Join(ref.FIDs, ","),
					"urls", networkURLsLogValue(ref.URLs),
				)
				if err := onImageReference(ctx, ref); err != nil {
					slog.Warn("resolve image reference failed", "sourceURL", networkURLLogValue(ref.SourceURL), "error", err)
				}
			}
		case "Network.loadingFailed":
			var params struct {
				RequestID string `json:"requestId"`
				ErrorText string `json:"errorText"`
			}
			if json.Unmarshal(envelope.Params, &params) == nil {
				info := requests[params.RequestID]
				delete(requests, params.RequestID)
				if onTrackedRequests != nil {
					onTrackedRequests(len(requests))
				}
				if looksLikeMessageURL(info.URL) {
					slog.Warn("network request failed", "requestID", params.RequestID, "url", networkURLLogValue(info.URL), "error", params.ErrorText)
				}
			}
		}
	}
}

func networkURLLogValue(rawURL string) string {
	const maxLogURLLength = 320

	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	lower := strings.ToLower(rawURL)
	if strings.HasPrefix(lower, "data:") {
		header, payload, _ := strings.Cut(rawURL, ",")
		return fmt.Sprintf("%s,<payload:%d chars>", clipLogText(header, 80), len(payload))
	}
	if strings.HasPrefix(lower, "blob:") {
		return "blob:<redacted>"
	}

	parsed, err := neturl.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return fmt.Sprintf("<invalid-or-relative-url:%d chars>", len(rawURL))
	}
	parsed.User = nil
	parsed.Fragment = ""
	originalQuery := parsed.Query()
	safeQuery := neturl.Values{}
	for _, key := range []string{
		"fid", "gid", "id", "count", "max_mid", "source", "type",
		"width", "high", "size", "imageType", "convert_emoji", "query_sender",
	} {
		for _, value := range originalQuery[key] {
			safeQuery.Add(key, clipLogText(value, 80))
		}
	}
	redactedKeys := 0
	for key := range originalQuery {
		if _, preserved := safeQuery[key]; !preserved {
			redactedKeys++
		}
	}
	parsed.RawQuery = safeQuery.Encode()
	result := parsed.String()
	if redactedKeys > 0 {
		result += fmt.Sprintf(" [redacted-query-keys:%d]", redactedKeys)
	}
	return clipLogText(result, maxLogURLLength)
}

func networkURLsLogValue(urls []string) string {
	const maxLoggedURLs = 5
	if len(urls) == 0 {
		return ""
	}
	limit := len(urls)
	if limit > maxLoggedURLs {
		limit = maxLoggedURLs
	}
	values := make([]string, 0, limit)
	for _, rawURL := range urls[:limit] {
		values = append(values, networkURLLogValue(rawURL))
	}
	result := strings.Join(values, ",")
	if len(urls) > limit {
		result += fmt.Sprintf(" [and %d more]", len(urls)-limit)
	}
	return clipLogText(result, 1000)
}

func rememberNetworkRequest(requests map[string]networkRequestInfo, requestID string, info networkRequestInfo, maxItems int) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return
	}
	if maxItems <= 0 {
		maxItems = maxTrackedNetworkRequests
	}
	if _, exists := requests[requestID]; !exists && len(requests) >= maxItems {
		type trackedRequestAge struct {
			id       string
			lastSeen time.Time
		}
		ages := make([]trackedRequestAge, 0, len(requests))
		for id, candidate := range requests {
			ages = append(ages, trackedRequestAge{id: id, lastSeen: candidate.LastSeenAt})
		}
		sort.Slice(ages, func(i, j int) bool {
			return ages[i].lastSeen.Before(ages[j].lastSeen)
		})
		removeCount := maxItems / 10
		if removeCount < 1 {
			removeCount = 1
		}
		for index := 0; index < removeCount && index < len(ages); index++ {
			delete(requests, ages[index].id)
		}
	}
	info.LastSeenAt = time.Now()
	requests[requestID] = info
}

func shouldInspectImageReferenceResponse(info networkRequestInfo, encodedLength float64) bool {
	if info.Status < 200 || info.Status >= 300 {
		return false
	}
	if encodedLength > 5*1024*1024 {
		return false
	}
	resourceType := strings.ToLower(info.ResourceType)
	mimeType := strings.ToLower(info.MimeType)
	if resourceType == "xhr" || resourceType == "fetch" {
		return true
	}
	if strings.Contains(mimeType, "json") || strings.Contains(mimeType, "javascript") || strings.Contains(mimeType, "text") {
		return true
	}
	return looksLikeMessageURL(info.URL)
}

func getNetworkResponseBody(ctx context.Context, client *cdpClient, requestID string) (string, error) {
	var result struct {
		Body          string `json:"body"`
		Base64Encoded bool   `json:"base64Encoded"`
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.call(callCtx, "Network.getResponseBody", map[string]any{
		"requestId": requestID,
	}, &result); err != nil {
		return "", err
	}
	if !result.Base64Encoded {
		return result.Body, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(result.Body)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func extractImageReferencesFromBody(body string, sourceURL string) []NetworkImageReference {
	if !looksLikeImageReferenceBody(body) {
		return nil
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return extractImageReferencesFromText(body, sourceURL)
	}
	collector := imageReferenceCollector{
		sourceURL: sourceURL,
		seen:      make(map[string]bool),
	}
	collector.walk(value, nil)
	return collector.refs
}

func extractImageReferencesFromURL(sourceURL string, imageURL string) []NetworkImageReference {
	if !looksLikeImageURL(imageURL) || isDecorativeImageURL(imageURL) {
		return nil
	}
	fids := appendUniqueStrings(nil, collectFIDValuesFromURL(sourceURL)...)
	fids = appendUniqueStrings(fids, collectFIDValuesFromURL(imageURL)...)
	if len(fids) == 0 {
		return nil
	}
	return []NetworkImageReference{{
		FIDs:      fids,
		URLs:      []string{strings.TrimSpace(imageURL)},
		SourceURL: sourceURL,
	}}
}

func ExtractImageReferencesFromURL(sourceURL string, imageURL string) []NetworkImageReference {
	return extractImageReferencesFromURL(sourceURL, imageURL)
}

func extractImageReferencesFromText(body string, sourceURL string) []NetworkImageReference {
	urls := imageURLPattern.FindAllString(body, -1)
	fids := fidTextPattern.FindAllStringSubmatch(body, -1)
	if len(urls) == 0 || len(fids) == 0 {
		return nil
	}
	fidValues := make([]string, 0, len(fids))
	for _, match := range fids {
		if len(match) > 1 {
			fidValues = appendUniqueStrings(fidValues, match[1])
		}
	}
	imageURLs := make([]string, 0, len(urls))
	for _, rawURL := range urls {
		imageURLs = appendUniqueStrings(imageURLs, strings.Trim(rawURL, `"'<>`))
	}
	if len(fidValues) == 0 || len(imageURLs) == 0 || len(fidValues)*len(imageURLs) > 40 {
		return nil
	}
	refs := make([]NetworkImageReference, 0, len(fidValues))
	for _, fid := range fidValues {
		refs = append(refs, NetworkImageReference{
			FIDs:      []string{fid},
			URLs:      imageURLs,
			SourceURL: sourceURL,
		})
	}
	return refs
}

func collectFIDValuesFromURL(rawURL string) []string {
	parsed, err := neturl.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil
	}
	values := make([]string, 0)
	for key, items := range parsed.Query() {
		if !isFIDKey(strings.ToLower(key)) {
			continue
		}
		for _, item := range items {
			values = appendUniqueStrings(values, item)
		}
	}
	for _, match := range fidURLPattern.FindAllStringSubmatch(rawURL, -1) {
		if len(match) > 1 {
			values = appendUniqueStrings(values, match[1])
		}
	}
	return values
}

func looksLikeImageReferenceBody(body string) bool {
	lower := strings.ToLower(body)
	return (strings.Contains(lower, "fid") ||
		strings.Contains(lower, "pic_id") ||
		strings.Contains(lower, "picid") ||
		strings.Contains(lower, "\"pid\"")) &&
		(strings.Contains(lower, "sinaimg.cn") ||
			strings.Contains(lower, "msget_thumbnail") ||
			strings.Contains(lower, "upload.api.weibo.com") ||
			strings.Contains(lower, ".jpg") ||
			strings.Contains(lower, ".jpeg") ||
			strings.Contains(lower, ".png") ||
			strings.Contains(lower, ".webp") ||
			strings.Contains(lower, ".gif"))
}

var (
	imageURLPattern = regexp.MustCompile(`https?://[^"'\s<>]+?(?:(?:\.jpg|\.jpeg|\.png|\.webp|\.gif)(?:\?[^"'\s<>]*)?|msget_thumbnail\?[^"'\s<>]+)`)
	fidTextPattern  = regexp.MustCompile(`(?i)(?:fid|fids|fidstr|file[_-]?id|pic[_-]?id|picid|pid)["'\s:=\[]+([A-Za-z0-9._:-]+)`)
	fidURLPattern   = regexp.MustCompile(`(?i)(?:fid|fileid|pic[_-]?id|picid|pid)[=/_-]([A-Za-z0-9.:-]+)`)
)

type imageReferenceCollector struct {
	sourceURL string
	seen      map[string]bool
	refs      []NetworkImageReference
}

type imageReferenceScope struct {
	fids []string
	urls []string
}

func (c *imageReferenceCollector) walk(value any, path []string) imageReferenceScope {
	switch typed := value.(type) {
	case map[string]any:
		scope := imageReferenceScope{}
		for key, child := range typed {
			lowerKey := strings.ToLower(key)
			nextPath := append(path, lowerKey)
			if isFIDKey(lowerKey) {
				scope.fids = appendUniqueStrings(scope.fids, collectFIDValues(child)...)
			}
			if !skipImageURLPath(nextPath) {
				scope.urls = appendUniqueStrings(scope.urls, collectImageURLValues(child, nextPath)...)
			}
			childScope := c.walk(child, nextPath)
			scope.fids = appendUniqueStrings(scope.fids, childScope.fids...)
			scope.urls = appendUniqueStrings(scope.urls, childScope.urls...)
		}
		c.emit(scope)
		return scope
	case []any:
		scope := imageReferenceScope{}
		containsFIDs := false
		for _, child := range typed {
			childScope := c.walk(child, path)
			if len(childScope.fids) > 0 {
				containsFIDs = true
			}
			scope.fids = appendUniqueStrings(scope.fids, childScope.fids...)
			scope.urls = appendUniqueStrings(scope.urls, childScope.urls...)
		}
		if containsFIDs {
			return imageReferenceScope{}
		}
		return scope
	case string:
		if looksLikeImageURL(typed) && !skipImageURLPath(path) && !isDecorativeImageURL(typed) {
			return imageReferenceScope{urls: []string{typed}}
		}
	}
	return imageReferenceScope{}
}

func (c *imageReferenceCollector) emit(scope imageReferenceScope) {
	if len(scope.fids) == 0 || len(scope.urls) == 0 {
		return
	}
	if len(scope.fids)*len(scope.urls) > 40 {
		return
	}
	filteredURLs := make([]string, 0, len(scope.urls))
	for _, rawURL := range scope.urls {
		if strings.TrimSpace(rawURL) != "" && !isDecorativeImageURL(rawURL) {
			filteredURLs = appendUniqueStrings(filteredURLs, strings.TrimSpace(rawURL))
		}
	}
	if len(filteredURLs) == 0 {
		return
	}
	for _, fid := range scope.fids {
		key := fid + "|" + strings.Join(filteredURLs, "\x00")
		if c.seen[key] {
			continue
		}
		c.seen[key] = true
		c.refs = append(c.refs, NetworkImageReference{
			FIDs:      []string{fid},
			URLs:      filteredURLs,
			SourceURL: c.sourceURL,
		})
	}
}

func isFIDKey(key string) bool {
	switch key {
	case "fid", "fids", "fidstr", "fid_string", "file_id", "fileid", "pic_id", "picid", "pic_ids", "picids", "pid":
		return true
	default:
		return false
	}
}

func collectFIDValues(value any) []string {
	switch typed := value.(type) {
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			values = appendUniqueStrings(values, collectFIDValues(item)...)
		}
		return values
	case json.Number:
		return []string{typed.String()}
	case float64:
		if typed > 0 && typed == float64(int64(typed)) {
			return []string{fmt.Sprintf("%.0f", typed)}
		}
	case string:
		value := strings.TrimSpace(typed)
		if value != "" {
			return []string{value}
		}
	}
	return nil
}

func collectImageURLValues(value any, path []string) []string {
	switch typed := value.(type) {
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			values = appendUniqueStrings(values, collectImageURLValues(item, path)...)
		}
		return values
	case map[string]any:
		values := make([]string, 0)
		for key, child := range typed {
			values = appendUniqueStrings(values, collectImageURLValues(child, append(path, strings.ToLower(key)))...)
		}
		return values
	case string:
		if looksLikeImageURL(typed) && !skipImageURLPath(path) && !isDecorativeImageURL(typed) {
			return []string{strings.TrimSpace(typed)}
		}
	}
	return nil
}

func appendUniqueStrings(values []string, next ...string) []string {
	for _, value := range next {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		exists := false
		for _, current := range values {
			if current == value {
				exists = true
				break
			}
		}
		if !exists {
			values = append(values, value)
		}
	}
	return values
}

func parseNetworkMessages(payload string) []NetworkMessage {
	var events []json.RawMessage
	if err := json.Unmarshal([]byte(payload), &events); err != nil {
		return nil
	}

	messages := make([]NetworkMessage, 0, len(events))
	for _, rawEvent := range events {
		message, ok := parseNetworkMessageEvent(rawEvent)
		if ok {
			messages = append(messages, message)
		}
	}
	return messages
}

func parseNetworkMessageEvent(rawEvent json.RawMessage) (NetworkMessage, bool) {
	var event struct {
		Data struct {
			SubType int             `json:"sub_type"`
			Type    string          `json:"type"`
			Info    json.RawMessage `json:"info"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rawEvent, &event); err != nil {
		return NetworkMessage{}, false
	}
	if event.Data.Type != "groupchat" || len(event.Data.Info) == 0 {
		return NetworkMessage{}, false
	}

	switch event.Data.SubType {
	case 331:
		if message, ok := parseGroupChatRecallMessage(rawEvent, event.Data.Info); ok {
			return message, true
		}
	default:
	}
	return parseGroupChatMessage(rawEvent, event.Data.Info, event.Data.SubType)
}

func parseGroupChatMessage(rawEvent json.RawMessage, rawInfo json.RawMessage, eventSubType int) (NetworkMessage, bool) {
	var info struct {
		ID             json.Number   `json:"id"`
		GID            json.Number   `json:"gid"`
		GroupName      string        `json:"group_name"`
		Content        string        `json:"content"`
		FromUID        json.Number   `json:"from_uid"`
		CurrentVersion json.Number   `json:"current_version"`
		FIDs           []json.Number `json:"fids"`
		MediaType      int           `json:"media_type"`
		Type           int           `json:"type"`
		SubType        int           `json:"sub_type"`
		Template       string        `json:"template"`
		Time           int64         `json:"time"`
		Data           map[string]struct {
			Value string `json:"value"`
		} `json:"data"`
		Annotations struct {
			PushProcExt struct {
				SendTime int64 `json:"send_time"`
			} `json:"push_proc_ext"`
			VideoPicFID json.Number `json:"video_pic_fid"`
		} `json:"annotations"`
		FromUser struct {
			ScreenName      string `json:"screen_name"`
			ProfileImageURL string `json:"profile_image_url"`
			AvatarLarge     string `json:"avatar_large"`
		} `json:"from_user"`
	}
	if err := json.Unmarshal(rawInfo, &info); err != nil {
		return NetworkMessage{}, false
	}

	groupID := info.GID.String()
	senderID := info.FromUID.String()
	weiboMessageID := info.ID.String()
	if weiboMessageID == "" || weiboMessageID == "0" {
		weiboMessageID = info.CurrentVersion.String()
	}
	effectiveSubType := info.SubType
	if effectiveSubType == 0 {
		effectiveSubType = eventSubType
	}
	if senderID == "" || senderID == "0" {
		senderID = fmt.Sprintf("weibo-system:%d", effectiveSubType)
	}
	if weiboMessageID == "" || weiboMessageID == "0" {
		weiboMessageID = fmt.Sprintf("event:%x", sha256.Sum256(rawEvent))
	}
	attachments := mediaAttachmentsFromInfo(rawInfo, info.MediaType, info.FIDs, info.Annotations.VideoPicFID)
	content := strings.TrimSpace(info.Content)
	if content == "" {
		content = renderMessageTemplate(info.Template, info.Data)
	}
	if groupID == "" || groupID == "0" {
		return NetworkMessage{}, false
	}

	var sentAt *time.Time
	if info.Annotations.PushProcExt.SendTime > 0 {
		value := time.UnixMilli(info.Annotations.PushProcExt.SendTime)
		sentAt = &value
	} else if info.Time > 0 {
		value := time.Unix(info.Time, 0)
		sentAt = &value
	}
	avatar := info.FromUser.ProfileImageURL
	if avatar == "" {
		avatar = info.FromUser.AvatarLarge
	}
	messageType := messageTypeFromGroupChatInfo(info.Type, info.MediaType, len(attachments), "text")
	if content == "" && len(attachments) == 0 {
		messageType = "event"
	} else if strings.TrimSpace(info.Template) != "" || effectiveSubType == 101 {
		messageType = "system"
	}
	senderName := strings.TrimSpace(info.FromUser.ScreenName)
	if senderName == "" && strings.HasPrefix(senderID, "weibo-system:") {
		senderName = "微博系统消息"
	}

	return NetworkMessage{
		WeiboMessageID: weiboMessageID,
		GroupID:        groupID,
		GroupName:      strings.TrimSpace(info.GroupName),
		SenderID:       senderID,
		SenderName:     senderName,
		SenderAvatar:   strings.TrimSpace(avatar),
		MessageType:    messageType,
		Content:        content,
		SentAt:         sentAt,
		Source:         "weibo_websocket",
		DedupeKey:      "weibo_message:" + weiboMessageID,
		Attachments:    attachments,
		Raw:            rawEvent,
	}, true
}

func renderMessageTemplate(template string, data map[string]struct {
	Value string `json:"value"`
}) string {
	result := strings.TrimSpace(template)
	for key, value := range data {
		result = strings.ReplaceAll(result, "{{"+key+".DATA}}", value.Value)
	}
	return strings.TrimSpace(result)
}

func parseGroupChatRecallMessage(rawEvent json.RawMessage, rawInfo json.RawMessage) (NetworkMessage, bool) {
	var info struct {
		GID        json.Number   `json:"gid"`
		RecallText string        `json:"recall_text"`
		FromUID    json.Number   `json:"from_uid"`
		Time       int64         `json:"time"`
		IDs        []json.Number `json:"ids"`
		FromUser   struct {
			ScreenName      string `json:"screen_name"`
			ProfileImageURL string `json:"profile_image_url"`
			AvatarLarge     string `json:"avatar_large"`
		} `json:"from_user"`
	}
	if err := json.Unmarshal(rawInfo, &info); err != nil {
		return NetworkMessage{}, false
	}
	groupID := info.GID.String()
	senderID := info.FromUID.String()
	content := strings.TrimSpace(info.RecallText)
	if groupID == "" || senderID == "" || content == "" {
		return NetworkMessage{}, false
	}

	recallID := ""
	if len(info.IDs) > 0 {
		recallID = info.IDs[0].String()
	}
	var sentAt *time.Time
	if info.Time > 0 {
		value := time.Unix(info.Time, 0)
		sentAt = &value
	}
	avatar := info.FromUser.ProfileImageURL
	if avatar == "" {
		avatar = info.FromUser.AvatarLarge
	}

	return NetworkMessage{
		WeiboMessageID: "recall:" + recallID,
		GroupID:        groupID,
		SenderID:       senderID,
		SenderName:     strings.TrimSpace(info.FromUser.ScreenName),
		SenderAvatar:   strings.TrimSpace(avatar),
		MessageType:    "recall",
		Content:        content,
		SentAt:         sentAt,
		Source:         "weibo_websocket",
		DedupeKey:      "weibo_recall:" + recallID,
		Raw:            rawEvent,
	}, true
}

func messageTypeFromGroupChatInfo(value int, mediaType int, attachmentCount int, fallback string) string {
	if mediaType == 10 {
		return "video"
	}
	if mediaType == 1 || attachmentCount > 0 {
		return "image"
	}
	switch value {
	case 321:
		return "text"
	default:
		if fallback != "" {
			return fallback
		}
		return "unknown"
	}
}

func mediaAttachmentsFromInfo(rawInfo json.RawMessage, mediaType int, fids []json.Number, videoPicFID json.Number) []NetworkAttachment {
	if mediaType == 10 {
		return videoAttachmentsFromInfo(mediaType, fids, videoPicFID)
	}
	return imageAttachmentsFromInfo(rawInfo, mediaType, fids)
}

func videoAttachmentsFromInfo(mediaType int, fids []json.Number, videoPicFID json.Number) []NetworkAttachment {
	attachments := make([]NetworkAttachment, 0, 2)
	if videoPicFID.String() != "" && videoPicFID.String() != "0" {
		attachments = append(attachments, NetworkAttachment{
			AttachmentType: "image",
			Meta:           imageAttachmentMeta(mediaType, []json.Number{videoPicFID}, ""),
		})
	}
	for _, fid := range fids {
		if fid.String() == "" || fid.String() == "0" {
			continue
		}
		attachments = append(attachments, NetworkAttachment{
			AttachmentType: "video",
			Meta:           videoAttachmentMeta(mediaType, []json.Number{fid}),
		})
	}
	return attachments
}

func imageAttachmentsFromInfo(rawInfo json.RawMessage, mediaType int, fids []json.Number) []NetworkAttachment {
	if mediaType != 1 && len(fids) == 0 {
		return nil
	}

	urls := filterMessageImageURLsByFID(extractImageURLs(rawInfo), fids)
	if len(urls) == 0 {
		attachments := make([]NetworkAttachment, 0, max(1, len(fids)))
		for _, fid := range fids {
			attachments = append(attachments, NetworkAttachment{
				AttachmentType: "image",
				Meta:           imageAttachmentMeta(mediaType, []json.Number{fid}, ""),
			})
		}
		if len(attachments) == 0 {
			return []NetworkAttachment{{
				AttachmentType: "image",
				Meta:           imageAttachmentMeta(mediaType, fids, ""),
			}}
		}
		return attachments
	}

	attachments := make([]NetworkAttachment, 0, len(urls))
	for _, url := range urls {
		attachments = append(attachments, NetworkAttachment{
			AttachmentType: "image",
			URL:            url,
			Meta:           imageAttachmentMeta(mediaType, fids, url),
		})
	}
	return attachments
}

func imageAttachmentMeta(mediaType int, fids []json.Number, url string) json.RawMessage {
	meta := map[string]any{
		"media_type": mediaType,
	}
	if len(fids) > 0 {
		values := make([]string, 0, len(fids))
		for _, fid := range fids {
			if fid.String() != "" {
				values = append(values, fid.String())
			}
		}
		meta["fids"] = values
	}
	if strings.TrimSpace(url) == "" {
		meta["url_status"] = "missing_in_message_payload"
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return nil
	}
	return raw
}

func videoAttachmentMeta(mediaType int, fids []json.Number) json.RawMessage {
	meta := map[string]any{
		"media_type": mediaType,
	}
	if len(fids) > 0 {
		values := make([]string, 0, len(fids))
		for _, fid := range fids {
			if fid.String() != "" {
				values = append(values, fid.String())
			}
		}
		meta["fids"] = values
	}
	meta["url_status"] = "missing_in_message_payload"
	raw, err := json.Marshal(meta)
	if err != nil {
		return nil
	}
	return raw
}

func extractImageURLs(raw json.RawMessage) []string {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	seen := make(map[string]bool)
	urls := make([]string, 0)
	walkImageURLs(value, nil, seen, &urls)
	return urls
}

func walkImageURLs(value any, path []string, seen map[string]bool, urls *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			nextPath := append(path, strings.ToLower(key))
			if skipImageURLPath(nextPath) {
				continue
			}
			walkImageURLs(child, nextPath, seen, urls)
		}
	case []any:
		for _, child := range typed {
			walkImageURLs(child, path, seen, urls)
		}
	case string:
		if !looksLikeImageURL(typed) || isDecorativeImageURL(typed) || seen[typed] {
			return
		}
		seen[typed] = true
		*urls = append(*urls, typed)
	}
}

func skipImageURLPath(path []string) bool {
	joined := strings.Join(path, ".")
	if strings.Contains(joined, "from_user") ||
		strings.Contains(joined, "profile_image") ||
		strings.Contains(joined, "avatar") {
		return true
	}
	for _, segment := range path {
		segment = strings.ToLower(strings.TrimSpace(segment))
		switch segment {
		case "icon", "role_icon", "badge", "badge_icon", "verified_icon", "level_icon",
			"medal", "medal_icon", "pendant", "pendant_url", "frame", "frame_icon",
			"crown", "crown_icon":
			return true
		}
		if strings.HasSuffix(segment, "_role_icon") || strings.HasSuffix(segment, "_badge_icon") ||
			strings.HasSuffix(segment, "_medal_icon") || strings.HasSuffix(segment, "_level_icon") {
			return true
		}
	}
	return false
}

func filterMessageImageURLsByFID(urls []string, fids []json.Number) []string {
	if len(urls) == 0 || len(fids) == 0 {
		return urls
	}
	expected := make(map[string]bool, len(fids))
	for _, fid := range fids {
		if value := strings.TrimSpace(fid.String()); value != "" && value != "0" {
			expected[value] = true
		}
	}
	filtered := make([]string, 0, len(urls))
	for _, rawURL := range urls {
		if !isFIDAddressedImageURL(rawURL) {
			filtered = appendUniqueStrings(filtered, rawURL)
			continue
		}
		for _, urlFID := range collectFIDValuesFromURL(rawURL) {
			if expected[urlFID] {
				filtered = appendUniqueStrings(filtered, rawURL)
				break
			}
		}
	}
	return filtered
}

func isFIDAddressedImageURL(rawURL string) bool {
	parsed, err := neturl.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	return strings.HasSuffix(parsed.Path, "/msget_thumbnail") || strings.HasSuffix(parsed.Path, "/msget")
}

func looksLikeImageURL(value string) bool {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return false
	}
	parsed, err := neturl.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	lower := strings.ToLower(raw)
	return strings.Contains(lower, "sinaimg.cn") ||
		strings.Contains(lower, "upload.api.weibo.com/2/mss/msget_thumbnail") ||
		strings.Contains(lower, "api.weibo.com/2/mss/msget_thumbnail") ||
		strings.Contains(lower, "weibo.cn") && strings.Contains(lower, "image") ||
		strings.Contains(lower, ".jpg") ||
		strings.Contains(lower, ".jpeg") ||
		strings.Contains(lower, ".png") ||
		strings.Contains(lower, ".webp") ||
		strings.Contains(lower, ".gif")
}

// isDecorativeImageURL rejects fixed Weibo UI assets that can appear beside
// an image fid in history responses but are not the image addressed by that
// fid. videopicfeed*.png is the group-role crown shown next to a sender.
func isDecorativeImageURL(value string) bool {
	parsed, err := neturl.Parse(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	path := strings.ToLower(parsed.Path)
	nameIndex := strings.LastIndex(path, "/")
	if nameIndex >= 0 {
		path = path[nameIndex+1:]
	}
	return strings.HasPrefix(path, "videopicfeed") && strings.HasSuffix(path, ".png")
}

func looksLikeMessagePayload(payload string) bool {
	value := strings.TrimSpace(payload)
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	keywords := []string{
		"groupchat",
		"sender_id",
		"sender_screen_name",
		"recipient_id",
		"recipient_screen_name",
		"group_chat",
		"dm_type",
		"idstr",
		"created_at",
		"direct_message",
		"message",
	}
	for _, keyword := range keywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return (strings.HasPrefix(value, "{") || strings.HasPrefix(value, "[")) &&
		(strings.Contains(lower, "text") || strings.Contains(lower, "msg"))
}

func looksLikeMessageURL(rawURL string) bool {
	lower := strings.ToLower(rawURL)
	keywords := []string{
		"chat",
		"direct",
		"message",
		"msg",
		"im",
		"webim",
		"dm",
	}
	for _, keyword := range keywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

type TargetGroup struct {
	ID   string
	Name string
}

func selectTargetGroup(ctx context.Context, webSocketDebuggerURL string, target TargetGroup) (string, error) {
	if strings.TrimSpace(target.ID) == "" && strings.TrimSpace(target.Name) == "" {
		return "no_target_configured", nil
	}

	evalCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	client, err := newCDPClient(evalCtx, webSocketDebuggerURL)
	if err != nil {
		return "", err
	}
	defer client.Close()

	expressionBytes, err := json.Marshal(map[string]string{
		"id":   strings.TrimSpace(target.ID),
		"name": strings.TrimSpace(target.Name),
	})
	if err != nil {
		return "", err
	}

	var result struct {
		Status   string   `json:"status"`
		Reason   string   `json:"reason,omitempty"`
		Text     string   `json:"text,omitempty"`
		IDHit    bool     `json:"idHit,omitempty"`
		Sessions []string `json:"sessions,omitempty"`
	}
	expression := "(" + targetGroupSelectExpression + ")(" + string(expressionBytes) + ")"
	if err := client.evaluate(evalCtx, expression, &result); err != nil {
		return "", err
	}
	status := result.Status
	if result.Reason != "" {
		status += ":" + result.Reason
	}
	if result.Text != "" {
		slog.Debug("target group selection", "status", result.Status, "text", result.Text, "idHit", result.IDHit)
	} else {
		slog.Debug("target group selection", "status", result.Status, "reason", result.Reason, "sessions", strings.Join(result.Sessions, " | "))
	}
	return status, nil
}

func logMessageCandidates(probe PageProbe) {
	if probe.Status != "page_ok" {
		return
	}
	if probe.GroupSelectStatus != "" || probe.GroupSelectError != "" {
		slog.Info("target group select status", "status", probe.GroupSelectStatus, "error", probe.GroupSelectError)
	}
	if probe.CandidateError != "" {
		slog.Warn("message candidate probe failed", "error", probe.CandidateError)
		return
	}
	slog.Info("message candidates detected", "count", len(probe.MessageCandidates))
	limit := len(probe.MessageCandidates)
	if limit > 10 {
		limit = 10
	}
	for _, candidate := range probe.MessageCandidates[:limit] {
		slog.Debug("message candidate",
			"index", candidate.Index,
			"score", candidate.Score,
			"tag", candidate.Tag,
			"id", candidate.ID,
			"class", candidate.ClassName,
			"role", candidate.Role,
			"text", clipLogText(candidate.Text, 220),
			"images", clipImageList(candidate.Images, 180),
			"x", candidate.Rect.X,
			"y", candidate.Rect.Y,
			"width", candidate.Rect.Width,
			"height", candidate.Rect.Height,
		)
	}
	if len(probe.MessageCandidates) > limit {
		slog.Info("message candidates omitted", "count", len(probe.MessageCandidates)-limit)
	}
}

func contentLogHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:8])
}

func clipLogText(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "..."
}

func clipImageList(images []string, maxPerImage int) string {
	if len(images) == 0 {
		return ""
	}
	clipped := make([]string, 0, len(images))
	for _, image := range images {
		clipped = append(clipped, clipLogText(image, maxPerImage))
	}
	return strings.Join(clipped, ",")
}

const targetGroupSelectExpression = `(target) => {
  const normalize = (value) => String(value || '').replace(/\s+/g, ' ').trim();
  const targetID = normalize(target && target.id);
  const targetName = normalize(target && target.name);
  const isVisible = (el) => {
    const style = window.getComputedStyle(el);
    const rect = el.getBoundingClientRect();
    return style.display !== 'none' &&
      style.visibility !== 'hidden' &&
      rect.width >= 24 &&
      rect.height >= 10 &&
      rect.bottom >= 0 &&
      rect.top <= window.innerHeight;
  };
  const rightText = normalize(document.querySelector('.right')?.innerText || '');
  if (targetName && rightText.includes(targetName) && !rightText.includes('您还未选中或发起聊天')) {
    return { status: 'already_selected', text: targetName };
  }
  const findSessionByHiddenID = () => {
    if (!targetID) return null;
    for (const node of document.querySelectorAll('body *')) {
      if (!(node instanceof HTMLElement)) continue;
      const value = normalize(node.textContent || '');
      if (value !== targetID && !value.includes(targetID)) continue;
      const session = node.closest('li.sessionlist, .sessionlist, [class*="session" i], li');
      if (session instanceof HTMLElement && isVisible(session)) {
        return {
          el: session,
          text: normalize(session.innerText || session.textContent || ''),
          idHit: true,
          hiddenText: value
        };
      }
    }
    return null;
  };
  const sessionNodes = Array.from(document.querySelectorAll('li.sessionlist, .sessionlist, [class*="session" i], li'));
  let fallback = findSessionByHiddenID();
  for (const el of sessionNodes) {
    if (fallback && fallback.idHit) break;
    if (!(el instanceof HTMLElement) || !isVisible(el)) continue;
    const text = normalize(el.innerText || el.textContent || '');
    const html = String(el.outerHTML || '');
    const idHit = Boolean(targetID && html.includes(targetID));
    const nameHit = Boolean(targetName && text.includes(targetName));
    if (!idHit && !nameHit) continue;
    const rect = el.getBoundingClientRect();
    const item = { el, text, idHit, y: rect.top };
    if (idHit) {
      fallback = item;
      break;
    }
    if (!fallback) fallback = item;
  }
  if (!fallback) {
    const sessions = sessionNodes
      .filter((el) => el instanceof HTMLElement && isVisible(el))
      .map((el) => normalize(el.innerText || el.textContent || '').slice(0, 120))
      .filter(Boolean)
      .slice(0, 10);
    return { status: 'not_found', reason: targetID || targetName || 'empty_target', sessions };
  }
  fallback.el.scrollIntoView({ block: 'center', inline: 'nearest' });
  fallback.el.click();
  return { status: 'clicked', text: fallback.text.slice(0, 180), idHit: fallback.idHit };
}`

const messageCandidateExpression = `(() => {
  const normalize = (value) => String(value || '').replace(/\s+/g, ' ').trim();
  const classText = (el) => normalize(el && el.className && typeof el.className === 'string' ? el.className : '');
  const isVisible = (el) => {
    const style = window.getComputedStyle(el);
    const rect = el.getBoundingClientRect();
    return style.display !== 'none' &&
      style.visibility !== 'hidden' &&
      rect.width >= 24 &&
      rect.height >= 10 &&
      rect.bottom >= 0 &&
      rect.top <= window.innerHeight;
  };
  const imageURL = (img) => img.currentSrc || img.src || img.getAttribute('data-src') || img.getAttribute('src') || '';
  const findRoot = () => {
    const selectors = [
      '#drag-area.right.right-container',
      '#drag-area',
      '.right.right-container',
      '.chatbox > .right',
      '.nochat.right',
      '.right.overflow-hidden'
    ];
    for (const selector of selectors) {
      const el = document.querySelector(selector);
      if (el instanceof HTMLElement) return { el, selector };
    }
    return { el: document.body, selector: 'body' };
  };
  const findMessageArea = (root) => {
    const selectors = [
      '.message-wrapper .__view',
      '.__vuescroll .__view',
      '.__panel .__view',
      '.__view',
      '.message-wrapper',
      '.textbox',
      '.message',
      ''
    ];
    for (const selector of selectors) {
      const el = selector ? root.querySelector(selector) : root;
      if (el instanceof HTMLElement) return { el, selector: selector || '<root>' };
    }
    return { el: root, selector: '<root>' };
  };
  const scoreElement = (el, text, rect) => {
    const mark = normalize([el.id, classText(el), el.getAttribute('role'), el.getAttribute('node-type'), el.getAttribute('action-type')].join(' ')).toLowerCase();
    let score = 0;
    if (/msg|message|chat|bubble|dialog|talk|content|item|view|textbox/.test(mark)) score += 5;
    if (/text|txt|content|bubble/.test(mark)) score += 2;
    if (el.querySelector('img')) score += 2;
    if (text.length >= 2 && text.length <= 800) score += 3;
    if (rect.width > 80 && rect.height > 18) score += 2;
    if (rect.left > 320) score += 2;
    if (rect.width > 520 && rect.height > 320) score -= 6;
    if (/搜索|设置|发送|表情|图片|文件|微博聊天网页版|按下Enter发送内容/.test(text)) score -= 4;
    if (text.length > 800) score -= 6;
    return score;
  };
  const rootInfo = findRoot();
  const areaInfo = findMessageArea(rootInfo.el);
  const root = rootInfo.el;
  const area = areaInfo.el;
  const rootRect = root.getBoundingClientRect();
  const usingBodyFallback = root === document.body;
  const candidates = [];
  const seen = new Set();
  const selectors = [
    '.__view > div',
    '.__view [class]',
    '.message-wrapper [class]',
    '.textbox [class]',
    '[class*="msg" i]',
    '[class*="message" i]',
    '[class*="chat" i]',
    '[class*="bubble" i]',
    '[class*="text" i]',
    '[class*="content" i]',
    '[role="listitem"]',
    'p',
    'span',
    'li',
    'div'
  ];
  const nodes = Array.from(area.querySelectorAll(selectors.join(',')));
  if (!nodes.includes(area)) nodes.unshift(area);
  for (const el of nodes) {
    if (!(el instanceof HTMLElement) || !isVisible(el)) continue;
    const rect = el.getBoundingClientRect();
    if (usingBodyFallback || rect.right < rootRect.left || rect.left < rootRect.left - 5) continue;
    const className = classText(el);
    const marker = normalize([el.id, className, el.getAttribute('role')].join(' ')).toLowerCase();
    if (/sessionlist|lastmsg|search-wrapper|nochat/.test(marker)) continue;
    const text = normalize(el.innerText || el.textContent || '');
    const images = Array.from(el.querySelectorAll('img')).map(imageURL).filter(Boolean).slice(0, 4);
    if (!text && images.length === 0) continue;
    if (text.length > 1200) continue;
    const key = [Math.round(rect.left), Math.round(rect.top), Math.round(rect.width), Math.round(rect.height), text.slice(0, 80), images.join('|')].join('::');
    if (seen.has(key)) continue;
    seen.add(key);
    const score = scoreElement(el, text, rect);
    if (score < 2) continue;
    candidates.push({
      tag: el.tagName.toLowerCase(),
      id: el.id || '',
      className: className.slice(0, 160),
      role: el.getAttribute('role') || '',
      text: text.slice(0, 300),
      images,
      rect: {
        x: Math.round(rect.left),
        y: Math.round(rect.top),
        width: Math.round(rect.width),
        height: Math.round(rect.height)
      },
      score
    });
  }
  candidates.sort((a, b) => b.score - a.score || a.rect.y - b.rect.y || a.rect.x - b.rect.x);
  const selected = candidates.slice(0, 30).map((candidate, index) => ({ index, ...candidate }));
  if (selected.length > 0) return selected;

  const samples = [];
  for (const el of Array.from(root.querySelectorAll('div, p, span')).slice(0, 400)) {
    if (!(el instanceof HTMLElement) || !isVisible(el)) continue;
    const rect = el.getBoundingClientRect();
    const text = normalize(el.innerText || el.textContent || '');
    if (!text) continue;
    samples.push({
      tag: el.tagName.toLowerCase(),
      id: el.id || '',
      className: classText(el).slice(0, 120),
      role: el.getAttribute('role') || '',
      text: text.slice(0, 220),
      images: [],
      rect: {
        x: Math.round(rect.left),
        y: Math.round(rect.top),
        width: Math.round(rect.width),
        height: Math.round(rect.height)
      },
      score: 0
    });
    if (samples.length >= 12) break;
  }
  const areaRect = area.getBoundingClientRect();
  return [{
    index: 0,
    tag: 'debug',
    id: root.id || '',
    className: ['root=' + rootInfo.selector, 'area=' + areaInfo.selector, 'rootClass=' + classText(root), 'areaClass=' + classText(area)].join(' '),
    role: '',
    text: ('no message candidate; rootRect=' + Math.round(rootRect.left) + ',' + Math.round(rootRect.top) + ',' + Math.round(rootRect.width) + ',' + Math.round(rootRect.height) +
      '; areaRect=' + Math.round(areaRect.left) + ',' + Math.round(areaRect.top) + ',' + Math.round(areaRect.width) + ',' + Math.round(areaRect.height) +
      '; samples=' + samples.map((item) => item.text).join(' || ')).slice(0, 300),
    images: [],
    rect: {
      x: Math.round(rootRect.left),
      y: Math.round(rootRect.top),
      width: Math.round(rootRect.width),
      height: Math.round(rootRect.height)
    },
    score: 0
  }, ...samples.map((item, index) => ({ index: index + 1, ...item }))];
})()`
