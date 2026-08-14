package browser

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"
)

func TestMonitorArgsAvoidRendererBusyLoopAndAllowBackgroundThrottling(t *testing.T) {
	args := monitorArgs(MonitorOptions{URL: "https://api.weibo.com/chat#/chat"}, "/tmp/weibo-profile")
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "--disable-features=BusyLoopOnMainThread") {
		t.Fatalf("expected renderer busy loop to be disabled, got %q", joined)
	}
	for _, unwanted := range []string{
		"--disable-background-timer-throttling",
		"--disable-backgrounding-occluded-windows",
		"--disable-renderer-backgrounding",
	} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("expected Chromium background throttling to remain enabled; found %q", unwanted)
		}
	}
}

func TestMonitoredPageMediaSuppressionCoversNotificationAudioAndFullMedia(t *testing.T) {
	joined := strings.Join(monitoredPageBlockedURLs, " ")
	for _, expected := range []string{"conchfairy.sinajs.cn/lib/chat.mp3", "upload.api.weibo.com/2/mss/msget?"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("blocked URL patterns %q do not contain %q", joined, expected)
		}
	}
	for _, expected := range []string{"HTMLMediaElement.prototype.play", "MutationObserver", "audio,video"} {
		if !strings.Contains(suppressMonitoredPageMediaExpression, expected) {
			t.Fatalf("media suppression expression does not contain %q", expected)
		}
	}
}

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

func TestLoginQRCodeTargetValidationAndScale(t *testing.T) {
	valid := loginQRCodeTarget{
		Found:  true,
		X:      120,
		Y:      80,
		Width:  240,
		Height: 240,
	}
	if !valid.valid() {
		t.Fatal("expected QR code target to be valid")
	}
	if got := valid.screenshotScale(); got != 2 {
		t.Fatalf("screenshotScale() = %v, want 2", got)
	}

	large := loginQRCodeTarget{
		Found:  true,
		X:      0,
		Y:      0,
		Width:  800,
		Height: 800,
	}
	if got := large.screenshotScale(); got != 1.28 {
		t.Fatalf("screenshotScale() = %v, want 1.28", got)
	}

	invalid := loginQRCodeTarget{
		Found:  true,
		X:      0,
		Y:      0,
		Width:  40,
		Height: 40,
	}
	if invalid.valid() {
		t.Fatal("expected undersized QR code target to be rejected")
	}
}

func TestDecodeDataURLAndNormalizePNG(t *testing.T) {
	sourceImage := image.NewRGBA(image.Rect(0, 0, 100, 100))
	sourceImage.Set(50, 50, color.Black)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, sourceImage); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}

	source := "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
	decoded, err := decodeDataURL(source)
	if err != nil {
		t.Fatalf("decodeDataURL() error = %v", err)
	}
	normalized, err := normalizePNG(decoded)
	if err != nil {
		t.Fatalf("normalizePNG() error = %v", err)
	}
	if !bytes.Equal(normalized, encoded.Bytes()) {
		t.Fatal("expected an existing PNG resource to remain unchanged")
	}
}

func TestNormalizePNGRejectsUnexpectedDimensions(t *testing.T) {
	sourceImage := image.NewRGBA(image.Rect(0, 0, 40, 40))
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, sourceImage); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	if _, err := normalizePNG(encoded.Bytes()); err == nil {
		t.Fatal("expected undersized QR resource to be rejected")
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
	if message.WeiboMessageID != "5318460893568964" {
		t.Fatalf("expected current_version fallback, got %q", message.WeiboMessageID)
	}
	if message.MessageType != "image" {
		t.Fatalf("expected image message type, got %q", message.MessageType)
	}
	if len(message.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(message.Attachments))
	}
	attachment := message.Attachments[0]
	if attachment.URL != "" {
		t.Fatalf("image URL must wait for the real Weibo thumbnail dimensions, got %q", attachment.URL)
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
	if meta.URLStatus != "missing_in_message_payload" {
		t.Fatalf("unexpected url status: %q", meta.URLStatus)
	}
}

