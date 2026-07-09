package browser

import (
	"context"
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
	"strings"
	"time"
)

type MonitorOptions struct {
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
		opts.HealthCheckInterval = 30 * time.Second
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

	cmd := exec.CommandContext(ctx, executable, monitorArgs(opts, profileDir)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	slog.Info("starting browser monitor", "executable", executable, "profileDir", profileDir, "url", opts.URL, "headless", opts.Headless, "debugPort", opts.DebugPort)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start browser monitor: %w", err)
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
	}
	slog.Info("monitor heartbeat", "status", probe.Status, "url", probe.URL, "title", probe.Title, "error", probe.Error)
	if opts.OnProbe != nil {
		if err := opts.OnProbe(ctx, probe); err != nil {
			slog.Warn("update monitor status failed", "error", err)
		}
	}

	networkErrCh := make(chan error, 1)
	go func() {
		networkErrCh <- monitorNetworkEvents(ctx, target.WebSocketDebuggerURL, opts.OnMessage, opts.OnImageReference)
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
			probe := PageProbe{
				Status:    "network_listening",
				URL:       target.URL,
				Title:     target.Title,
				CheckedAt: time.Now(),
			}
			slog.Info("monitor heartbeat", "status", probe.Status, "url", probe.URL, "title", probe.Title, "error", probe.Error)
			if opts.OnProbe != nil {
				if err := opts.OnProbe(ctx, probe); err != nil {
					slog.Warn("update monitor status failed", "error", err)
				}
			}
		case err := <-networkErrCh:
			if ctx.Err() != nil {
				stopProcess(cmd)
				return nil
			}
			stopProcess(cmd)
			if err != nil {
				return fmt.Errorf("network monitor exited: %w", err)
			}
			return errors.New("network monitor exited")
		case <-reloadC:
			slog.Info("page reload interval reached; opening chat URL again", "url", opts.URL)
			if err := openURL(ctx, opts.DebugPort, opts.URL); err != nil {
				slog.Warn("page reload failed", "error", err)
			}
		}
	}
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

func openURL(ctx context.Context, port int, pageURL string) error {
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPut, debugURL(port, "/json/new?"+neturl.QueryEscape(pageURL)), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("debug endpoint returned HTTP %d", resp.StatusCode)
	}
	return nil
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
	ResourceType string
	Status       int
	MimeType     string
}

