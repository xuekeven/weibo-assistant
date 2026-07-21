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
	ID                int64      `json:"id"`
	Name              string     `json:"name"`
	GroupID           *int64     `json:"groupId,omitempty"`
	WeiboGroupID      string     `json:"weiboGroupId,omitempty"`
	GroupName         string     `json:"groupName,omitempty"`
	SenderID          *int64     `json:"senderId,omitempty"`
	WeiboSenderID     string     `json:"weiboSenderId,omitempty"`
	SenderNickname    string     `json:"senderNickname,omitempty"`
	MessageType       string     `json:"messageType,omitempty"`
	Enabled           bool       `json:"enabled"`
	HitCount          int64      `json:"hitCount"`
	FinalFailureCount int64      `json:"finalFailureCount"`
	LastMatchedAt     *time.Time `json:"lastMatchedAt,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

type CreateNotificationRuleParams struct {
	Name           string `json:"name"`
	GroupID        *int64 `json:"groupId"`
	WeiboGroupID   string `json:"weiboGroupId"`
	GroupName      string `json:"groupName"`
	SenderID       *int64 `json:"senderId"`
	WeiboSenderID  string `json:"weiboSenderId"`
	SenderNickname string `json:"senderNickname"`
	MessageType    string `json:"messageType"`
	Enabled        *bool  `json:"enabled"`
}

type UpdateNotificationRuleParams struct {
	ID             int64  `json:"-"`
	Name           string `json:"name"`
	GroupID        *int64 `json:"groupId"`
	WeiboGroupID   string `json:"weiboGroupId"`
	GroupName      string `json:"groupName"`
	SenderID       *int64 `json:"senderId"`
	WeiboSenderID  string `json:"weiboSenderId"`
	SenderNickname string `json:"senderNickname"`
	MessageType    string `json:"messageType"`
	Enabled        *bool  `json:"enabled"`
}

type ListNotificationRulesParams struct {
	Enabled *bool
	Limit   int
}

type EnvironmentNotificationRuleParams struct {
	WeiboGroupID  string
	GroupName     string
	WeiboSenderID string
	SenderName    string
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
	if !hasNotificationRuleCondition(params.GroupID, params.WeiboGroupID, params.GroupName, params.SenderID, params.WeiboSenderID, params.SenderNickname, params.MessageType) {
		return NotificationRule{}, errors.New("notification rule requires at least one matching condition")
	}
	enabled := true
	if params.Enabled != nil {
		enabled = *params.Enabled
	}

	var rule NotificationRule
	err := s.pool.QueryRow(ctx, `
			insert into notification_rules (
			  name, group_id, weibo_group_id, group_name, sender_id, weibo_sender_id,
			  sender_nickname, message_type, enabled
			)
			values ($1, $2, nullif($3, ''), nullif($4, ''), $5, nullif($6, ''), nullif($7, ''), nullif($8, ''), $9)
			returning id,
			          name,
			          group_id,
			          coalesce(weibo_group_id, ''),
			          coalesce(group_name, ''),
			          sender_id,
			          coalesce(weibo_sender_id, ''),
			          coalesce(sender_nickname, ''),
		          coalesce(message_type, ''),
		          enabled,
		          created_at,
		          updated_at
		`, name, params.GroupID, strings.TrimSpace(params.WeiboGroupID), strings.TrimSpace(params.GroupName), params.SenderID, strings.TrimSpace(params.WeiboSenderID), strings.TrimSpace(params.SenderNickname), strings.TrimSpace(params.MessageType), enabled).Scan(
		&rule.ID,
		&rule.Name,
		&rule.GroupID,
		&rule.WeiboGroupID,
		&rule.GroupName,
		&rule.SenderID,
		&rule.WeiboSenderID,
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
	if !hasNotificationRuleCondition(params.GroupID, params.WeiboGroupID, params.GroupName, params.SenderID, params.WeiboSenderID, params.SenderNickname, params.MessageType) {
		return NotificationRule{}, errors.New("notification rule requires at least one matching condition")
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
		       weibo_group_id = nullif($4, ''),
		       group_name = nullif($5, ''),
		       sender_id = $6,
		       weibo_sender_id = nullif($7, ''),
		       sender_nickname = nullif($8, ''),
		       message_type = nullif($9, ''),
		       enabled = $10,
		       updated_at = now()
		 where id = $1
		returning id,
		          name,
		          group_id,
		          coalesce(weibo_group_id, ''),
		          coalesce(group_name, ''),
		          sender_id,
		          coalesce(weibo_sender_id, ''),
		          coalesce(sender_nickname, ''),
		          coalesce(message_type, ''),
		          enabled,
		          created_at,
		          updated_at
	`, params.ID, name, params.GroupID, strings.TrimSpace(params.WeiboGroupID), strings.TrimSpace(params.GroupName), params.SenderID, strings.TrimSpace(params.WeiboSenderID), strings.TrimSpace(params.SenderNickname), strings.TrimSpace(params.MessageType), enabled).Scan(
		&rule.ID,
		&rule.Name,
		&rule.GroupID,
		&rule.WeiboGroupID,
		&rule.GroupName,
		&rule.SenderID,
		&rule.WeiboSenderID,
		&rule.SenderNickname,
		&rule.MessageType,
		&rule.Enabled,
		&rule.CreatedAt,
		&rule.UpdatedAt,
	)
	return rule, err
}

func hasNotificationRuleCondition(groupID *int64, weiboGroupID string, groupName string, senderID *int64, weiboSenderID string, senderNickname string, messageType string) bool {
	return groupID != nil ||
		strings.TrimSpace(weiboGroupID) != "" ||
		strings.TrimSpace(groupName) != "" ||
		senderID != nil ||
		strings.TrimSpace(weiboSenderID) != "" ||
		strings.TrimSpace(senderNickname) != "" ||
		strings.TrimSpace(messageType) != ""
}

