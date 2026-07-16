package browser

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	URL                 string
	ProfileDir          string
	Executable          string
	Headless            bool
	NoSandbox           bool
	DebugPort           int
	HealthCheckInterval time.Duration
	PageReloadInterval  time.Duration
	TargetGroupID       string
	TargetGroupName     string
	OnProbe             func(context.Context, PageProbe) error
	OnMessage           func(context.Context, NetworkMessage) error
	OnImageReference    func(context.Context, NetworkImageReference) error
	OnBrowserState      func(bool)
	OnCDPState          func(connected bool, reconnected bool)
	OnPageTarget        func(webSocketDebuggerURL string)
	OnWebSocketState    func(connected bool, reconnected bool)
	OnMessagesParsed    func(int)
	OnTrackedRequests   func(int)
}

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

	slog.Info("starting browser monitor", "executable", executable, "profileDir", profileDir, "url", opts.URL, "headless", opts.Headless, "debugPort", opts.DebugPort)
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

	if opts.OnProbe != nil {
		_ = opts.OnProbe(ctx, PageProbe{
			Status:    "browser_started",
			URL:       opts.URL,
			CheckedAt: time.Now(),
		})
	}

	target, probe, err := waitForChatPageTarget(ctx, opts.DebugPort)
	if err != nil {
		stopProcess(cmd)
		return err
	}
	if probe.Status == "page_ok" && target.WebSocketDebuggerURL != "" {
		selectStatus, selectErr := ensureTargetGroupSelected(ctx, target.WebSocketDebuggerURL, TargetGroup{
			ID:   opts.TargetGroupID,
			Name: opts.TargetGroupName,
		})
		probe.GroupSelectStatus = selectStatus
		if selectErr != nil {
			probe.GroupSelectError = selectErr.Error()
			stopProcess(cmd)
			return selectErr
		}
		if opts.OnPageTarget != nil {
			opts.OnPageTarget(target.WebSocketDebuggerURL)
		}
	}
	slog.Info("monitor heartbeat", "status", probe.Status, "url", probe.URL, "title", probe.Title, "error", probe.Error)
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
			err := monitorNetworkEvents(networkCtx, webSocketDebuggerURL, opts.OnMessage, opts.OnImageReference, opts.OnMessagesParsed, opts.OnTrackedRequests, opts.OnWebSocketState)
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
		case <-ticker.C:
			currentTarget, probe := getCurrentPageProbe(ctx, opts.DebugPort)
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
						"oldURL", target.URL,
						"newURL", currentTarget.URL,
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
			slog.Info("monitor heartbeat", "status", probe.Status, "url", probe.URL, "title", probe.Title, "error", probe.Error)
			if opts.OnProbe != nil {
				if err := opts.OnProbe(ctx, probe); err != nil {
					slog.Warn("update monitor status failed", "error", err)
				}
			}
			if probe.Status == "login_required" {
				stopProcess(cmd)
				return errors.New("weibo login is required")
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
				stopProcess(cmd)
				return errors.New("weibo login is required")
			}
			if reconnectProbe.Status != "page_ok" || currentTarget.WebSocketDebuggerURL == "" {
				scheduleNetworkReconnect(fmt.Errorf("chat page is not ready: status=%s error=%s", reconnectProbe.Status, reconnectProbe.Error))
				continue
			}
			target = currentTarget
			slog.Info("reconnecting network monitor in current browser process",
				"attempt", reconnectAttempt,
				"url", target.URL,
			)
			startNetworkMonitor(target.WebSocketDebuggerURL)
			if opts.OnPageTarget != nil {
				opts.OnPageTarget(target.WebSocketDebuggerURL)
			}
		case <-reloadC:
			slog.Info("page reload interval reached; reloading current chat page", "url", target.URL)
			if err := reloadPage(ctx, target.WebSocketDebuggerURL); err != nil {
				slog.Warn("page reload failed", "error", err)
			}
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
		"--disable-background-timer-throttling",
		"--disable-backgrounding-occluded-windows",
		"--disable-renderer-backgrounding",
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