func monitorNetworkEvents(
	ctx context.Context,
	webSocketDebuggerURL string,
	onMessage func(context.Context, NetworkMessage) error,
	onImageReference func(context.Context, NetworkImageReference) error,
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
	for {
		envelope, err := client.readEnvelope(ctx)
		if err != nil {
			return err
		}
		if envelope.Method == "" {
			continue
		}

		switch envelope.Method {
		case "Network.webSocketCreated":
			var params struct {
				RequestID string `json:"requestId"`
				URL       string `json:"url"`
			}
			if json.Unmarshal(envelope.Params, &params) == nil {
				requests[params.RequestID] = networkRequestInfo{URL: params.URL, ResourceType: "WebSocket"}
				slog.Info("network websocket created", "requestID", params.RequestID, "url", params.URL)
			}
		case "Network.webSocketClosed":
			var params struct {
				RequestID string `json:"requestId"`
			}
			if json.Unmarshal(envelope.Params, &params) == nil {
				info := requests[params.RequestID]
				slog.Info("network websocket closed", "requestID", params.RequestID, "url", info.URL)
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
			for _, message := range messages {
				if message.MessageType == "recall" {
					slog.Info("network recall event parsed",
						"groupID", message.GroupID,
						"groupName", message.GroupName,
						"senderID", message.SenderID,
						"senderName", message.SenderName,
						"content", clipLogText(message.Content, 240),
					)
					continue
				}
				slog.Info("network message parsed",
					"weiboMessageID", message.WeiboMessageID,
					"groupID", message.GroupID,
					"groupName", message.GroupName,
					"senderID", message.SenderID,
					"senderName", message.SenderName,
					"type", message.MessageType,
					"content", clipLogText(message.Content, 240),
				)
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
				slog.Info("network websocket frame received",
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
			requests[params.RequestID] = networkRequestInfo{
				URL:          params.Response.URL,
				ResourceType: params.Type,
				Status:       params.Response.Status,
				MimeType:     params.Response.MimeType,
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
			if onImageReference == nil || !shouldInspectImageReferenceResponse(info, params.EncodedDataLength) {
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
			if json.Unmarshal(envelope.Params, &params) == nil && looksLikeMessageURL(requests[params.RequestID].URL) {
				slog.Warn("network request failed", "requestID", params.RequestID, "url", requests[params.RequestID].URL, "error", params.ErrorText)
			}
		}
	}
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
		return nil
	}
	collector := imageReferenceCollector{
		sourceURL: sourceURL,
		seen:      make(map[string]bool),
	}
	collector.walk(value, nil)
	return collector.refs
}

func looksLikeImageReferenceBody(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "fid") &&
		(strings.Contains(lower, "sinaimg.cn") ||
			strings.Contains(lower, ".jpg") ||
			strings.Contains(lower, ".jpeg") ||
			strings.Contains(lower, ".png") ||
			strings.Contains(lower, ".webp") ||
			strings.Contains(lower, ".gif"))
}

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
		for _, child := range typed {
			childScope := c.walk(child, path)
			scope.fids = appendUniqueStrings(scope.fids, childScope.fids...)
			scope.urls = appendUniqueStrings(scope.urls, childScope.urls...)
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
	return key == "fid" || key == "fids" || key == "file_id" || key == "fileid"
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
	case 321:
		return parseGroupChatTextMessage(rawEvent, event.Data.Info)
	case 331:
		return parseGroupChatRecallMessage(rawEvent, event.Data.Info)
	default:
		return NetworkMessage{}, false
	}
}

func parseGroupChatTextMessage(rawEvent json.RawMessage, rawInfo json.RawMessage) (NetworkMessage, bool) {
	var info struct {
		GID            json.Number   `json:"gid"`
		GroupName      string        `json:"group_name"`
		Content        string        `json:"content"`
		FromUID        json.Number   `json:"from_uid"`
		CurrentVersion json.Number   `json:"current_version"`
		FIDs           []json.Number `json:"fids"`
		MediaType      int           `json:"media_type"`
		Type           int           `json:"type"`
		SubType        int           `json:"sub_type"`
		Annotations    struct {
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
	weiboMessageID := info.CurrentVersion.String()
	content := strings.TrimSpace(info.Content)
	if groupID == "" || senderID == "" || weiboMessageID == "" || content == "" {
		return NetworkMessage{}, false
	}

	var sentAt *time.Time
	if info.Annotations.PushProcExt.SendTime > 0 {
		value := time.UnixMilli(info.Annotations.PushProcExt.SendTime)
		sentAt = &value
	}
	avatar := info.FromUser.ProfileImageURL
	if avatar == "" {
		avatar = info.FromUser.AvatarLarge
	}
	attachments := imageAttachmentsFromInfo(rawInfo, info.MediaType, info.FIDs)
	messageType := messageTypeFromGroupChatInfo(info.Type, info.MediaType, len(attachments), "text")

	return NetworkMessage{
		WeiboMessageID: weiboMessageID,
		GroupID:        groupID,
		GroupName:      strings.TrimSpace(info.GroupName),
		SenderID:       senderID,
		SenderName:     strings.TrimSpace(info.FromUser.ScreenName),
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
		return []NetworkAttachment{{
			AttachmentType: "image",
			Meta:           imageAttachmentMeta(mediaType, fids, ""),
		}}
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
		slog.Info("target group selection", "status", result.Status, "text", result.Text, "idHit", result.IDHit)
	} else {
		slog.Info("target group selection", "status", result.Status, "reason", result.Reason, "sessions", strings.Join(result.Sessions, " | "))
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
		slog.Info("message candidate",
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
