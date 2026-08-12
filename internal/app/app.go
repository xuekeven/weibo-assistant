package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/xuekeven/weibo-assistant/internal/browser"
	"github.com/xuekeven/weibo-assistant/internal/config"
	"github.com/xuekeven/weibo-assistant/internal/httpapi"
	"github.com/xuekeven/weibo-assistant/internal/migrate"
	"github.com/xuekeven/weibo-assistant/internal/notifier"
	"github.com/xuekeven/weibo-assistant/internal/store"
	"github.com/xuekeven/weibo-assistant/internal/systemmetrics"
)

func RunServer(ctx context.Context, cfg config.Config) error {
	// 依赖的创建顺序也是关闭顺序：数据库 -> 迁移 -> API。
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	if err := migrate.Apply(ctx, st); err != nil {
		return err
	}
	notify := notifier.NewWeworkClient(cfg.WeworkRelayURL, cfg.WeworkRelayAPIKey, cfg.WeworkRelayToUser)
	api := httpapi.NewServer(cfg, st, notify)
	frontend, err := frontendFileHandler(cfg.FrontendDistDir)
	if err != nil {
		return fmt.Errorf("load frontend for unified server: %w", err)
	}
	server := &http.Server{
		Addr:              api.Addr(),
		Handler:           unifiedHTTPHandler(api.Handler(), frontend),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	// HTTP 服务和 PostgreSQL 实时事件监听并行运行。
	errCh := make(chan error, 1)
	go func() {
		slog.Info("api server listening", "addr", server.Addr)
		errCh <- server.ListenAndServe()
	}()
	go func() {
		if err := api.RunRealtime(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("realtime listener exited", "error", err)
		}
	}()

	select {
	case <-ctx.Done():
		// 收到 SIGTERM 后最多等待 10 秒，让正在处理的 HTTP 请求正常结束。
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func unifiedHTTPHandler(apiHandler, frontendHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasPrefix(path, "/api/") ||
			path == "/api" ||
			path == "/livez" ||
			path == "/readyz" ||
			path == "/healthz" ||
			path == "/metrics" {
			apiHandler.ServeHTTP(w, r)
			return
		}
		frontendHandler.ServeHTTP(w, r)
	})
}

func RunMonitor(ctx context.Context, cfg config.Config) error {
	// RunMonitor 是监控进程的“装配函数”。具体采集在 browser，持久化在 store，
	// 这里负责启动后台任务并把 browser 的事件回调连接到业务处理。
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := migrate.Apply(ctx, st); err != nil {
		return err
	}

	if err := st.UpdateMonitorStatus(ctx, "monitor_starting", cfg.WeiboChatURL, ""); err != nil {
		return err
	}
	if _, err := st.SyncEnvironmentNotificationRule(ctx, store.EnvironmentNotificationRuleParams{
		WeiboGroupID:  cfg.TargetGroupID,
		GroupName:     cfg.TargetGroupName,
		WeiboSenderID: cfg.TargetSenderID,
		SenderName:    cfg.TargetSenderName,
	}); err != nil {
		return fmt.Errorf("sync environment notification rule: %w", err)
	}
	// 指标上报和通知投递是独立 goroutine；退出前等待它们结束，避免丢失收尾操作。
	metrics := newMonitorMetrics()
	metricsCtx, cancelMetrics := context.WithCancel(ctx)
	metricsDone := make(chan struct{})
	go func() {
		defer close(metricsDone)
		runMetricsReporter(metricsCtx, st, metrics)
	}()
	defer func() {
		cancelMetrics()
		<-metricsDone
	}()
	imageURLByFID := newImageURLCache(cfg.ImageCacheTTL, cfg.ImageCacheMaxItems)
	weworkEnabled := strings.TrimSpace(cfg.WeworkRelayURL) != "" && strings.TrimSpace(cfg.WeworkRelayAPIKey) != ""
	weworkClient := notifier.NewWeworkClient(cfg.WeworkRelayURL, cfg.WeworkRelayAPIKey, cfg.WeworkRelayToUser)
	loginAlerts := newLoginAlertSender(
		weworkClient,
		weworkEnabled,
	)
	historyTarget := &historyPageTarget{}
	disconnects := &disconnectWindow{}
	var targetGroup store.Group
	if strings.TrimSpace(cfg.TargetGroupID) != "" {
		targetGroup, err = st.UpsertGroup(ctx, store.UpsertGroupParams{
			WeiboGroupID: cfg.TargetGroupID,
			Name:         fallbackText(cfg.TargetGroupName, cfg.TargetGroupID),
		})
		if err != nil {
			return fmt.Errorf("prepare history sync group: %w", err)
		}
		if err := enqueueStartupHistorySync(ctx, st, targetGroup); err != nil {
			return fmt.Errorf("enqueue startup history sync: %w", err)
		}
	}
	historyCtx, cancelHistory := context.WithCancel(ctx)
	historyDone := make(chan struct{})
	go func() {
		defer close(historyDone)
		runHistorySyncWorker(historyCtx, cfg, st, metrics, imageURLByFID, historyTarget)
	}()
	defer func() {
		cancelHistory()
		<-historyDone
	}()
	mediaCtx, cancelMedia := context.WithCancel(ctx)
	mediaDone := make(chan struct{})
	go func() {
		defer close(mediaDone)
		runMediaResolutionWorker(mediaCtx, st, imageURLByFID, historyTarget)
	}()
	defer func() {
		cancelMedia()
		<-mediaDone
	}()
	if strings.TrimSpace(cfg.WeworkRelayAPIKey) != "" {
		workerCtx, cancelWorker := context.WithCancel(ctx)
		workerDone := make(chan struct{})
		go func() {
			defer close(workerDone)
			runNotificationWorker(workerCtx, cfg, st, metrics)
		}()
		defer func() {
			cancelWorker()
			<-workerDone
		}()
	} else {
		slog.Warn("notification worker disabled because WEWORK_RELAY_API_KEY is empty")
	}

	sendMonitorLifecycleNotification(ctx, weworkClient, weworkEnabled, monitorStartedNotificationText)
	defer func() {
		// 主 context 在正常停服时已经取消，关闭通知必须使用独立的短超时。
		notificationCtx, cancelNotification := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancelNotification()
		sendMonitorLifecycleNotification(notificationCtx, weworkClient, weworkEnabled, monitorStoppedNotificationText)
	}()

	// browser 不直接访问数据库。它发现事件后调用下面的 OnXxx 回调。
	return browser.RunMonitor(ctx, browser.MonitorOptions{
		URL:                    cfg.WeiboChatURL,
		ProfileDir:             cfg.WeiboProfileDir,
		Executable:             cfg.ChromeExecutable,
		Headless:               cfg.Headless,
		NoSandbox:              cfg.ChromeNoSandbox,
		DebugPort:              cfg.ChromeDebugPort,
		RendererCPULimit:       cfg.RendererCPULimit,
		RendererCPUChecks:      cfg.RendererCPUChecks,
		RendererCPUInterval:    cfg.RendererCPUInterval,
		HealthCheckInterval:    cfg.HealthCheckInterval,
		PageReloadInterval:     cfg.PageReloadInterval,
		LoginQRRefreshInterval: cfg.LoginQRRefreshInterval,
		TargetGroupID:          cfg.TargetGroupID,
		TargetGroupName:        cfg.TargetGroupName,
		OnProbe: func(probeCtx context.Context, probe browser.PageProbe) error {
			// 心跳既更新健康状态，也尝试用 DOM 中的新图片地址补全旧附件。
			if err := st.UpdateMonitorStatus(probeCtx, probe.Status, probe.URL, probe.Error); err != nil {
				return err
			}
			err := resolveProbeImageCandidates(probeCtx, st, imageURLByFID, probe)
			metrics.imageCacheItems.Store(int64(imageURLByFID.Len()))
			return err
		},
		OnMessage: func(messageCtx context.Context, message browser.NetworkMessage) error {
			// 这里完成 browser.NetworkMessage -> store.CreateMessageParams 的层间转换。
			if cfg.TargetGroupID != "" && message.GroupID != cfg.TargetGroupID {
				slog.Info("ignore message from non-target group", "groupID", message.GroupID, "targetGroupID", cfg.TargetGroupID)
				return nil
			}
			enrichNetworkAttachmentsWithImageCache(message.Attachments, imageURLByFID)
			notification, targetMatched := notificationForNetworkMessage(cfg, message)
			groupName := message.GroupName
			if strings.TrimSpace(groupName) == "" && message.GroupID == cfg.TargetGroupID {
				groupName = cfg.TargetGroupName
			}
			params := store.CreateMessageParams{
				WeiboMessageID: message.WeiboMessageID,
				Group: store.UpsertGroupParams{
					WeiboGroupID: message.GroupID,
					Name:         fallbackText(groupName, message.GroupID),
				},
				Sender: store.UpsertUserParams{
					WeiboUserID: message.SenderID,
					Nickname:    fallbackText(message.SenderName, message.SenderID),
					AvatarURL:   message.SenderAvatar,
					Raw:         json.RawMessage(`null`),
				},
				MessageType:  message.MessageType,
				Content:      message.Content,
				SentAt:       message.SentAt,
				Source:       message.Source,
				DedupeKey:    message.DedupeKey,
				Attachments:  storeAttachmentsFromNetwork(message.Attachments),
				Raw:          message.Raw,
				Notification: notification,
			}
			result, err := st.CreateMessage(messageCtx, params)
			if err != nil {
				metrics.persistFailure.Add(1)
				return err
			}
			metrics.persistSuccess.Add(1)
			if !result.Created {
				metrics.dedupeTotal.Add(1)
			}
			if err := st.MarkMonitorMessage(messageCtx); err != nil {
				slog.Warn("update monitor last message time failed", "error", err)
			}
			logMessage := "重复消息已忽略"
			if result.Created {
				logMessage = "新消息已保存"
			}
			slog.Info(logMessage,
				"数据库消息ID", result.Message.ID,
				"微博消息ID", result.Message.WeiboMessageID,
				"群聊ID", result.Message.GroupID,
				"发送人ID", result.Message.WeiboSenderID,
			)
			if targetMatched && notification == nil && result.Created {
				slog.Warn("target message matched but wework relay is not configured",
					"messageID", result.Message.ID,
					"weiboMessageID", result.Message.WeiboMessageID,
				)
				recordNotificationEvent(messageCtx, st, cfg, result.Message, "skipped", nil, "WEWORK_RELAY_URL or WEWORK_RELAY_API_KEY is empty")
			} else if result.NotificationsQueued > 0 {
				slog.Info("message notifications queued", "messageID", result.Message.ID, "rules", result.NotificationsQueued)
			}
			return nil
		},
		OnImageReference: func(refCtx context.Context, ref browser.NetworkImageReference) error {
			// 图片 URL 和消息可能来自不同网络响应，所以先按 fid 缓存，再补写数据库。
			defer metrics.imageCacheItems.Store(int64(imageURLByFID.Len()))
			for _, fid := range ref.FIDs {
				for _, imageURL := range ref.URLs {
					imageURLByFID.Set(fid, imageURL)
					count, err := st.ResolveImageAttachmentURL(refCtx, store.ResolveImageAttachmentParams{
						FID:       fid,
						URL:       imageURL,
						SourceURL: ref.SourceURL,
					})
					if err != nil {
						return err
					}
					if count > 0 {
						slog.Info("image attachment resolved", "fid", fid, "resource", mediaURLLogValue(imageURL), "updated", count)
					}
				}
			}
			return nil
		},
		OnBrowserState: func(running bool) {
			metrics.browserRunning.Store(running)
		},
		OnCDPState: func(connected bool, reconnected bool) {
			metrics.cdpConnected.Store(connected)
			if !connected {
				recordHistoryDisconnect(ctx, st, targetGroup, disconnects)
			}
			if reconnected {
				metrics.cdpReconnects.Add(1)
				from, to, stopMID := disconnects.TakeRecoveryWindow()
				go enqueueRecoveryHistorySync(ctx, st, targetGroup, from, to, stopMID)
			}
		},
		OnPageTarget: historyTarget.Set,
		OnWebSocketState: func(connected bool, reconnected bool) {
			if !connected {
				recordHistoryDisconnect(ctx, st, targetGroup, disconnects)
				return
			}
			if reconnected {
				from, to, stopMID := disconnects.TakeRecoveryWindow()
				go enqueueRecoveryHistorySync(ctx, st, targetGroup, from, to, stopMID)
			}
		},
		OnMessagesParsed: func(count int) {
			metrics.recordParsed(count)
		},
		OnTrackedRequests: func(count int) {
			metrics.trackedRequests.Store(int64(count))
		},
		OnBeforePageReload: func(reloadCtx context.Context) error {
			return checkCPUHealthBeforePageReload(
				reloadCtx,
				systemmetrics.ReadCPU,
				weworkClient,
				weworkEnabled,
				cfg.CPUPowerAlertWatts,
				cfg.CPUTemperatureAlertC,
			)
		},
		OnLoginRequired: loginAlerts.Notify,
		OnLoginRestored: func() {
			// 企微不可用不应阻塞微博监听恢复；后台进行有上限的重试。
			go sendLoginRestoredNotification(ctx, loginAlerts)
		},
	})
}

func resolveProbeImageCandidates(ctx context.Context, st *store.Store, imageURLByFID *imageURLCache, probe browser.PageProbe) error {
	for _, candidate := range probe.MessageCandidates {
		for _, imageURL := range candidate.Images {
			for _, ref := range browser.ExtractImageReferencesFromURL(imageURL, imageURL) {
				for _, fid := range ref.FIDs {
					for _, url := range ref.URLs {
						imageURLByFID.Set(fid, url)
						count, err := st.ResolveImageAttachmentURL(ctx, store.ResolveImageAttachmentParams{
							FID:       fid,
							URL:       url,
							SourceURL: ref.SourceURL,
						})
						if err != nil {
							return err
						}
						if count > 0 {
							slog.Info("image attachment resolved from dom", "fid", fid, "resource", mediaURLLogValue(url), "updated", count)
						}
					}
				}
			}
		}
	}
	return nil
}

func enrichNetworkAttachmentsWithImageCache(attachments []browser.NetworkAttachment, imageURLByFID *imageURLCache) {
	for index := range attachments {
		if attachments[index].AttachmentType != "image" || strings.TrimSpace(attachments[index].URL) != "" {
			continue
		}
		for _, fid := range networkAttachmentFIDs(attachments[index]) {
			if imageURL, ok := imageURLByFID.Get(fid); ok {
				attachments[index].URL = imageURL
				break
			}
		}
	}
}

func networkAttachmentFIDs(attachment browser.NetworkAttachment) []string {
	var meta struct {
		FIDs []string `json:"fids"`
	}
	if err := json.Unmarshal(attachment.Meta, &meta); err != nil {
		return nil
	}
	return meta.FIDs
}

func storeAttachmentsFromNetwork(attachments []browser.NetworkAttachment) []store.Attachment {
	if len(attachments) == 0 {
		return nil
	}
	result := make([]store.Attachment, 0, len(attachments))
	for _, attachment := range attachments {
		result = append(result, store.Attachment{
			AttachmentType: attachment.AttachmentType,
			URL:            attachment.URL,
			LocalPath:      attachment.LocalPath,
			Meta:           attachment.Meta,
		})
	}
	return result
}

func notificationForNetworkMessage(cfg config.Config, message browser.NetworkMessage) (*store.EnqueueNotificationParams, bool) {
	if message.MessageType == "event" {
		return nil, false
	}
	storedMessage := store.Message{
		WeiboSenderID: message.SenderID,
		SenderName:    message.SenderName,
		MessageType:   message.MessageType,
		Content:       message.Content,
		SentAt:        message.SentAt,
		CapturedAt:    time.Now(),
	}
	targetMatched := false
	if hasTargetSender(cfg) && messageCanMatchTargetSender(message.MessageType) {
		targetMatched = messageMatchesTargetSender(cfg, storedMessage)
	}
	if strings.TrimSpace(cfg.WeworkRelayURL) == "" || strings.TrimSpace(cfg.WeworkRelayAPIKey) == "" {
		return nil, targetMatched
	}
	return &store.EnqueueNotificationParams{
		Content:     buildTargetNotificationContent(storedMessage),
		RelayURL:    cfg.WeworkRelayURL,
		ToUser:      cfg.WeworkRelayToUser,
		MaxAttempts: cfg.NotificationMaxAttempts,
	}, targetMatched
}

func messageCanMatchTargetSender(messageType string) bool {
	switch strings.TrimSpace(messageType) {
	case "system", "event":
		return false
	default:
		return true
	}
}

func runNotificationWorker(ctx context.Context, cfg config.Config, st *store.Store, metrics *monitorMetrics) {
	// worker 每次只领取一个任务。数据库的 SKIP LOCKED 保证未来启动多个 worker 时
	// 同一任务不会被同时发送。
	pollInterval := cfg.NotificationPollInterval
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	for {
		job, err := st.ClaimNotificationOutbox(ctx, 5*time.Minute)
		if errors.Is(err, pgx.ErrNoRows) {
			// 没有待发送任务不是错误，等待一小段时间后继续轮询。
			if !waitContext(ctx, pollInterval) {
				return
			}
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("claim notification outbox failed", "error", err)
			if !waitContext(ctx, 3*time.Second) {
				return
			}
			continue
		}

		client := notifier.NewWeworkClient(job.RelayURL, cfg.WeworkRelayAPIKey, job.ToUser)
		response, sendErr := client.SendTextWithIdempotency(ctx, job.Content, fmt.Sprintf("weibo-outbox-%d", job.ID))
		if sendErr == nil {
			if err := st.MarkNotificationOutboxSent(ctx, job.ID); err != nil {
				slog.Error("mark notification outbox sent failed", "jobID", job.ID, "error", err)
				continue
			}
			recordOutboxNotificationEvent(ctx, st, job, "sent", &response, "")
			metrics.notifySent.Add(1)
			slog.Info("target message wework notification sent", "messageID", job.MessageID, "attempt", job.AttemptCount)
			continue
		}

		// 失败后使用指数退避，减少中继服务故障时的请求压力。
		nextAttemptAt := time.Now().Add(notificationRetryDelay(job.AttemptCount))
		finalFailure, err := st.MarkNotificationOutboxFailed(ctx, job, sendErr, nextAttemptAt)
		if err != nil {
			slog.Error("mark notification outbox failed", "jobID", job.ID, "error", err)
			continue
		}
		status := "retrying"
		if finalFailure {
			status = "failed"
			metrics.notifyFailed.Add(1)
		} else {
			metrics.notifyRetry.Add(1)
		}
		recordOutboxNotificationEvent(ctx, st, job, status, &response, sendErr.Error())
		slog.Warn("target message wework notification failed",
			"messageID", job.MessageID,
			"attempt", job.AttemptCount,
			"maxAttempts", job.MaxAttempts,
			"final", finalFailure,
			"nextAttemptAt", nextAttemptAt,
			"error", sendErr,
		)
	}
}

func notificationRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := 5 * time.Second
	for index := 1; index < attempt && delay < 15*time.Minute; index++ {
		delay *= 2
	}
	if delay > 15*time.Minute {
		return 15 * time.Minute
	}
	return delay
}

func waitContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func recordOutboxNotificationEvent(ctx context.Context, st *store.Store, job store.NotificationOutboxJob, status string, response *notifier.SendResponse, errorMessage string) {
	requestPayload, _ := json.Marshal(map[string]string{
		"content": job.Content,
		"touser":  job.ToUser,
	})
	responsePayload, _ := json.Marshal(response)
	if _, err := st.CreateNotificationEvent(ctx, store.CreateNotificationEventParams{
		RuleID:          job.RuleID,
		MessageID:       job.MessageID,
		Status:          status,
		RelayURL:        job.RelayURL,
		ToUser:          job.ToUser,
		RequestPayload:  requestPayload,
		ResponsePayload: responsePayload,
		ErrorMessage:    errorMessage,
	}); err != nil {
		slog.Error("record notification event failed", "error", err, "messageID", job.MessageID, "status", status)
	}
}

func hasTargetSender(cfg config.Config) bool {
	return strings.TrimSpace(cfg.TargetSenderID) != "" || strings.TrimSpace(cfg.TargetSenderName) != ""
}

func messageMatchesTargetSender(cfg config.Config, message store.Message) bool {
	if targetID := strings.TrimSpace(cfg.TargetSenderID); targetID != "" {
		return strings.TrimSpace(message.WeiboSenderID) == targetID
	}
	return strings.TrimSpace(message.SenderName) == strings.TrimSpace(cfg.TargetSenderName)
}