func TestParseNetworkMessagesImageIgnoresRoleIcon(t *testing.T) {
	payload := `[
		{
			"data": {
				"type": "groupchat",
				"sub_type": 321,
				"info": {
					"gid": 4761715839862414,
					"id": 5322381207801843,
					"content": "分享图片",
					"from_uid": 1401527553,
					"media_type": 1,
					"fids": [5322381209113145],
					"role": 2,
					"role_icon": "https://h5.sinaimg.cn/upload/100/1158/2020/05/06/videopicfeed3.png"
				}
			}
		}
	]`

	messages := parseNetworkMessages(payload)
	if len(messages) != 1 || len(messages[0].Attachments) != 1 {
		t.Fatalf("unexpected parsed messages: %#v", messages)
	}
	attachment := messages[0].Attachments[0]
	if attachment.URL != "" {
		t.Fatalf("role icon must not become the image attachment: %#v", attachment)
	}
	if fids := networkAttachmentFIDsForTest(attachment); len(fids) != 1 || fids[0] != "5322381209113145" {
		t.Fatalf("unexpected attachment fids: %#v", fids)
	}
}

func TestExtractImageReferencesFromBodyIgnoresRoleCrownAsset(t *testing.T) {
	body := `{
		"messages": [{
			"fid": "5322381209113145",
			"role_icon": "https://h5.sinaimg.cn/upload/100/1158/2020/05/06/videopicfeed3.png",
			"thumbnail": "https://upload.api.weibo.com/2/mss/msget_thumbnail?fid=5322381209113145&size=240,136"
		}]
	}`
	refs := extractImageReferencesFromBody(body, "https://api.weibo.com/webim/groupchat/query_messages.json")
	if len(refs) == 0 {
		t.Fatal("expected the real fid-addressed thumbnail reference")
	}
	for _, ref := range refs {
		for _, imageURL := range ref.URLs {
			if strings.Contains(imageURL, "videopicfeed3.png") {
				t.Fatalf("role crown asset must not be correlated with an image fid: %#v", refs)
			}
		}
	}
}

func TestExtractImageReferencesFromURLIgnoresRoleCrownAsset(t *testing.T) {
	roleIcon := "https://h5.sinaimg.cn/upload/100/1158/2020/05/06/videopicfeed3.png?fid=5322381209113145"
	if refs := extractImageReferencesFromURL(roleIcon, roleIcon); len(refs) != 0 {
		t.Fatalf("role crown asset must not become an image reference: %#v", refs)
	}
}

func TestImageAttachmentsRejectMismatchedMSSFID(t *testing.T) {
	raw := json.RawMessage(`{
		"media_type": 1,
		"fids": [5322381209113145],
		"pics": [{
			"url": "https://upload.api.weibo.com/2/mss/msget_thumbnail?fid=999&high=240&width=240&size=240,136"
		}]
	}`)
	attachments := imageAttachmentsFromInfo(raw, 1, []json.Number{"5322381209113145"})
	if len(attachments) != 1 || attachments[0].URL != "" {
		t.Fatalf("mismatched MSS URL must be discarded: %#v", attachments)
	}
}

func TestImageAttachmentsKeepScopedSinaimgURL(t *testing.T) {
	raw := json.RawMessage(`{
		"media_type": 1,
		"fids": [5322381209113145],
		"pics": [{"url": "https://wx1.sinaimg.cn/large/abc123.jpg"}]
	}`)
	attachments := imageAttachmentsFromInfo(raw, 1, []json.Number{"5322381209113145"})
	if len(attachments) != 1 || attachments[0].URL != "https://wx1.sinaimg.cn/large/abc123.jpg" {
		t.Fatalf("scoped sinaimg URL should remain valid: %#v", attachments)
	}
}

func networkAttachmentFIDsForTest(attachment NetworkAttachment) []string {
	var meta struct {
		FIDs []string `json:"fids"`
	}
	if json.Unmarshal(attachment.Meta, &meta) != nil {
		return nil
	}
	return meta.FIDs
}

