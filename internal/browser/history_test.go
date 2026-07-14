package browser

import "testing"

func TestParseGroupHistoryResponse(t *testing.T) {
	body := []byte(`{
		"result": true,
		"last_read_mid": 5320597183400367,
		"messages": [
			{
				"gid": 4761715839862414,
				"id": 5320591567226651,
				"time": 1784011344,
				"type": 321,
				"from_uid": 10001,
				"content": "较早消息",
				"from_user": {"screen_name": "用户一"},
				"media_type": 0
			},
			{
				"gid": 4761715839862414,
				"id": 5320597183400367,
				"time": 1784012683,
				"type": 321,
				"from_uid": 10002,
				"content": "分享图片",
				"from_user": {"screen_name": "用户二"},
				"media_type": 1,
				"fids": [5320597183400999]
			}
		],
		"group_tips": [],
		"significant_msgs": []
	}`)

	page, err := ParseGroupHistoryResponse(body, "4761715839862414", "weibo_startup")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(page.Messages))
	}
	if page.OldestMID != "5320591567226651" || page.NewestMID != "5320597183400367" {
		t.Fatalf("unexpected page bounds: oldest=%s newest=%s", page.OldestMID, page.NewestMID)
	}
	if page.Messages[0].Source != "weibo_startup" || page.Messages[0].Content != "较早消息" {
		t.Fatalf("unexpected first message: %#v", page.Messages[0])
	}
	image := page.Messages[1]
	if image.MessageType != "image" || len(image.Attachments) != 1 {
		t.Fatalf("history image was not parsed: %#v", image)
	}
}

func TestParseGroupHistoryResponseKeepsSystemAndArchiveOnlyEvents(t *testing.T) {
	body := []byte(`{
		"result": true,
		"messages": [
			{
				"gid": 4761715839862414,
				"id": 5320374565997228,
				"time": 1783959607,
				"type": 321,
				"sub_type": 101,
				"from_uid": 5394206608,
				"template": "恭喜 {{user.DATA}} 升级至{{level.DATA}}",
				"data": {"user": {"value": "测试用户"}, "level": {"value": "LV6"}},
				"from_user": {"screen_name": "粉丝群"}
			},
			{
				"gid": 4761715839862414,
				"id": 5320537790219144,
				"time": 1784000000,
				"type": 332,
				"from_uid": 10001
			}
		]
	}`)

	page, err := ParseGroupHistoryResponse(body, "4761715839862414", "weibo_history")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(page.Messages))
	}
	if page.Messages[0].MessageType != "system" || page.Messages[0].Content != "恭喜 测试用户 升级至LV6" {
		t.Fatalf("unexpected system message: %#v", page.Messages[0])
	}
	if page.Messages[1].MessageType != "event" || page.Messages[1].Content != "" {
		t.Fatalf("empty history event must remain archive-only: %#v", page.Messages[1])
	}
}
