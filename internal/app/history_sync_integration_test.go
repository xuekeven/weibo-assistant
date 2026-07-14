package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/xuekeven/weibo-assistant/internal/browser"
	"github.com/xuekeven/weibo-assistant/internal/config"
	"github.com/xuekeven/weibo-assistant/internal/store"
)

func TestHistorySyncAgainstLoggedInBrowser(t *testing.T) {
	if os.Getenv("WEIBO_HISTORY_INTEGRATION") != "1" {
		t.Skip("set WEIBO_HISTORY_INTEGRATION=1 to use the logged-in browser and configured database")
	}
	cfg, err := config.Load("../../.env")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	group, err := st.UpsertGroup(ctx, store.UpsertGroupParams{
		WeiboGroupID: cfg.TargetGroupID,
		Name:         fallbackText(cfg.TargetGroupName, cfg.TargetGroupID),
	})
	if err != nil {
		t.Fatal(err)
	}
	stopMID, err := st.GetNewestMessageMID(ctx, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := st.EnqueueHistorySync(ctx, store.EnqueueHistorySyncParams{
		GroupID:  group.ID,
		Mode:     "startup",
		StopMID:  stopMID,
		MaxPages: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if job.Status == "pending" {
		job, err = st.ClaimHistorySync(ctx, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
	}
	targetURL, err := integrationChatTarget(ctx, cfg.ChromeDebugPort)
	if err != nil {
		t.Fatal(err)
	}
	metrics := newMonitorMetrics()
	cache := newImageURLCache(cfg.ImageCacheTTL, cfg.ImageCacheMaxItems)
	page, err := browser.FetchGroupHistoryPage(ctx, targetURL, browser.GroupHistoryRequest{
		GroupID: group.WeiboGroupID,
		MaxMID:  "0",
		Count:   20,
	}, "weibo_startup")
	if err != nil {
		t.Fatal(err)
	}
	existing := 0
	groupMismatch := 0
	for _, message := range page.Messages {
		if message.GroupID != group.WeiboGroupID {
			groupMismatch++
		}
		found, _, findErr := st.MessageExistenceAt(ctx, message.WeiboMessageID, time.Now())
		if findErr != nil {
			t.Fatal(findErr)
		}
		if found {
			existing++
		}
	}
	t.Logf("page diagnostics: messages=%d existing=%d groupMismatch=%d oldest=%s newest=%s stop=%s", len(page.Messages), existing, groupMismatch, page.OldestMID, page.NewestMID, stopMID)
	if err := processHistorySyncJob(ctx, cfg, st, metrics, cache, targetURL, job); err != nil {
		t.Fatal(err)
	}
	completed, err := st.GetHistorySyncJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "completed" || completed.PagesFetched != 1 {
		t.Fatalf("unexpected completed job: %#v", completed)
	}
	t.Logf("history sync completed: inserted=%d stop=%s", completed.MessagesInserted, completed.StopReason)
}

func integrationChatTarget(ctx context.Context, port int) (string, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/json", port), nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	var targets []struct {
		Type                 string `json:"type"`
		URL                  string `json:"url"`
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(response.Body).Decode(&targets); err != nil {
		return "", err
	}
	for _, target := range targets {
		if target.Type == "page" && target.WebSocketDebuggerURL != "" {
			return target.WebSocketDebuggerURL, nil
		}
	}
	return "", fmt.Errorf("chat page target not found")
}
