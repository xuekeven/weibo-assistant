package httpapi

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	if !yesterdayStart.Before(todayStart) || !todayStart.Before(tomorrowStart) {
		writeError(w, http.StatusBadRequest, errors.New("daily stats boundaries must be strictly increasing"))
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
	weiboSenderIDs, err := queryWeiboSenderIDs(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	messages, err := s.store.ListGroupMessagesForExport(r.Context(), groupID, since, before, weiboSenderIDs)
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
	if _, err := w.Write(content); err != nil {
		slog.Warn("write message export failed", "groupID", groupID, "format", format, "error", err)
	}
}

func queryWeiboSenderIDs(r *http.Request) ([]string, error) {
	const maxSelectedSenders = 200
	values := r.URL.Query()["weiboSenderId"]
	senderIDs := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		senderID := strings.TrimSpace(value)
		if senderID == "" {
			continue
		}
		if _, ok := seen[senderID]; ok {
			continue
		}
		seen[senderID] = struct{}{}
		senderIDs = append(senderIDs, senderID)
		if len(senderIDs) > maxSelectedSenders {
			return nil, errors.New("at most 200 weiboSenderId values are allowed")
		}
	}
	return senderIDs, nil
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
				message.SentAt.Format(time.RFC3339), safeCSVCell(message.GroupName), safeCSVCell(message.SenderName),
				safeCSVCell(message.WeiboSenderID), safeCSVCell(message.MessageType), safeCSVCell(message.Content), safeCSVCell(message.WeiboMessageID),
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

// Spreadsheet programs may interpret untrusted cells beginning with these
// characters as formulas. Prefixing an apostrophe preserves the visible text
// while preventing exported chat content from executing as a formula.
func safeCSVCell(value string) string {
	if value != "" && (value[0] == '\t' || value[0] == '\r' || value[0] == '\n') {
		return "'" + value
	}
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed == "" {
		return value
	}
	switch trimmed[0] {
	case '=', '+', '-', '@':
		return "'" + value
	default:
		return value
	}
}

func fallbackExportText(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
