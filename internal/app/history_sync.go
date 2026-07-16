package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/xuekeven/weibo-assistant/internal/browser"
	"github.com/xuekeven/weibo-assistant/internal/config"
	"github.com/xuekeven/weibo-assistant/internal/store"
)

type historyPageTarget struct {
	mu  sync.RWMutex
	url string
}

func (target *historyPageTarget) Set(url string) {
	target.mu.Lock()
	target.url = strings.TrimSpace(url)
	target.mu.Unlock()
}

func (target *historyPageTarget) Get() string {
	target.mu.RLock()
	defer target.mu.RUnlock()
	return target.url
}

type disconnectWindow struct {
	mu   sync.Mutex
	from *time.Time
	mid  string
}

func (window *disconnectWindow) MarkDisconnected(stopMID string) {
	window.mu.Lock()
	defer window.mu.Unlock()
	if window.from == nil {
		now := time.Now()
		window.from = &now
		window.mid = strings.TrimSpace(stopMID)
	}
}

func (window *disconnectWindow) TakeRecoveryWindow() (*time.Time, *time.Time, string) {
	window.mu.Lock()
	defer window.mu.Unlock()
	if window.from == nil {
		return nil, nil, ""
	}
	from := *window.from
	to := time.Now()
	stopMID := window.mid
	window.from = nil
	window.mid = ""
	return &from, &to, stopMID
}

