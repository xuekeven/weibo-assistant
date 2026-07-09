package browser

import (
	"encoding/json"
	"testing"
)

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
	if attachment.URL != "" {
		t.Fatalf("expected no image URL from websocket-only payload, got %q", attachment.URL)
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
	if meta.URLStatus != "missing_in_websocket_payload" {
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
