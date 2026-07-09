package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type NotificationRule struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	GroupID        *int64    `json:"groupId,omitempty"`
	SenderID       *int64    `json:"senderId,omitempty"`
	SenderNickname string    `json:"senderNickname,omitempty"`
	MessageType    string    `json:"messageType,omitempty"`
	Enabled        bool      `json:"enabled"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type CreateNotificationRuleParams struct {
	Name           string `json:"name"`
	GroupID        *int64 `json:"groupId"`
	SenderID       *int64 `json:"senderId"`
	SenderNickname string `json:"senderNickname"`
	MessageType    string `json:"messageType"`
	Enabled        *bool  `json:"enabled"`
}

type UpdateNotificationRuleParams struct {
	ID             int64  `json:"-"`
	Name           string `json:"name"`
	GroupID        *int64 `json:"groupId"`
	SenderID       *int64 `json:"senderId"`
	SenderNickname string `json:"senderNickname"`
	MessageType    string `json:"messageType"`
	Enabled        *bool  `json:"enabled"`
}

type ListNotificationRulesParams struct {
	Enabled *bool
	Limit   int
}

type NotificationEvent struct {
	ID              int64           `json:"id"`
	RuleID          *int64          `json:"ruleId,omitempty"`
	MessageID       int64           `json:"messageId"`
	Status          string          `json:"status"`
	RelayURL        string          `json:"relayUrl,omitempty"`
	ToUser          string          `json:"touser,omitempty"`
	RequestPayload  json.RawMessage `json:"requestPayload,omitempty"`
	ResponsePayload json.RawMessage `json:"responsePayload,omitempty"`
	ErrorMessage    string          `json:"errorMessage,omitempty"`
	CreatedAt       time.Time       `json:"createdAt"`
}

type CreateNotificationEventParams struct {
	RuleID          *int64          `json:"ruleId"`
	MessageID       int64           `json:"messageId"`
	Status          string          `json:"status"`
	RelayURL        string          `json:"relayUrl"`
	ToUser          string          `json:"touser"`
	RequestPayload  json.RawMessage `json:"requestPayload"`
	ResponsePayload json.RawMessage `json:"responsePayload"`
	ErrorMessage    string          `json:"errorMessage"`
}

type ListNotificationEventsParams struct {
	RuleID    int64
	MessageID int64
	Status    string
	Limit     int
}

func (s *Store) CreateNotificationRule(ctx context.Context, params CreateNotificationRuleParams) (NotificationRule, error) {
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return NotificationRule{}, errors.New("rule name is required")
	}
	enabled := true
	if params.Enabled != nil {
		enabled = *params.Enabled
	}

	var rule NotificationRule
	err := s.pool.QueryRow(ctx, `
		insert into notification_rules (
		  name, group_id, sender_id, sender_nickname, message_type, enabled
		)
		values ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6)
		returning id,
		          name,
		          group_id,
		          sender_id,
		          coalesce(sender_nickname, ''),
		          coalesce(message_type, ''),
		          enabled,
		          created_at,
		          updated_at
	`, name, params.GroupID, params.SenderID, strings.TrimSpace(params.SenderNickname), strings.TrimSpace(params.MessageType), enabled).Scan(
		&rule.ID,
		&rule.Name,
		&rule.GroupID,
		&rule.SenderID,
		&rule.SenderNickname,
		&rule.MessageType,
		&rule.Enabled,
		&rule.CreatedAt,
		&rule.UpdatedAt,
	)
	return rule, err
}

func (s *Store) UpdateNotificationRule(ctx context.Context, params UpdateNotificationRuleParams) (NotificationRule, error) {
	if params.ID <= 0 {
		return NotificationRule{}, errors.New("rule id is required")
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return NotificationRule{}, errors.New("rule name is required")
	}
	enabled := true
	if params.Enabled != nil {
		enabled = *params.Enabled
	}

	var rule NotificationRule
	err := s.pool.QueryRow(ctx, `
		update notification_rules
		   set name = $2,
		       group_id = $3,
		       sender_id = $4,
		       sender_nickname = nullif($5, ''),
		       message_type = nullif($6, ''),
		       enabled = $7,
		       updated_at = now()
		 where id = $1
		returning id,
		          name,
		          group_id,
		          sender_id,
		          coalesce(sender_nickname, ''),
		          coalesce(message_type, ''),
		          enabled,
		          created_at,
		          updated_at
	`, params.ID, name, params.GroupID, params.SenderID, strings.TrimSpace(params.SenderNickname), strings.TrimSpace(params.MessageType), enabled).Scan(
		&rule.ID,
		&rule.Name,
		&rule.GroupID,
		&rule.SenderID,
		&rule.SenderNickname,
		&rule.MessageType,
		&rule.Enabled,
		&rule.CreatedAt,
		&rule.UpdatedAt,
	)
	return rule, err
}

func (s *Store) ListNotificationRules(ctx context.Context, params ListNotificationRulesParams) ([]NotificationRule, error) {
	limit := normalizeLimit(params.Limit, 50, 200)
	args := []any{limit}
	whereSQL := ""
	if params.Enabled != nil {
		args = append([]any{*params.Enabled}, args...)
		whereSQL = "where enabled = $1"
	}

	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		select id,
		       name,
		       group_id,
		       sender_id,
		       coalesce(sender_nickname, ''),
		       coalesce(message_type, ''),
		       enabled,
		       created_at,
		       updated_at
		  from notification_rules
		  %s
		 order by updated_at desc, id desc
		 limit $%d
	`, whereSQL, len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rules := make([]NotificationRule, 0)
	for rows.Next() {
		var rule NotificationRule
		if err := rows.Scan(
			&rule.ID,
			&rule.Name,
			&rule.GroupID,
			&rule.SenderID,
			&rule.SenderNickname,
			&rule.MessageType,
			&rule.Enabled,
			&rule.CreatedAt,
			&rule.UpdatedAt,
		); err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

func (s *Store) CreateNotificationEvent(ctx context.Context, params CreateNotificationEventParams) (NotificationEvent, error) {
	if params.MessageID <= 0 {
		return NotificationEvent{}, errors.New("message id is required")
	}
	if strings.TrimSpace(params.Status) == "" {
		return NotificationEvent{}, errors.New("event status is required")
	}

	var event NotificationEvent
	err := s.pool.QueryRow(ctx, `
		insert into notification_events (
		  rule_id, message_id, status, relay_url, touser,
		  request_payload, response_payload, error_message
		)
		values (
		  $1, $2, $3, nullif($4, ''), nullif($5, ''),
		  nullif($6, 'null')::jsonb, nullif($7, 'null')::jsonb, nullif($8, '')
		)
		returning id,
		          rule_id,
		          message_id,
		          status,
		          coalesce(relay_url, ''),
		          coalesce(touser, ''),
		          coalesce(request_payload, 'null'::jsonb),
		          coalesce(response_payload, 'null'::jsonb),
		          coalesce(error_message, ''),
		          created_at
	`, params.RuleID, params.MessageID, strings.TrimSpace(params.Status), params.RelayURL, params.ToUser, jsonOrNull(params.RequestPayload), jsonOrNull(params.ResponsePayload), params.ErrorMessage).Scan(
		&event.ID,
		&event.RuleID,
		&event.MessageID,
		&event.Status,
		&event.RelayURL,
		&event.ToUser,
		&event.RequestPayload,
		&event.ResponsePayload,
		&event.ErrorMessage,
		&event.CreatedAt,
	)
	return event, err
}

func (s *Store) ListNotificationEvents(ctx context.Context, params ListNotificationEventsParams) ([]NotificationEvent, error) {
	limit := normalizeLimit(params.Limit, 50, 200)
	conditions := make([]string, 0, 3)
	args := make([]any, 0, 4)
	addArg := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}

	if params.RuleID > 0 {
		conditions = append(conditions, "rule_id = "+addArg(params.RuleID))
	}
	if params.MessageID > 0 {
		conditions = append(conditions, "message_id = "+addArg(params.MessageID))
	}
	if params.Status = strings.TrimSpace(params.Status); params.Status != "" {
		conditions = append(conditions, "status = "+addArg(params.Status))
	}

	whereSQL := ""
	if len(conditions) > 0 {
		whereSQL = "where " + strings.Join(conditions, " and ")
	}
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		select id,
		       rule_id,
		       message_id,
		       status,
		       coalesce(relay_url, ''),
		       coalesce(touser, ''),
		       coalesce(request_payload, 'null'::jsonb),
		       coalesce(response_payload, 'null'::jsonb),
		       coalesce(error_message, ''),
		       created_at
		  from notification_events
		  %s
		 order by id desc
		 limit %s
	`, whereSQL, addArg(limit)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]NotificationEvent, 0)
	for rows.Next() {
		var event NotificationEvent
		if err := rows.Scan(
			&event.ID,
			&event.RuleID,
			&event.MessageID,
			&event.Status,
			&event.RelayURL,
			&event.ToUser,
			&event.RequestPayload,
			&event.ResponsePayload,
			&event.ErrorMessage,
			&event.CreatedAt,
		); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
