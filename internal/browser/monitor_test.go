package browser

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSelectPageTargetPrefersWeiboChat(t *testing.T) {
	targets := []chromeTarget{
		{Type: "page", URL: "https://example.com", WebSocketDebuggerURL: "ws://example"},
		{Type: "worker", URL: "https://api.weibo.com/chat#/worker", WebSocketDebuggerURL: "ws://worker"},
		{Type: "page", URL: "https://api.weibo.com/chat#/chat", WebSocketDebuggerURL: "ws://chat"},
	}

	target, ok := selectPageTarget(targets)
	if !ok {
		t.Fatal("expected a page target")
	}
	if target.WebSocketDebuggerURL != "ws://chat" {
		t.Fatalf("expected chat target, got %#v", target)
	}
}

func TestClassifyPageDetectsLoginAndChat(t *testing.T) {
	tests := []struct {
		name  string
		url   string
		title string
		want  string
	}{
		{name: "chat", url: "https://api.weibo.com/chat#/chat", title: "微博聊天", want: "page_ok"},
		{name: "passport login", url: "https://passport.weibo.com/login", title: "微博", want: "login_required"},
		{name: "login title", url: "https://api.weibo.com/chat#/chat", title: "请登录", want: "login_required"},
		{name: "other page", url: "https://weibo.com", title: "微博", want: "page_open"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyPage(tt.url, tt.title); got != tt.want {
				t.Fatalf("classifyPage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRememberNetworkRequestKeepsBoundedSize(t *testing.T) {
	requests := make(map[string]networkRequestInfo)
	rememberNetworkRequest(requests, "request-1", networkRequestInfo{}, 2)
	time.Sleep(time.Millisecond)
	rememberNetworkRequest(requests, "request-2", networkRequestInfo{}, 2)
	time.Sleep(time.Millisecond)
	rememberNetworkRequest(requests, "request-3", networkRequestInfo{}, 2)

	if len(requests) != 2 {
		t.Fatalf("expected bounded request map, got %d", len(requests))
	}
	if _, ok := requests["request-1"]; ok {
		t.Fatal("expected oldest request to be evicted")
	}
}

func TestParseNetworkMessagesImageWithFIDs(t *testing.T) {
	payload := `[
		{
			"data": {
				"type": "groupchat",
				"sub_type": 321,
				"info": {
					"gid": 4761715839862414,
					"group_name": "茧房建筑师协会",
					"content": "分享图片",
					"from_uid": 2104906147,
					"from_user": {
						"screen_name": "slipknot-li",
						"profile_image_url": "https://tvax1.sinaimg.cn/crop.avatar.jpg"
					},
					"current_version": 5318460893568964,
					"media_type": 1,
					"fids": [5318460894348385],
					"annotations": {
						"push_proc_ext": {
							"send_time": 1783503352612
						}
					}
				}
			},
			"channel": "/im/7608757850"
		}
	]`

	messages := parseNetworkMessages(payload)
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}

	message := messages[0]
	if message.MessageType != "image" {
		t.Fatalf("expected image message type, got %q", message.MessageType)
	}
	if len(message.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(message.Attachments))
	}
	attachment := message.Attachments[0]
	expectedURL := "https://upload.api.weibo.com/2/mss/msget_thumbnail?fid=5318460894348385&high=240&width=240&size=111,240&source=209678993&imageType=origin"
	if attachment.URL != expectedURL {
		t.Fatalf("unexpected generated image URL: %q", attachment.URL)
	}

	var meta struct {
		FIDs      []string `json:"fids"`
		URLStatus string   `json:"url_status"`
	}
	if err := json.Unmarshal(attachment.Meta, &meta); err != nil {
		t.Fatalf("unmarshal attachment meta: %v", err)
	}
	if len(meta.FIDs) != 1 || meta.FIDs[0] != "5318460894348385" {
		t.Fatalf("unexpected fids: %#v", meta.FIDs)
	}
	if meta.URLStatus != "" {
		t.Fatalf("unexpected url status: %q", meta.URLStatus)
	}
}

func TestExtractImageReferencesFromBody(t *testing.T) {
	body := `{
		"data": {
			"messages": [
				{
					"fids": ["5318460894348385"],
					"pics": [
						{"url": "https://wx1.sinaimg.cn/large/abc123.jpg"}
					],
					"from_user": {
						"profile_image_url": "https://tvax1.sinaimg.cn/crop.avatar.jpg"
					}
				}
			]
		}
	}`

	refs := extractImageReferencesFromBody(body, "https://api.weibo.com/example")
	if len(refs) == 0 {
		t.Fatal("expected image reference")
	}

	found := false
	for _, ref := range refs {
		if len(ref.FIDs) == 1 &&
			ref.FIDs[0] == "5318460894348385" &&
			len(ref.URLs) == 1 &&
			ref.URLs[0] == "https://wx1.sinaimg.cn/large/abc123.jpg" {
			found = true
		}
		for _, url := range ref.URLs {
			if url == "https://tvax1.sinaimg.cn/crop.avatar.jpg" {
				t.Fatalf("avatar URL should not be extracted as image message URL: %#v", refs)
			}
		}
	}
	if !found {
		t.Fatalf("expected fid/image URL pair, got %#v", refs)
	}
}

func TestExtractImageReferencesFromBodyWithPicID(t *testing.T) {
	body := `{
		"pic_infos": {
			"abc123": {
				"pic_id": "abc123",
				"large": {
					"url": "https://wx2.sinaimg.cn/large/abc123.jpg"
				}
			}
		}
	}`

	refs := extractImageReferencesFromBody(body, "https://api.weibo.com/example")
	if len(refs) == 0 {
		t.Fatal("expected pic_id image reference")
	}
	if refs[0].FIDs[0] != "abc123" || refs[0].URLs[0] != "https://wx2.sinaimg.cn/large/abc123.jpg" {
		t.Fatalf("unexpected pic_id refs: %#v", refs)
	}
}

func TestExtractImageReferencesFromURL(t *testing.T) {
	refs := extractImageReferencesFromURL(
		"https://api.weibo.com/chat/image?fid=5318460894348385",
		"https://wx1.sinaimg.cn/large/abc123.jpg",
	)
	if len(refs) != 1 {
		t.Fatalf("expected 1 url reference, got %#v", refs)
	}
	if refs[0].FIDs[0] != "5318460894348385" || refs[0].URLs[0] != "https://wx1.sinaimg.cn/large/abc123.jpg" {
		t.Fatalf("unexpected url refs: %#v", refs)
	}
}

func TestExtractImageReferencesFromWeiboThumbnailURL(t *testing.T) {
	src := "https://upload.api.weibo.com/2/mss/msget_thumbnail?fid=5318817577437843&high=240&width=240&size=111,240&source=209678993&imageType=origin"
	refs := extractImageReferencesFromURL(src, src)
	if len(refs) != 1 {
		t.Fatalf("expected 1 thumbnail reference, got %#v", refs)
	}
	if refs[0].FIDs[0] != "5318817577437843" || refs[0].URLs[0] != src {
		t.Fatalf("unexpected thumbnail refs: %#v", refs)
	}
}

func TestNetworkReconnectDelayUsesCappedExponentialBackoff(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 0, want: time.Second},
		{attempt: 1, want: time.Second},
		{attempt: 2, want: 2 * time.Second},
		{attempt: 3, want: 4 * time.Second},
		{attempt: 6, want: 30 * time.Second},
		{attempt: 20, want: 30 * time.Second},
	}

	for _, test := range tests {
		if got := networkReconnectDelay(test.attempt); got != test.want {
			t.Fatalf("attempt %d: expected %s, got %s", test.attempt, test.want, got)
		}
	}
}