func TestParseNetworkMessagesVideoWithCoverFID(t *testing.T) {
	payload := `[
		{
			"data": {
				"type": "groupchat",
				"sub_type": 321,
				"info": {
					"gid": 4761715839862414,
					"content": "分享视频",
					"from_uid": 2706566322,
					"from_user": {
						"screen_name": "拾人牙慧的复读机",
						"profile_image_url": "https://tvax2.sinaimg.cn/avatar.jpg"
					},
					"media_type": 10,
					"type": 321,
					"id": 5321456835628344,
					"time": 1784217640,
					"fids": [5321456833529563],
					"annotations": {
						"include_video": 0,
						"video_pic_fid": 5321456092450432
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
	if message.MessageType != "video" {
		t.Fatalf("expected video message type, got %q", message.MessageType)
	}
	if len(message.Attachments) != 2 {
		t.Fatalf("expected cover and video attachments, got %#v", message.Attachments)
	}
	if message.Attachments[0].AttachmentType != "image" {
		t.Fatalf("expected first attachment to be video cover image, got %#v", message.Attachments[0])
	}
	if message.Attachments[1].AttachmentType != "video" {
		t.Fatalf("expected second attachment to be video metadata, got %#v", message.Attachments[1])
	}
}

func TestParseNetworkMessagesUsesStableIDAcrossVersionChanges(t *testing.T) {
	payload := `[
		{
			"data": {
				"type": "groupchat",
				"sub_type": 321,
				"info": {
					"id": 5320231658456624,
					"gid": 4761715839862414,
					"content": "不过这周应该能跌个差不多",
					"from_uid": 1401527553,
					"current_version": 5320231658456966
				}
			}
		},
		{
			"data": {
				"type": "groupchat",
				"sub_type": 321,
				"info": {
					"id": 5320231658456624,
					"gid": 4761715839862414,
					"content": "不过这周应该能跌个差不多",
					"from_uid": 1401527553,
					"current_version": 5320231658456970
				}
			}
		}
	]`

	messages := parseNetworkMessages(payload)
	if len(messages) != 2 {
		t.Fatalf("expected 2 delivery events, got %d", len(messages))
	}
	for _, message := range messages {
		if message.WeiboMessageID != "5320231658456624" {
			t.Fatalf("expected stable info.id, got %q", message.WeiboMessageID)
		}
		if message.DedupeKey != "weibo_message:5320231658456624" {
			t.Fatalf("unexpected dedupe key %q", message.DedupeKey)
		}
	}
}

func TestParseNetworkMessagesMemberLevelUpgrade(t *testing.T) {
	payload := `[
		{
			"data": {
				"type": "groupchat",
				"sub_type": 101,
				"info": {
					"template": "恭喜 {{user.DATA}} 升级至{{level.DATA}}",
					"gid": 4761715839862414,
					"data": {
						"level": {"value": "LV6 建筑大佬"},
						"user": {"value": "宁愿坐在自行车上笑ZJU"}
					},
					"type": 321,
					"from_uid": 5394206608,
					"content": "恭喜 宁愿坐在自行车上笑ZJU 升级至LV6 建筑大佬",
					"from_user": {"screen_name": "粉丝群"},
					"sub_type": 101,
					"id": 5320374565997228,
					"time": 1783959607
				}
			}
		}
	]`
	if !looksLikeMessagePayload(payload) {
		t.Fatal("system groupchat payload must reach the parser")
	}

	messages := parseNetworkMessages(payload)
	if len(messages) != 1 {
		t.Fatalf("expected 1 system message, got %d", len(messages))
	}
	message := messages[0]
	if message.MessageType != "system" {
		t.Fatalf("message type = %q, want system", message.MessageType)
	}
	if message.WeiboMessageID != "5320374565997228" {
		t.Fatalf("message id = %q", message.WeiboMessageID)
	}
	if message.SenderID != "5394206608" || message.SenderName != "粉丝群" {
		t.Fatalf("unexpected sender: %s %s", message.SenderID, message.SenderName)
	}
	if message.SentAt == nil || message.SentAt.Unix() != 1783959607 {
		t.Fatalf("unexpected sent time: %v", message.SentAt)
	}
}

func TestParseNetworkMessagesKeepsUnknownGroupChatType(t *testing.T) {
	payload := `[{"data":{"type":"groupchat","sub_type":909,"info":{"gid":4761715839862414,"sub_type":909}}}]`

	messages := parseNetworkMessages(payload)
	if len(messages) != 1 {
		t.Fatalf("expected unknown groupchat event to be retained, got %d", len(messages))
	}
	message := messages[0]
	if message.Content != "" {
		t.Fatalf("archive-only event must not get display content: %q", message.Content)
	}
	if message.MessageType != "event" {
		t.Fatalf("fallback event type = %q, want event", message.MessageType)
	}
	if message.WeiboMessageID == "" || message.SenderID != "weibo-system:909" {
		t.Fatalf("missing fallback identifiers: %#v", message)
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

func TestExtractImageReferencesFromBodyDoesNotPairSiblingMessages(t *testing.T) {
	body := `{
		"data": {
			"messages": [
				{
					"fids": ["5320938923230262"],
					"pics": [{"url": "https://wx1.sinaimg.cn/large/nuts.jpg"}]
				},
				{
					"fids": ["5320938924000000"],
					"pics": [{"url": "https://ww1.sinaimg.cn/large/sunset.jpg"}]
				}
			]
		}
	}`

	refs := extractImageReferencesFromBody(body, "https://api.weibo.com/webim/groupchat/query_messages.json")
	for _, ref := range refs {
		for _, fid := range ref.FIDs {
			for _, imageURL := range ref.URLs {
				if fid == "5320938923230262" && imageURL == "https://ww1.sinaimg.cn/large/sunset.jpg" {
					t.Fatalf("target fid was paired with a sibling message image: %#v", refs)
				}
				if fid == "5320938924000000" && imageURL == "https://wx1.sinaimg.cn/large/nuts.jpg" {
					t.Fatalf("sibling fid was paired with the target image: %#v", refs)
				}
			}
		}
	}

	wantPairs := map[string]string{
		"5320938923230262": "https://wx1.sinaimg.cn/large/nuts.jpg",
		"5320938924000000": "https://ww1.sinaimg.cn/large/sunset.jpg",
	}
	for fid, wantURL := range wantPairs {
		found := false
		for _, ref := range refs {
			if len(ref.FIDs) == 1 && ref.FIDs[0] == fid && len(ref.URLs) == 1 && ref.URLs[0] == wantURL {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing local fid/image pair %s -> %s in %#v", fid, wantURL, refs)
		}
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

func TestNetworkURLLogValueRedactsPayloadAndUnknownQueryValues(t *testing.T) {
	dataURL := "data:image/png;base64,very-secret-payload"
	if got := networkURLLogValue(dataURL); strings.Contains(got, "very-secret-payload") {
		t.Fatalf("data URL payload leaked into log value: %q", got)
	}

	rawURL := "https://api.weibo.com/webim/groupchat/query_messages.json?fid=123&gid=456&token=secret&callback=private"
	got := networkURLLogValue(rawURL)
	if !strings.Contains(got, "fid=123") || !strings.Contains(got, "gid=456") {
		t.Fatalf("useful identifiers missing from log value: %q", got)
	}
	if strings.Contains(got, "secret") || strings.Contains(got, "private") {
		t.Fatalf("sensitive query value leaked into log value: %q", got)
	}
	if !strings.Contains(got, "redacted-query-keys:2") {
		t.Fatalf("redaction count missing from log value: %q", got)
	}

	relativeURL := "/messages?token=secret"
	if got := networkURLLogValue(relativeURL); strings.Contains(got, "secret") || strings.Contains(got, "token") {
		t.Fatalf("relative URL detail leaked into log value: %q", got)
	}
}
