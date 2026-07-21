package httpapi

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/xuekeven/weibo-assistant/internal/store"
)

func (s *Server) handleGetGroupMessageStats(w http.ResponseWriter, r *http.Request) {
	groupID, err := pathInt64(r, "/api/groups/", "/message-stats")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	yesterdayStart, ok := requiredQueryTime(w, r, "yesterdayStart")
	if !ok {
		return
	}
	todayStart, ok := requiredQueryTime(w, r, "todayStart")
	if !ok {
		return
	}
	tomorrowStart, ok := requiredQueryTime(w, r, "tomorrowStart")
	if !ok {
		return
	}
	stats, err := s.store.GetGroupDailyMessageStats(r.Context(), groupID, yesterdayStart, todayStart, tomorrowStart)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleExportGroupMessages(w http.ResponseWriter, r *http.Request) {
	groupID, err := pathInt64(r, "/api/groups/", "/messages/export")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	since, ok := requiredQueryTime(w, r, "since")
	if !ok {
		return
	}
	before, ok := requiredQueryTime(w, r, "before")
	if !ok {
		return
	}
	if !since.Before(before) {
		writeError(w, http.StatusBadRequest, errors.New("since must be earlier than before"))
		return
	}
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format != "csv" && format != "json" && format != "md" {
		writeError(w, http.StatusBadRequest, errors.New("format must be csv, json, or md"))
		return
	}
	messages, err := s.store.ListGroupMessagesForExport(r.Context(), groupID, since, before)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	content, contentType, err := encodeMessageExport(format, messages)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	lastDay := before.Add(-time.Nanosecond).Format("2006-01-02")
	filename := fmt.Sprintf("weibo-messages-%s", since.Format("2006-01-02"))
	if lastDay != since.Format("2006-01-02") {
		filename += "-to-" + lastDay
	}
	filename += "." + format
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func requiredQueryTime(w http.ResponseWriter, r *http.Request, key string) (time.Time, bool) {
	value, ok := parseOptionalTime(w, r, key)
	if !ok {
		return time.Time{}, false
	}
	if value == nil {
		writeError(w, http.StatusBadRequest, errors.New(key+" is required"))
		return time.Time{}, false
	}
	return *value, true
}

func encodeMessageExport(format string, messages []store.ExportMessage) ([]byte, string, error) {
	switch format {
	case "json":
		content, err := json.MarshalIndent(messages, "", "  ")
		return content, "application/json; charset=utf-8", err
	case "csv":
		var buffer bytes.Buffer
		buffer.WriteString("\xEF\xBB\xBF")
		writer := csv.NewWriter(&buffer)
		if err := writer.Write([]string{"时间", "群聊", "发送人", "微博用户ID", "消息类型", "内容", "消息ID"}); err != nil {
			return nil, "", err
		}
		for _, message := range messages {
			if err := writer.Write([]string{
				message.SentAt.Format(time.RFC3339), message.GroupName, message.SenderName,
				message.WeiboSenderID, message.MessageType, message.Content, message.WeiboMessageID,
			}); err != nil {
				return nil, "", err
			}
		}
		writer.Flush()
		return buffer.Bytes(), "text/csv; charset=utf-8", writer.Error()
	default:
		var buffer strings.Builder
		for _, message := range messages {
			fmt.Fprintf(&buffer, "- **%s** %s\n  %s\n", fallbackExportText(message.SenderName, "未知发送人"), message.SentAt.Format(time.RFC3339), fallbackExportText(message.Content, "["+message.MessageType+"]"))
		}
		return []byte(buffer.String()), "text/markdown; charset=utf-8", nil
	}
}

func fallbackExportText(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
