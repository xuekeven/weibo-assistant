package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/xuekeven/weibo-assistant/internal/store"
)

func TestMessageExportSenderIDs(t *testing.T) {
	request := httptest.NewRequest("GET", "/api/groups/1/messages/export?weiboSenderId=123&weiboSenderId=456&weiboSenderId=123&weiboSenderId=+", nil)
	got, err := queryWeiboSenderIDs(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "123" || got[1] != "456" {
		t.Fatalf("queryWeiboSenderIDs() = %#v, want [123 456]", got)
	}
}

func TestEncodeMessageExport(t *testing.T) {
	message := store.ExportMessage{
		WeiboMessageID: "123",
		GroupName:      "测试群",
		SenderName:     "发送人",
		MessageType:    "text",
		Content:        "包含,逗号",
		SentAt:         time.Date(2026, 7, 22, 9, 30, 0, 0, time.FixedZone("CST", 8*60*60)),
	}

	csvContent, csvType, err := encodeMessageExport("csv", []store.ExportMessage{message})
	if err != nil {
		t.Fatal(err)
	}
	if csvType != "text/csv; charset=utf-8" || !strings.Contains(string(csvContent), `"包含,逗号"`) {
		t.Fatalf("unexpected CSV export: type=%q content=%q", csvType, string(csvContent))
	}

	jsonContent, _, err := encodeMessageExport("json", []store.ExportMessage{message})
	if err != nil || !strings.Contains(string(jsonContent), `"weiboMessageId": "123"`) {
		t.Fatalf("unexpected JSON export: error=%v content=%q", err, string(jsonContent))
	}

	markdownContent, markdownType, err := encodeMessageExport("md", []store.ExportMessage{message})
	if err != nil || markdownType != "text/markdown; charset=utf-8" || !strings.Contains(string(markdownContent), "**发送人**") {
		t.Fatalf("unexpected Markdown export: error=%v type=%q content=%q", err, markdownType, string(markdownContent))
	}
}

func TestEncodeMessageExportEscapesSpreadsheetFormulas(t *testing.T) {
	message := store.ExportMessage{
		WeiboMessageID: "=1+1",
		GroupName:      "+group",
		SenderName:     "@sender",
		MessageType:    "\ttext",
		Content:        "  -2+3",
		SentAt:         time.Date(2026, 7, 22, 9, 30, 0, 0, time.UTC),
	}

	content, _, err := encodeMessageExport("csv", []store.ExportMessage{message})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"'+group", "'@sender", "'\ttext", "'  -2+3", "'=1+1"} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("CSV export does not neutralize %q: %q", want, string(content))
		}
	}
}
