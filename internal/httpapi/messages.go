package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/xuekeven/weibo-assistant/internal/store"
)

func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	limit, ok := queryIntRange(w, r, "limit", 50, 1, 200)
	if !ok {
		return
	}
	groups, err := s.store.ListGroups(r.Context(), store.ListGroupsParams{
		Limit: limit,
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
	limit, ok := queryIntRange(w, r, "limit", 50, 1, 200)
	if !ok {
		return
	}
	users, err := s.store.ListUsers(r.Context(), store.ListUsersParams{
		Query: r.URL.Query().Get("q"),
		Limit: limit,
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
	since, ok := parseOptionalTime(w, r, "since")
	if !ok {
		return
	}
	before, ok := parseOptionalTime(w, r, "before")
	if !ok {
		return
	}
	groupID, ok := queryInt64Range(w, r, "groupId", 0, 1)
	if !ok {
		return
	}
	senderID, ok := queryInt64Range(w, r, "senderId", 0, 1)
	if !ok {
		return
	}
	limit, ok := queryIntRange(w, r, "limit", 50, 1, 200)
	if !ok {
		return
	}
	includeRaw, ok := parseBoolWithDefault(w, r, "includeRaw", true)
	if !ok {
		return
	}
	weiboSenderIDs, err := queryWeiboSenderIDs(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	messages, err := s.store.ListMessages(r.Context(), store.ListMessagesParams{
		GroupID:        groupID,
		WeiboGroupID:   r.URL.Query().Get("weiboGroupId"),
		SenderID:       senderID,
		WeiboSenderIDs: weiboSenderIDs,
		MessageType:    r.URL.Query().Get("messageType"),
		Query:          r.URL.Query().Get("q"),
		Since:          since,
		Before:         before,
		Limit:          limit,
		IncludeRaw:     includeRaw,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if includeRaw {
		enrichImageAttachmentsFromRaw(messages)
	}
	stripRaw(messages, includeRaw)
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
	limit, ok := queryIntRange(w, r, "limit", 50, 1, 200)
	if !ok {
		return
	}
	includeRaw, ok := parseBoolWithDefault(w, r, "includeRaw", true)
	if !ok {
		return
	}

	messages, err := s.store.ListMessages(r.Context(), store.ListMessagesParams{
		GroupID:    groupID,
		Query:      r.URL.Query().Get("q"),
		Before:     before,
		Limit:      limit,
		IncludeRaw: includeRaw,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if includeRaw {
		enrichImageAttachmentsFromRaw(messages)
	}
	stripRaw(messages, includeRaw)
	writeJSON(w, http.StatusOK, map[string]any{
		"items": messages,
	})
}

func (s *Server) handleGetMessage(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "/api/messages/", "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	includeRaw, ok := parseBoolWithDefault(w, r, "includeRaw", false)
	if !ok {
		return
	}
	message, err := s.store.GetMessageByID(r.Context(), id, includeRaw)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, message)
}

func (s *Server) handleResolveMessageMedia(w http.ResponseWriter, r *http.Request) {
	s.handleQueueMessageMedia(w, r, "/resolve-media", false)
}

func (s *Server) handleRetryMessageMedia(w http.ResponseWriter, r *http.Request) {
	s.handleQueueMessageMedia(w, r, "/retry-media", true)
}

func (s *Server) handleQueueMessageMedia(w http.ResponseWriter, r *http.Request, suffix string, retryFailed bool) {
	id, err := pathInt64(r, "/api/messages/", suffix)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if _, err := s.store.GetMessageByID(r.Context(), id, false); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	var queued int64
	if retryFailed {
		queued, err = s.store.RetryMediaResolutionForMessage(r.Context(), id)
	} else {
		queued, err = s.store.EnqueueMediaResolutionForMessage(r.Context(), id)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	status := http.StatusAccepted
	if queued == 0 {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{
		"messageId": id,
		"queued":    queued,
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
	if err := decodeJSON(w, r, &params); err != nil {
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
	limit, ok := queryIntRange(w, r, "limit", 50, 1, 200)
	if !ok {
		return
	}

	rules, err := s.store.ListNotificationRules(r.Context(), store.ListNotificationRulesParams{
		Enabled: enabled,
		Limit:   limit,
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
	if err := decodeJSON(w, r, &params); err != nil {
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
	if err := decodeJSON(w, r, &params); err != nil {
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

func (s *Server) handleDeleteNotificationRule(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "/api/notification-rules/", "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.DeleteNotificationRule(r.Context(), id); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListNotificationEvents(w http.ResponseWriter, r *http.Request) {
	ruleID, ok := queryInt64Range(w, r, "ruleId", 0, 1)
	if !ok {
		return
	}
	messageID, ok := queryInt64Range(w, r, "messageId", 0, 1)
	if !ok {
		return
	}
	limit, ok := queryIntRange(w, r, "limit", 50, 1, 200)
	if !ok {
		return
	}
	events, err := s.store.ListNotificationEvents(r.Context(), store.ListNotificationEventsParams{
		RuleID:    ruleID,
		MessageID: messageID,
		Status:    r.URL.Query().Get("status"),
		Limit:     limit,
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
	if err := decodeJSON(w, r, &params); err != nil {
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

func (s *Server) handleListNotificationOutbox(w http.ResponseWriter, r *http.Request) {
	limit, ok := queryIntRange(w, r, "limit", 50, 1, 200)
	if !ok {
		return
	}
	items, err := s.store.ListNotificationOutbox(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleRetryNotificationOutbox(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "/api/notification-outbox/", "/retry")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.RetryNotificationOutbox(r.Context(), id); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "retry"})
}

func queryIntRange(w http.ResponseWriter, r *http.Request, key string, fallback int, min int, max int) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < min || value > max {
		writeError(w, http.StatusBadRequest, fmt.Errorf("%s must be between %d and %d", key, min, max))
		return 0, false
	}
	return value, true
}

func stripRaw(messages []store.Message, includeRaw bool) {
	if includeRaw {
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
		"url_status": "missing_in_message_payload",
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

func queryInt64Range(w http.ResponseWriter, r *http.Request, key string, fallback int64, min int64) (int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < min {
		writeError(w, http.StatusBadRequest, fmt.Errorf("%s must be an integer greater than or equal to %d", key, min))
		return 0, false
	}
	return value, true
}

func parseBoolWithDefault(w http.ResponseWriter, r *http.Request, key string, fallback bool) (bool, bool) {
	value, ok := parseOptionalBool(w, r, key)
	if !ok {
		return false, false
	}
	if value == nil {
		return fallback, true
	}
	return *value, true
}

const maxJSONBodyBytes = 1 << 20

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain a single JSON value")
		}
		return err
	}
	return nil
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
