package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/xuekeven/weibo-assistant/internal/store"
)

func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.store.ListGroups(r.Context(), store.ListGroupsParams{
		Limit: queryInt(r, "limit", 50),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": groups,
	})
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers(r.Context(), store.ListUsersParams{
		Query: r.URL.Query().Get("q"),
		Limit: queryInt(r, "limit", 50),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": users,
	})
}

func (s *Server) handleListMessages(w http.ResponseWriter, r *http.Request) {
	before, ok := parseOptionalTime(w, r, "before")
	if !ok {
		return
	}

	messages, err := s.store.ListMessages(r.Context(), store.ListMessagesParams{
		GroupID:       queryInt64(r, "groupId", 0),
		WeiboGroupID:  r.URL.Query().Get("weiboGroupId"),
		SenderID:      queryInt64(r, "senderId", 0),
		WeiboSenderID: r.URL.Query().Get("weiboSenderId"),
		Query:         r.URL.Query().Get("q"),
		Before:        before,
		Limit:         queryInt(r, "limit", 50),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	enrichImageAttachmentsFromRaw(messages)
	stripRawIfRequested(r, messages)
	writeJSON(w, http.StatusOK, map[string]any{
		"items": messages,
	})
}

func (s *Server) handleListGroupMessages(w http.ResponseWriter, r *http.Request) {
	groupID, err := pathInt64(r, "/api/groups/", "/messages")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	before, ok := parseOptionalTime(w, r, "before")
	if !ok {
		return
	}

	messages, err := s.store.ListMessages(r.Context(), store.ListMessagesParams{
		GroupID: groupID,
		Query:   r.URL.Query().Get("q"),
		Before:  before,
		Limit:   queryInt(r, "limit", 50),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	enrichImageAttachmentsFromRaw(messages)
	stripRawIfRequested(r, messages)
	writeJSON(w, http.StatusOK, map[string]any{
		"items": messages,
	})
}

func (s *Server) handleCreateMessage(w http.ResponseWriter, r *http.Request) {
	s.createMessage(w, r, "")
}

func (s *Server) handleCreateDevMessage(w http.ResponseWriter, r *http.Request) {
	s.createMessage(w, r, "dev")
}

func (s *Server) createMessage(w http.ResponseWriter, r *http.Request, defaultSource string) {
	var params store.CreateMessageParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(params.Source) == "" && defaultSource != "" {
		params.Source = defaultSource
	}
	result, err := s.store.CreateMessage(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	status := http.StatusCreated
	if !result.Created {
		status = http.StatusOK
	}
	writeJSON(w, status, result)
}

func (s *Server) handleListNotificationRules(w http.ResponseWriter, r *http.Request) {
	enabled, ok := parseOptionalBool(w, r, "enabled")
	if !ok {
		return
	}

	rules, err := s.store.ListNotificationRules(r.Context(), store.ListNotificationRulesParams{
		Enabled: enabled,
		Limit:   queryInt(r, "limit", 50),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": rules,
	})
}

func (s *Server) handleCreateNotificationRule(w http.ResponseWriter, r *http.Request) {
	var params store.CreateNotificationRuleParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	rule, err := s.store.CreateNotificationRule(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

func (s *Server) handleUpdateNotificationRule(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "/api/notification-rules/", "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	var params store.UpdateNotificationRuleParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	params.ID = id
	rule, err := s.store.UpdateNotificationRule(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (s *Server) handleListNotificationEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.store.ListNotificationEvents(r.Context(), store.ListNotificationEventsParams{
		RuleID:    queryInt64(r, "ruleId", 0),
		MessageID: queryInt64(r, "messageId", 0),
		Status:    r.URL.Query().Get("status"),
		Limit:     queryInt(r, "limit", 50),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": events,
	})
}

func (s *Server) handleCreateNotificationEvent(w http.ResponseWriter, r *http.Request) {
	var params store.CreateNotificationEventParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	event, err := s.store.CreateNotificationEvent(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, event)
}

func queryInt(r *http.Request, key string, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func stripRawIfRequested(r *http.Request, messages []store.Message) {
	if strings.ToLower(strings.TrimSpace(r.URL.Query().Get("includeRaw"))) != "false" {
		return
	}
	for index := range messages {
		messages[index].Raw = nil
	}
}

func enrichImageAttachmentsFromRaw(messages []store.Message) {
	for index := range messages {
		if len(messages[index].Attachments) > 0 {
			continue
		}
		attachment, ok := imageAttachmentFromRaw(messages[index].Raw)
		if !ok {
			continue
		}
		if messages[index].MessageType == "text" {
			messages[index].MessageType = "image"
		}
		messages[index].Attachments = []store.Attachment{attachment}
	}
}

func imageAttachmentFromRaw(raw json.RawMessage) (store.Attachment, bool) {
	if len(raw) == 0 {
		return store.Attachment{}, false
	}
	var event struct {
		Data struct {
			Info struct {
				MediaType int           `json:"media_type"`
				FIDs      []json.Number `json:"fids"`
			} `json:"info"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return store.Attachment{}, false
	}
	if event.Data.Info.MediaType != 1 && len(event.Data.Info.FIDs) == 0 {
		return store.Attachment{}, false
	}
	meta := map[string]any{
		"media_type": event.Data.Info.MediaType,
		"url_status": "missing_in_websocket_payload",
	}
	if len(event.Data.Info.FIDs) > 0 {
		fids := make([]string, 0, len(event.Data.Info.FIDs))
		for _, fid := range event.Data.Info.FIDs {
			if fid.String() != "" {
				fids = append(fids, fid.String())
			}
		}
		meta["fids"] = fids
	}
	metaRaw, err := json.Marshal(meta)
	if err != nil {
		return store.Attachment{}, false
	}
	return store.Attachment{
		AttachmentType: "image",
		Meta:           metaRaw,
	}, true
}

func queryInt64(r *http.Request, key string, fallback int64) int64 {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func parseOptionalTime(w http.ResponseWriter, r *http.Request, key string) (*time.Time, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return nil, true
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New(key+" must be RFC3339 time"))
		return nil, false
	}
	return &parsed, true
}

func parseOptionalBool(w http.ResponseWriter, r *http.Request, key string) (*bool, bool) {
	raw := strings.ToLower(strings.TrimSpace(r.URL.Query().Get(key)))
	if raw == "" {
		return nil, true
	}
	if raw == "1" || raw == "true" || raw == "yes" || raw == "on" {
		value := true
		return &value, true
	}
	if raw == "0" || raw == "false" || raw == "no" || raw == "off" {
		value := false
		return &value, true
	}
	writeError(w, http.StatusBadRequest, errors.New(key+" must be boolean"))
	return nil, false
}

func pathInt64(r *http.Request, prefix string, suffix string) (int64, error) {
	path := r.URL.Path
	if !strings.HasPrefix(path, prefix) || (suffix != "" && !strings.HasSuffix(path, suffix)) {
		return 0, errors.New("invalid path")
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if raw == "" {
		return 0, errors.New("missing id")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}