func (s *Store) ListNotificationRules(ctx context.Context, params ListNotificationRulesParams) ([]NotificationRule, error) {
	limit := normalizeLimit(params.Limit, 50, 200)
	args := []any{limit}
	whereSQL := "where r.archived_at is null"
	if params.Enabled != nil {
		args = append([]any{*params.Enabled}, args...)
		whereSQL += " and r.enabled = $1"
	}

	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		select r.id,
		       r.name,
		       r.group_id,
		       coalesce(r.weibo_group_id, ''),
		       coalesce(r.group_name, ''),
		       r.sender_id,
		       coalesce(r.weibo_sender_id, ''),
		       coalesce(r.sender_nickname, ''),
		       coalesce(r.message_type, ''),
		       r.enabled,
		       (select count(distinct e.message_id) from notification_events e where e.rule_id = r.id),
		       (select count(*) from notification_events e where e.rule_id = r.id and e.status = 'failed'),
		       (select max(e.created_at) from notification_events e where e.rule_id = r.id),
		       r.created_at,
		       r.updated_at
		  from notification_rules r
		  %s
		 order by r.updated_at desc, r.id desc
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
			&rule.WeiboGroupID,
			&rule.GroupName,
			&rule.SenderID,
			&rule.WeiboSenderID,
			&rule.SenderNickname,
			&rule.MessageType,
			&rule.Enabled,
			&rule.HitCount,
			&rule.FinalFailureCount,
			&rule.LastMatchedAt,
			&rule.CreatedAt,
			&rule.UpdatedAt,
		); err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

func (s *Store) DeleteNotificationRule(ctx context.Context, id int64) error {
	commandTag, err := s.pool.Exec(ctx, `
		update notification_rules
		   set enabled = false, archived_at = now(), updated_at = now()
		 where id = $1 and name <> '环境变量默认通知规则' and archived_at is null
	`, id)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() != 1 {
		return errors.New("notification rule not found or is managed by environment variables")
	}
	return nil
}

func (s *Store) SyncEnvironmentNotificationRule(ctx context.Context, params EnvironmentNotificationRuleParams) (NotificationRule, error) {
	const ruleName = "环境变量默认通知规则"
	weiboGroupID := strings.TrimSpace(params.WeiboGroupID)
	groupName := ""
	if weiboGroupID == "" {
		groupName = strings.TrimSpace(params.GroupName)
	}
	weiboSenderID := strings.TrimSpace(params.WeiboSenderID)
	senderName := ""
	if weiboSenderID == "" {
		senderName = strings.TrimSpace(params.SenderName)
	}
	enabled := weiboSenderID != "" || senderName != ""

	var rule NotificationRule
	err := s.pool.QueryRow(ctx, `
		insert into notification_rules (
		  name, weibo_group_id, group_name, weibo_sender_id, sender_nickname, enabled
		)
		values ($1, nullif($2, ''), nullif($3, ''), nullif($4, ''), nullif($5, ''), $6)
		on conflict (name) where name = '环境变量默认通知规则' do update set
		  weibo_group_id = excluded.weibo_group_id,
		  group_name = excluded.group_name,
		  weibo_sender_id = excluded.weibo_sender_id,
		  sender_nickname = excluded.sender_nickname,
		  enabled = excluded.enabled,
		  updated_at = now()
		returning id,
		          name,
		          group_id,
		          coalesce(weibo_group_id, ''),
		          coalesce(group_name, ''),
		          sender_id,
		          coalesce(weibo_sender_id, ''),
		          coalesce(sender_nickname, ''),
		          coalesce(message_type, ''),
		          enabled,
		          created_at,
		          updated_at
	`, ruleName, weiboGroupID, groupName, weiboSenderID, senderName, enabled).Scan(
		&rule.ID,
		&rule.Name,
		&rule.GroupID,
		&rule.WeiboGroupID,
		&rule.GroupName,
		&rule.SenderID,
		&rule.WeiboSenderID,
		&rule.SenderNickname,
		&rule.MessageType,
		&rule.Enabled,
		&rule.CreatedAt,
		&rule.UpdatedAt,
	)
	return rule, err
}

func matchingNotificationRuleIDs(ctx context.Context, db messageDB, group Group, user User, messageType string) ([]int64, error) {
	rows, err := db.Query(ctx, `
		select id
		  from notification_rules
		 where enabled = true
		   and (group_id is null or group_id = $1)
		   and (weibo_group_id is null or weibo_group_id = nullif($2, ''))
		   and (group_name is null or group_name = $3)
		   and (sender_id is null or sender_id = $4)
		   and (weibo_sender_id is null or weibo_sender_id = nullif($5, ''))
		   and (sender_nickname is null or sender_nickname = $6)
		   and (message_type is null or message_type = $7)
		   and (
		         $7 not in ('system', 'event')
		         or (sender_id is null and weibo_sender_id is null and sender_nickname is null)
		       )
		 order by id asc
	`, group.ID, group.WeiboGroupID, group.Name, user.ID, user.WeiboUserID, user.Nickname, strings.TrimSpace(messageType))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
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
	if err != nil {
		return event, err
	}
	return event, s.NotifyRealtimeEvent(ctx, RealtimeEvent{Type: "notification_event_created", NotificationEventID: event.ID})
}

func (s *Store) GetNotificationEventByID(ctx context.Context, id int64) (NotificationEvent, error) {
	var event NotificationEvent
	err := s.pool.QueryRow(ctx, `
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
		 where id = $1
	`, id).Scan(
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
