package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/xuekeven/weibo-assistant/internal/browser"
	"github.com/xuekeven/weibo-assistant/internal/config"
	"github.com/xuekeven/weibo-assistant/internal/httpapi"
	"github.com/xuekeven/weibo-assistant/internal/migrate"
	"github.com/xuekeven/weibo-assistant/internal/notifier"
	"github.com/xuekeven/weibo-assistant/internal/store"
)

func RunServer(ctx context.Context, cfg config.Config) error {
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	if err := migrate.Apply(ctx, st); err != nil {
		return err
	}
	if err := st.UpdateMonitorStatus(ctx, "server_started", "", ""); err != nil {
		return err
	}

	notify := notifier.NewWeworkClient(cfg.WeworkRelayURL, cfg.WeworkRelayAPIKey, cfg.WeworkRelayToUser)
	api := httpapi.NewServer(cfg, st, notify)
	server := &http.Server{
		Addr:              api.Addr(),
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("api server listening", "addr", server.Addr)
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
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

func RunLogin(ctx context.Context, cfg config.Config) error {
	return browser.RunLogin(ctx, browser.LoginOptions{
		URL:        cfg.WeiboChatURL,
		ProfileDir: cfg.WeiboProfileDir,
		Executable: cfg.ChromeExecutable,
		NoSandbox:  cfg.ChromeNoSandbox,
	})
}

func RunMonitor(ctx context.Context, cfg config.Config) error {
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.UpdateMonitorStatus(ctx, "monitor_starting", cfg.WeiboChatURL, ""); err != nil {
		return err
	}
	notify := notifier.NewWeworkClient(cfg.WeworkRelayURL, cfg.WeworkRelayAPIKey, cfg.WeworkRelayToUser)
	imageURLByFID := make(map[string]string)

	return browser.RunMonitor(ctx, browser.MonitorOptions{
		URL:                 cfg.WeiboChatURL,
		ProfileDir:          cfg.WeiboProfileDir,
		Executable:          cfg.ChromeExecutable,
		Headless:            cfg.Headless,
		NoSandbox:           cfg.ChromeNoSandbox,
		DebugPort:           cfg.ChromeDebugPort,
		HealthCheckInterval: cfg.HealthCheckInterval,
		PageReloadInterval:  cfg.PageReloadInterval,
		TargetGroupID:       cfg.TargetGroupID,
		TargetGroupName:     cfg.TargetGroupName,
		OnProbe: func(probeCtx context.Context, probe browser.PageProbe) error {
			return st.UpdateMonitorStatus(probeCtx, probe.Status, probe.URL, probe.Error)
		},
		OnMessage: func(messageCtx context.Context, message browser.NetworkMessage) error {
			if cfg.TargetGroupID != "" && message.GroupID != cfg.TargetGroupID {
				slog.Info("ignore message from non-target group", "groupID", message.GroupID, "targetGroupID", cfg.TargetGroupID)
				return nil
			}
			enrichNetworkAttachmentsWithImageCache(message.Attachments, imageURLByFID)
			params := store.CreateMessageParams{
				WeiboMessageID: message.WeiboMessageID,
				Group: store.UpsertGroupParams{
					WeiboGroupID: message.GroupID,
					Name:         fallbackText(message.GroupName, message.GroupID),
				},
				Sender: store.UpsertUserParams{
					WeiboUserID: message.SenderID,
					Nickname:    fallbackText(message.SenderName, message.SenderID),
					AvatarURL:   message.SenderAvatar,
					Raw:         json.RawMessage(`null`),
				},
				MessageType: message.MessageType,
				Content:     message.Content,
				SentAt:      message.SentAt,
				Source:      message.Source,
				DedupeKey:   message.DedupeKey,
				Attachments: storeAttachmentsFromNetwork(message.Attachments),
				Raw:         message.Raw,
			}
			result, err := st.CreateMessage(messageCtx, params)
			if err != nil {
				return err
			}
			slog.Info("network message persisted",
				"created", result.Created,
				"messageID", result.Message.ID,
				"weiboMessageID", result.Message.WeiboMessageID,
				"groupID", result.Message.GroupID,
				"senderName", result.Message.SenderName,
			)
			maybeNotifyTargetSender(messageCtx, cfg, st, notify, result)
			return nil
		},
		OnImageReference: func(refCtx context.Context, ref browser.NetworkImageReference) error {
			for _, fid := range ref.FIDs {
				for _, imageURL := range ref.URLs {
					if fid != "" && imageURL != "" {
						imageURLByFID[fid] = imageURL
					}
					count, err := st.ResolveImageAttachmentURL(refCtx, store.ResolveImageAttachmentParams{
						FID:       fid,
						URL:       imageURL,
						SourceURL: ref.SourceURL,
					})
					if err != nil {
						return err
					}
					if count > 0 {
						slog.Info("image attachment resolved", "fid", fid, "url", imageURL, "updated", count)
					}
				}
			}
			return nil
		},
	})
}

func enrichNetworkAttachmentsWithImageCache(attachments []browser.NetworkAttachment, imageURLByFID map[string]string) {
	for index := range attachments {
		if attachments[index].AttachmentType != "image" || strings.TrimSpace(attachments[index].URL) != "" {
			continue
		}
		for _, fid := range networkAttachmentFIDs(attachments[index]) {
			if imageURL := imageURLByFID[fid]; strings.TrimSpace(imageURL) != "" {
				attachments[index].URL = imageURL
				return
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

func maybeNotifyTargetSender(ctx context.Context, cfg config.Config, st *store.Store, notify *notifier.WeworkClient, result store.CreateMessageResult) {
	if !result.Created {
		return
	}
	if !hasTargetSender(cfg) || !messageMatchesTargetSender(cfg, result.Message) {
		return
	}
	if strings.TrimSpace(cfg.WeworkRelayURL) == "" || strings.TrimSpace(cfg.WeworkRelayAPIKey) == "" {
		slog.Warn("target message matched but wework relay is not configured",
			"messageID", result.Message.ID,
			"weiboMessageID", result.Message.WeiboMessageID,
			"senderName", result.Message.SenderName,
			"weiboSenderID", result.Message.WeiboSenderID,
		)
		recordNotificationEvent(ctx, st, cfg, result.Message, "skipped", nil, "WEWORK_RELAY_URL or WEWORK_RELAY_API_KEY is empty")
		return
	}

	content := buildTargetNotificationContent(result.Message)
	response, err := notify.SendText(ctx, content)
	if err != nil {
		slog.Error("target message wework notification failed",
			"error", err,
			"messageID", result.Message.ID,
			"weiboMessageID", result.Message.WeiboMessageID,
		)
		recordNotificationEvent(ctx, st, cfg, result.Message, "failed", &response, err.Error())
		return
	}
	slog.Info("target message wework notification sent",
		"messageID", result.Message.ID,
		"weiboMessageID", result.Message.WeiboMessageID,
		"senderName", result.Message.SenderName,
	)
	recordNotificationEvent(ctx, st, cfg, result.Message, "sent", &response, "")
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
		sentAt = message.SentAt.Format("2006-01-02 15:04:05")
	}
	content := strings.TrimSpace(message.Content)
	if content == "" {
		content = fmt.Sprintf("[%s]", message.MessageType)
	}
	content = truncateRunes(content, 1500)
	return fmt.Sprintf("微博群聊新消息\n群聊：%s\n发送人：%s\n时间：%s\n内容：%s",
		fallbackText(message.GroupName, message.WeiboGroupID),
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