func buildTargetNotificationContent(message store.Message) string {
	sentAt := "未知时间"
	if message.SentAt != nil {
		sentAt = message.SentAt.Format("1月2日15点04分")
	} else if !message.CapturedAt.IsZero() {
		sentAt = message.CapturedAt.Format("1月2日15点04分")
	}
	content := strings.TrimSpace(message.Content)
	if content == "" {
		content = fmt.Sprintf("[%s]", message.MessageType)
	}
	content = truncateRunes(content, 1500)
	return fmt.Sprintf("@%s 在%s在群里发消息：\n%s",
		fallbackText(message.SenderName, message.WeiboSenderID),
		sentAt,
		content,
	)
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

func recordNotificationEvent(ctx context.Context, st *store.Store, cfg config.Config, message store.Message, status string, response *notifier.SendResponse, errorMessage string) {
	requestPayload, _ := json.Marshal(map[string]string{
		"content": buildTargetNotificationContent(message),
		"touser":  cfg.WeworkRelayToUser,
	})
	responsePayload, _ := json.Marshal(response)
	if _, err := st.CreateNotificationEvent(ctx, store.CreateNotificationEventParams{
		MessageID:       message.ID,
		Status:          status,
		RelayURL:        cfg.WeworkRelayURL,
		ToUser:          cfg.WeworkRelayToUser,
		RequestPayload:  requestPayload,
		ResponsePayload: responsePayload,
		ErrorMessage:    errorMessage,
	}); err != nil {
		slog.Error("record notification event failed", "error", err, "messageID", message.ID, "status", status)
	}
}

func fallbackText(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return strings.TrimSpace(fallback)
}

func RunMigrate(ctx context.Context, cfg config.Config) error {
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()
	return migrate.Apply(ctx, st)
}
