package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestFetchImageThumbnailURLAgainstLoggedInBrowser(t *testing.T) {
	if os.Getenv("WEIBO_MEDIA_INTEGRATION") != "1" {
		t.Skip("set WEIBO_MEDIA_INTEGRATION=1 to use the logged-in browser")
	}
	fid := strings.TrimSpace(os.Getenv("WEIBO_MEDIA_TEST_FID"))
	if fid == "" {
		fid = "5322532208255108"
	}
	port := 9222
	if configured := strings.TrimSpace(os.Getenv("CHROME_DEBUG_PORT")); configured != "" {
		value, err := strconv.Atoi(configured)
		if err != nil {
			t.Fatal(err)
		}
		port = value
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	targetURL, err := integrationPageTarget(ctx, port)
	if err != nil {
		t.Fatal(err)
	}
	thumbnail, sourceURL, err := FetchImageThumbnailURL(ctx, targetURL, fid)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(thumbnail, "fid="+fid) || !strings.Contains(sourceURL, "meta_query.json") {
		t.Fatalf("unexpected resolution: thumbnail=%q source=%q", thumbnail, sourceURL)
	}
	t.Logf("resolved fid %s to %s", fid, thumbnail)
}

func integrationPageTarget(ctx context.Context, port int) (string, error) {
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
		if target.Type == "page" && strings.Contains(target.URL, "api.weibo.com/chat") && target.WebSocketDebuggerURL != "" {
			return target.WebSocketDebuggerURL, nil
		}
	}
	return "", fmt.Errorf("logged-in Weibo chat page target not found")
}