func waitForChatPageTarget(ctx context.Context, port int) (chromeTarget, PageProbe, error) {
	for {
		target, probe := getCurrentPageProbe(ctx, port)
		if probe.Status == "page_ok" && target.WebSocketDebuggerURL != "" {
			return target, probe, nil
		}
		if probe.Status == "login_required" {
			return target, probe, errors.New("weibo login is required")
		}
		slog.Info("chat page not ready; retrying", "status", probe.Status, "url", probe.URL, "title", probe.Title, "error", probe.Error, "retryIn", "1s")
		select {
		case <-ctx.Done():
			return chromeTarget{}, probe, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

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
	return target, probe
}

func ensureTargetGroupSelected(ctx context.Context, webSocketDebuggerURL string, targetGroup TargetGroup) (string, error) {
	targetConfigured := strings.TrimSpace(targetGroup.ID) != "" || strings.TrimSpace(targetGroup.Name) != ""
	if !targetConfigured {
		return "no_target_configured", nil
	}
	for {
		selectStatus, selectErr := selectTargetGroup(ctx, webSocketDebuggerURL, targetGroup)
		if selectErr == nil && (strings.HasPrefix(selectStatus, "clicked") || strings.HasPrefix(selectStatus, "already_selected")) {
			if strings.HasPrefix(selectStatus, "clicked") {
				time.Sleep(2 * time.Second)
			}
			return selectStatus, nil
		}
		errorText := ""
		if selectErr != nil {
			errorText = selectErr.Error()
		}
		slog.Info("target group not ready; retrying", "status", selectStatus, "error", errorText, "retryIn", "3s")
		select {
		case <-ctx.Done():
			return selectStatus, ctx.Err()
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

func monitorNetworkEvents(
	ctx context.Context,
	webSocketDebuggerURL string,
	onMessage func(context.Context, NetworkMessage) error,
	onImageReference func(context.Context, NetworkImageReference) error,
	onMessagesParsed func(int),
	onTrackedRequests func(int),
	onWebSocketState func(connected bool, reconnected bool),
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
				slog.Info("network websocket created", "requestID", params.RequestID, "url", params.URL)
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
				slog.Info("network websocket closed", "requestID", params.RequestID, "url", info.URL)
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
					slog.Info("network message parsed",
						"weiboMessageID", message.WeiboMessageID,
						"groupID", message.GroupID,
						"senderID", message.SenderID,
						"type", message.MessageType,
						"contentLength", len([]rune(message.Content)),
						"contentSHA256", contentLogHash(message.Content),
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
					"url", info.URL,
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
				slog.Info("network response candidate",
					"requestID", params.RequestID,
					"type", params.Type,
					"status", params.Response.Status,
					"mime", params.Response.MimeType,
					"url", params.Response.URL,
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
					"sourceURL", clipLogText(ref.SourceURL, 180),
					"fids", strings.Join(ref.FIDs, ","),
					"urls", strings.Join(ref.URLs, ","),
				)
				if err := onImageReference(ctx, ref); err != nil {
					slog.Warn("resolve image url reference failed", "sourceURL", ref.SourceURL, "error", err)
				}
			}
			if !shouldInspectImageReferenceResponse(info, params.EncodedDataLength) {
				continue
			}
			body, err := getNetworkResponseBody(ctx, client, params.RequestID)
			if err != nil {
				slog.Debug("read network response body failed", "requestID", params.RequestID, "url", info.URL, "error", err)
				continue
			}
			refs := extractImageReferencesFromBody(body, info.URL)
			for _, ref := range refs {
				slog.Info("network image reference parsed",
					"sourceURL", clipLogText(ref.SourceURL, 180),
					"fids", strings.Join(ref.FIDs, ","),
					"urls", strings.Join(ref.URLs, ","),
				)
				if err := onImageReference(ctx, ref); err != nil {
					slog.Warn("resolve image reference failed", "sourceURL", ref.SourceURL, "error", err)
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
					slog.Warn("network request failed", "requestID", params.RequestID, "url", info.URL, "error", params.ErrorText)
				}
			}
		}
	}
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
	if !looksLikeImageURL(imageURL) {
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
		if looksLikeImageURL(typed) && !skipImageURLPath(path) {
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
		if strings.TrimSpace(rawURL) != "" {
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
		if looksLikeImageURL(typed) && !skipImageURLPath(path) {
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
	attachments := imageAttachmentsFromInfo(rawInfo, info.MediaType, info.FIDs)
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

func imageAttachmentsFromInfo(rawInfo json.RawMessage, mediaType int, fids []json.Number) []NetworkAttachment {
	if mediaType != 1 && len(fids) == 0 {
		return nil
	}

	urls := extractImageURLs(rawInfo)
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
		meta["url_status"] = "missing_in_websocket_payload"
	}
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
		if !looksLikeImageURL(typed) || seen[typed] {
			return
		}
		seen[typed] = true
		*urls = append(*urls, typed)
	}
}

func skipImageURLPath(path []string) bool {
	joined := strings.Join(path, ".")
	return strings.Contains(joined, "from_user") ||
		strings.Contains(joined, "profile_image") ||
		strings.Contains(joined, "avatar")
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