func runHistorySyncWorker(ctx context.Context, cfg config.Config, st *store.Store, metrics *monitorMetrics, imageURLByFID *imageURLCache, target *historyPageTarget) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if targetURL := target.Get(); targetURL != "" {
			job, err := st.ClaimHistorySync(ctx, 10*time.Minute)
			switch {
			case err == nil:
				if runErr := processHistorySyncJob(ctx, cfg, st, metrics, imageURLByFID, targetURL, job); runErr != nil {
					slog.Warn("history sync job failed", "jobID", job.ID, "mode", job.Mode, "error", runErr)
					if markErr := st.FailHistorySync(ctx, job.ID, runErr); markErr != nil {
						slog.Warn("mark history sync failed", "jobID", job.ID, "error", markErr)
					}
				}
				continue
			case !errors.Is(err, pgx.ErrNoRows):
				slog.Warn("claim history sync job failed", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func processHistorySyncJob(ctx context.Context, cfg config.Config, st *store.Store, metrics *monitorMetrics, imageURLByFID *imageURLCache, targetURL string, job store.HistorySyncJob) error {
	cursor := strings.TrimSpace(job.CursorMID)
	if cursor == "" && job.Mode == "manual" {
		oldestMID, err := st.GetOldestMessageMID(ctx, job.GroupID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		cursor = oldestMID
	}
	if cursor == "" {
		cursor = "0"
	}
	progress := store.HistorySyncProgress{CursorMID: cursor}
	stopReason := "max_pages_reached"
	stopMID := strings.TrimSpace(job.StopMID)

	for progress.PagesFetched < job.MaxPages {
		pageCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		page, err := browser.FetchGroupHistoryPage(pageCtx, targetURL, browser.GroupHistoryRequest{
			GroupID: job.WeiboGroupID,
			MaxMID:  cursor,
			Count:   20,
		}, historyMessageSource(job.Mode))
		cancel()
		if err != nil {
			return err
		}
		progress.PagesFetched++
		progress.MessagesSeen += len(page.Messages)
		if len(page.Messages) == 0 {
			stopReason = "no_more_messages"
			break
		}

		stop := false
		// 接口按旧到新返回。倒序处理才能先保存断线后的新消息，再在连续的已知消息处停止。
		for index := len(page.Messages) - 1; index >= 0; index-- {
			message := page.Messages[index]
			exists, existedAtSnapshot, err := st.MessageExistenceAt(ctx, message.WeiboMessageID, job.CreatedAt)
			if err != nil {
				return err
			}
			if reached, reason := historySyncBoundaryReached(stopMID, message.WeiboMessageID, existedAtSnapshot); reached {
				stopReason = reason
				stop = true
				break
			}
			if exists {
				continue
			}
			if job.RequestedFrom != nil && message.SentAt != nil && message.SentAt.Before(*job.RequestedFrom) {
				stopReason = "requested_start_reached"
				stop = true
				break
			}
			if job.RequestedTo != nil && message.SentAt != nil && message.SentAt.After(*job.RequestedTo) {
				continue
			}
			created, err := persistHistoryMessage(ctx, cfg, st, metrics, imageURLByFID, job, message)
			if err != nil {
				return err
			}
			if created {
				progress.MessagesInserted++
			}
		}

		if page.OldestMID == "" || page.OldestMID == cursor {
			if !stop {
				stopReason = "cursor_not_advanced"
			}
			stop = true
		} else {
			cursor = page.OldestMID
			progress.CursorMID = cursor
		}
		if err := st.UpdateHistorySyncProgress(ctx, job.ID, progress); err != nil {
			return err
		}
		if stop {
			break
		}
	}
	if err := st.CompleteHistorySync(ctx, job.ID, progress, stopReason); err != nil {
		return err
	}
	slog.Info("history sync job completed",
		"jobID", job.ID,
		"mode", job.Mode,
		"pages", progress.PagesFetched,
		"seen", progress.MessagesSeen,
		"inserted", progress.MessagesInserted,
		"stopReason", stopReason,
	)
	return nil
}

func historySyncBoundaryReached(stopMID string, messageMID string, existedAtSnapshot bool) (bool, string) {
	if strings.TrimSpace(stopMID) != "" && strings.TrimSpace(messageMID) == strings.TrimSpace(stopMID) {
		return true, "stop_mid_reached"
	}
	if existedAtSnapshot {
		return true, "existing_message_reached"
	}
	return false, ""
}

func persistHistoryMessage(ctx context.Context, cfg config.Config, st *store.Store, metrics *monitorMetrics, imageURLByFID *imageURLCache, job store.HistorySyncJob, message browser.NetworkMessage) (bool, error) {
	if message.GroupID != "" && message.GroupID != job.WeiboGroupID {
		return false, nil
	}
	message.GroupID = job.WeiboGroupID
	if strings.TrimSpace(message.GroupName) == "" {
		message.GroupName = job.GroupName
	}
	enrichNetworkAttachmentsWithImageCache(message.Attachments, imageURLByFID)
	var notification *store.EnqueueNotificationParams
	if job.Mode == "recovery" {
		notification, _ = notificationForNetworkMessage(cfg, message)
	}
	result, err := st.CreateMessage(ctx, store.CreateMessageParams{
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
		MessageType:  message.MessageType,
		Content:      message.Content,
		SentAt:       message.SentAt,
		Source:       message.Source,
		DedupeKey:    message.DedupeKey,
		Attachments:  storeAttachmentsFromNetwork(message.Attachments),
		Raw:          message.Raw,
		Notification: notification,
	})
	if err != nil {
		metrics.persistFailure.Add(1)
		return false, err
	}
	metrics.persistSuccess.Add(1)
	if !result.Created {
		metrics.dedupeTotal.Add(1)
	}
	if err := st.MarkMonitorMessage(ctx); err != nil {
		slog.Warn("update monitor last message time after history sync failed", "error", err)
	}
	return result.Created, nil
}

func historyMessageSource(mode string) string {
	switch mode {
	case "startup":
		return "weibo_startup"
	case "recovery":
		return "weibo_recovery"
	default:
		return "weibo_history"
	}
}

func enqueueRecoveryHistorySync(ctx context.Context, st *store.Store, group store.Group, from *time.Time, to *time.Time, stopMID string) {
	if from == nil || to == nil || group.ID <= 0 {
		return
	}
	job, err := st.EnqueueHistorySync(ctx, store.EnqueueHistorySyncParams{
		GroupID:       group.ID,
		Mode:          "recovery",
		RequestedFrom: from,
		RequestedTo:   to,
		MaxPages:      100,
		StopMID:       stopMID,
	})
	if err != nil {
		slog.Warn("enqueue recovery history sync failed", "error", err)
		return
	}
	slog.Info("recovery history sync queued", "jobID", job.ID, "from", from, "to", to)
}

func enqueueStartupHistorySync(ctx context.Context, st *store.Store, group store.Group) error {
	if group.ID <= 0 {
		return fmt.Errorf("cannot enqueue startup history sync without a group")
	}
	stopMID, err := st.GetNewestHistoryBoundaryMID(ctx, group.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	job, err := st.EnqueueHistorySync(ctx, store.EnqueueHistorySyncParams{
		GroupID:  group.ID,
		Mode:     "startup",
		MaxPages: 1000,
		StopMID:  stopMID,
	})
	if err != nil {
		return err
	}
	slog.Info("startup history sync queued", "jobID", job.ID, "groupID", group.WeiboGroupID)
	return nil
}

func recordHistoryDisconnect(ctx context.Context, st *store.Store, group store.Group, window *disconnectWindow) {
	stopMID := ""
	if group.ID > 0 {
		value, err := st.GetNewestHistoryBoundaryMID(ctx, group.ID)
		if err == nil {
			stopMID = value
		} else if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("load disconnect history boundary failed", "error", err)
		}
	}
	window.MarkDisconnected(stopMID)
}
