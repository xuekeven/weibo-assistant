package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type Group struct {
	ID            int64      `json:"id"`
	WeiboGroupID  string     `json:"weiboGroupId,omitempty"`
	Name          string     `json:"name"`
	AvatarURL     string     `json:"avatarUrl,omitempty"`
	LastMessageAt *time.Time `json:"lastMessageAt,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

type User struct {
	ID          int64           `json:"id"`
	WeiboUserID string          `json:"weiboUserId,omitempty"`
	Nickname    string          `json:"nickname"`
	AvatarURL   string          `json:"avatarUrl,omitempty"`
	ProfileURL  string          `json:"profileUrl,omitempty"`
	Raw         json.RawMessage `json:"raw,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

type Attachment struct {
	ID             int64           `json:"id,omitempty"`
	MessageID      int64           `json:"messageId,omitempty"`
	AttachmentType string          `json:"attachmentType"`
	URL            string          `json:"url,omitempty"`
	LocalPath      string          `json:"localPath,omitempty"`
	Meta           json.RawMessage `json:"meta,omitempty"`
	CreatedAt      time.Time       `json:"createdAt,omitempty"`
}

type Message struct {
	ID             int64           `json:"id"`
	WeiboMessageID string          `json:"weiboMessageId,omitempty"`
	GroupID        int64           `json:"groupId"`
	WeiboGroupID   string          `json:"weiboGroupId,omitempty"`
	GroupName      string          `json:"groupName,omitempty"`
	SenderID       *int64          `json:"senderId,omitempty"`
	WeiboSenderID  string          `json:"weiboSenderId,omitempty"`
	SenderName     string          `json:"senderName,omitempty"`
	MessageType    string          `json:"messageType"`
	Content        string          `json:"content,omitempty"`
	SentAt         *time.Time      `json:"sentAt,omitempty"`
	CapturedAt     time.Time       `json:"capturedAt"`
	Source         string          `json:"source"`
	DedupeKey      string          `json:"dedupeKey"`
	Raw            json.RawMessage `json:"raw,omitempty"`
	Attachments    []Attachment    `json:"attachments,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
}

type UpsertGroupParams struct {
	WeiboGroupID string
	Name         string
	AvatarURL    string
}

type UpsertUserParams struct {
	WeiboUserID string
	Nickname    string
	AvatarURL   string
	ProfileURL  string
	Raw         json.RawMessage
}

type CreateMessageParams struct {
	WeiboMessageID string            `json:"weiboMessageId"`
	Group          UpsertGroupParams `json:"group"`
	Sender         UpsertUserParams  `json:"sender"`
	MessageType    string            `json:"messageType"`
	Content        string            `json:"content"`
	SentAt         *time.Time        `json:"sentAt"`
	Source         string            `json:"source"`
	DedupeKey      string            `json:"dedupeKey"`
	Raw            json.RawMessage   `json:"raw"`
	Attachments    []Attachment      `json:"attachments"`
}

type CreateMessageResult struct {
	Message Message `json:"message"`
	Created bool    `json:"created"`
}

type ResolveImageAttachmentParams struct {
	FID       string
	URL       string
	SourceURL string
}

type ListGroupsParams struct {
	Limit int
}

type ListUsersParams struct {
	Query string
	Limit int
}

type ListMessagesParams struct {
	GroupID       int64
	WeiboGroupID  string
	SenderID      int64
	WeiboSenderID string
	Query         string
	Before        *time.Time
	Limit         int
}

func (s *Store) UpsertGroup(ctx context.Context, params UpsertGroupParams) (Group, error) {
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return Group{}, errors.New("group name is required")
	}
	if params.WeiboGroupID == "" {
		return s.upsertGroupByName(ctx, name, params.AvatarURL)
	}

	var group Group
	err := s.pool.QueryRow(ctx, `
		insert into weibo_groups (weibo_group_id, name, avatar_url, updated_at)
		values (nullif($1, ''), $2, nullif($3, ''), now())
		on conflict (weibo_group_id) do update set
		  name = excluded.name,
		  avatar_url = coalesce(excluded.avatar_url, weibo_groups.avatar_url),
		  updated_at = now()
		returning id,
		          coalesce(weibo_group_id, ''),
		          name,
		          coalesce(avatar_url, ''),
		          last_message_at,
		          created_at,
		          updated_at
	`, params.WeiboGroupID, name, params.AvatarURL).Scan(
		&group.ID,
		&group.WeiboGroupID,
		&group.Name,
		&group.AvatarURL,
		&group.LastMessageAt,
		&group.CreatedAt,
		&group.UpdatedAt,
	)
	return group, err
}

func (s *Store) upsertGroupByName(ctx context.Context, name string, avatarURL string) (Group, error) {
	var group Group
	err := s.pool.QueryRow(ctx, `
		with existing as (
		  select id from weibo_groups where weibo_group_id is null and name = $1 limit 1
		), updated as (
		  update weibo_groups
		     set avatar_url = coalesce(nullif($2, ''), avatar_url),
		         updated_at = now()
		   where id in (select id from existing)
		   returning id
		), inserted as (
		  insert into weibo_groups (name, avatar_url)
		  select $1, nullif($2, '')
		  where not exists (select 1 from existing)
		  returning id
		)
		select g.id,
		       coalesce(g.weibo_group_id, ''),
		       g.name,
		       coalesce(g.avatar_url, ''),
		       g.last_message_at,
		       g.created_at,
		       g.updated_at
		  from weibo_groups g
		 where g.id = coalesce((select id from updated), (select id from inserted))
	`, name, avatarURL).Scan(
		&group.ID,
		&group.WeiboGroupID,
		&group.Name,
		&group.AvatarURL,
		&group.LastMessageAt,
		&group.CreatedAt,
		&group.UpdatedAt,
	)
	return group, err
}

func (s *Store) UpsertUser(ctx context.Context, params UpsertUserParams) (User, error) {
	nickname := strings.TrimSpace(params.Nickname)
	if nickname == "" {
		return User{}, errors.New("sender nickname is required")
	}
	if params.WeiboUserID == "" {
		return s.upsertUserByNickname(ctx, nickname, params)
	}

	var user User
	err := s.pool.QueryRow(ctx, `
		insert into weibo_users (weibo_user_id, nickname, avatar_url, profile_url, raw, updated_at)
		values (nullif($1, ''), $2, nullif($3, ''), nullif($4, ''), nullif($5, 'null')::jsonb, now())
		on conflict (weibo_user_id) do update set
		  nickname = excluded.nickname,
		  avatar_url = coalesce(excluded.avatar_url, weibo_users.avatar_url),
		  profile_url = coalesce(excluded.profile_url, weibo_users.profile_url),
		  raw = coalesce(excluded.raw, weibo_users.raw),
		  updated_at = now()
		returning id,
		          coalesce(weibo_user_id, ''),
		          nickname,
		          coalesce(avatar_url, ''),
		          coalesce(profile_url, ''),
		          coalesce(raw, 'null'::jsonb),
		          created_at,
		          updated_at
	`, params.WeiboUserID, nickname, params.AvatarURL, params.ProfileURL, jsonOrNull(params.Raw)).Scan(
		&user.ID,
		&user.WeiboUserID,
		&user.Nickname,
		&user.AvatarURL,
		&user.ProfileURL,
		&user.Raw,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	return user, err
}

func (s *Store) upsertUserByNickname(ctx context.Context, nickname string, params UpsertUserParams) (User, error) {
	var user User
	err := s.pool.QueryRow(ctx, `
		with existing as (
		  select id from weibo_users where weibo_user_id is null and nickname = $1 limit 1
		), updated as (
		  update weibo_users
		     set avatar_url = coalesce(nullif($2, ''), avatar_url),
		         profile_url = coalesce(nullif($3, ''), profile_url),
		         raw = coalesce(nullif($4, 'null')::jsonb, raw),
		         updated_at = now()
		   where id in (select id from existing)
		   returning id
		), inserted as (
		  insert into weibo_users (nickname, avatar_url, profile_url, raw)
		  select $1, nullif($2, ''), nullif($3, ''), nullif($4, 'null')::jsonb
		  where not exists (select 1 from existing)
		  returning id
		)
		select u.id,
		       coalesce(u.weibo_user_id, ''),
		       u.nickname,
		       coalesce(u.avatar_url, ''),
		       coalesce(u.profile_url, ''),
		       coalesce(u.raw, 'null'::jsonb),
		       u.created_at,
		       u.updated_at
		  from weibo_users u
		 where u.id = coalesce((select id from updated), (select id from inserted))
	`, nickname, params.AvatarURL, params.ProfileURL, jsonOrNull(params.Raw)).Scan(
		&user.ID,
		&user.WeiboUserID,
		&user.Nickname,
		&user.AvatarURL,
		&user.ProfileURL,
		&user.Raw,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	return user, err
}

func (s *Store) CreateMessage(ctx context.Context, params CreateMessageParams) (CreateMessageResult, error) {
	if strings.TrimSpace(params.Source) == "" {
		return CreateMessageResult{}, errors.New("message source is required")
	}
	if strings.TrimSpace(params.MessageType) == "" {
		params.MessageType = "text"
	}

	group, err := s.UpsertGroup(ctx, params.Group)
	if err != nil {
		return CreateMessageResult{}, err
	}
	user, err := s.UpsertUser(ctx, params.Sender)
	if err != nil {
		return CreateMessageResult{}, err
	}

	dedupeKey := strings.TrimSpace(params.DedupeKey)
	if dedupeKey == "" {
		dedupeKey = BuildDedupeKey(params, group.ID, user.ID)
	}

	var message Message
	err = s.pool.QueryRow(ctx, `
		insert into weibo_messages (
		  weibo_message_id, group_id, sender_id, message_type, content,
		  sent_at, source, dedupe_key, raw
		)
		values (
		  nullif($1, ''), $2, $3, $4, nullif($5, ''),
		  $6, $7, $8, nullif($9, 'null')::jsonb
		)
		on conflict (dedupe_key) do nothing
		returning id,
		          coalesce(weibo_message_id, ''),
		          group_id,
		          sender_id,
		          message_type,
		          coalesce(content, ''),
		          sent_at,
		          captured_at,
		          source,
		          dedupe_key,
		          coalesce(raw, 'null'::jsonb),
		          created_at
	`, params.WeiboMessageID, group.ID, user.ID, params.MessageType, params.Content, params.SentAt, params.Source, dedupeKey, jsonOrNull(params.Raw)).Scan(
		&message.ID,
		&message.WeiboMessageID,
		&message.GroupID,
		&message.SenderID,
		&message.MessageType,
		&message.Content,
		&message.SentAt,
		&message.CapturedAt,
		&message.Source,
		&message.DedupeKey,
		&message.Raw,
		&message.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, err := s.GetMessageByDedupeKey(ctx, dedupeKey)
		if err == nil && len(existing.Attachments) == 0 && len(params.Attachments) > 0 {
			attachments, insertErr := s.InsertAttachments(ctx, existing.ID, params.Attachments)
			if insertErr != nil {
				return CreateMessageResult{}, insertErr
			}
			existing.Attachments = attachments
		}
		return CreateMessageResult{Message: existing, Created: false}, err
	}
	if err != nil {
		return CreateMessageResult{}, err
	}

	message.GroupName = group.Name
	message.WeiboGroupID = group.WeiboGroupID
	message.SenderName = user.Nickname
	message.WeiboSenderID = user.WeiboUserID
	if len(params.Attachments) > 0 {
		attachments, err := s.InsertAttachments(ctx, message.ID, params.Attachments)
		if err != nil {
			return CreateMessageResult{}, err
		}
		message.Attachments = attachments
	}

	if err := s.updateGroupLastMessageAt(ctx, group.ID, params.SentAt); err != nil {
		return CreateMessageResult{}, err
	}
	return CreateMessageResult{Message: message, Created: true}, nil
}

func (s *Store) InsertAttachments(ctx context.Context, messageID int64, attachments []Attachment) ([]Attachment, error) {
	result := make([]Attachment, 0, len(attachments))
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.AttachmentType) == "" {
			attachment.AttachmentType = "unknown"
		}
		var saved Attachment
		err := s.pool.QueryRow(ctx, `
			insert into weibo_message_attachments (message_id, attachment_type, url, local_path, meta)
			values ($1, $2, nullif($3, ''), nullif($4, ''), nullif($5, 'null')::jsonb)
			returning id,
			          message_id,
			          attachment_type,
			          coalesce(url, ''),
			          coalesce(local_path, ''),
			          coalesce(meta, 'null'::jsonb),
			          created_at
		`, messageID, attachment.AttachmentType, attachment.URL, attachment.LocalPath, jsonOrNull(attachment.Meta)).Scan(
			&saved.ID,
			&saved.MessageID,
			&saved.AttachmentType,
			&saved.URL,
			&saved.LocalPath,
			&saved.Meta,
			&saved.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		result = append(result, saved)
	}
	return result, nil
}

func (s *Store) ResolveImageAttachmentURL(ctx context.Context, params ResolveImageAttachmentParams) (int64, error) {
	fid := strings.TrimSpace(params.FID)
	imageURL := strings.TrimSpace(params.URL)
	if fid == "" || imageURL == "" {
		return 0, nil
	}

	metaPatch := map[string]any{
		"resolved_url": imageURL,
	}
	if strings.TrimSpace(params.SourceURL) != "" {
		metaPatch["resolved_url_source"] = strings.TrimSpace(params.SourceURL)
	}
	metaPatchRaw, err := json.Marshal(metaPatch)
	if err != nil {
		return 0, err
	}

	fidFilterRaw, err := json.Marshal(map[string]any{
		"fids": []string{fid},
	})
	if err != nil {
		return 0, err
	}

	taggedURL := map[string]any{
		"url": imageURL,
	}
	taggedURLRaw, err := json.Marshal(taggedURL)
	if err != nil {
		return 0, err
	}

	result, err := s.pool.Exec(ctx, `
		update weibo_message_attachments
		   set url = $1,
		       meta = coalesce(meta, '{}'::jsonb) || $2::jsonb
		 where attachment_type = 'image'
		   and coalesce(url, '') = ''
		   and (
		         coalesce(meta, '{}'::jsonb) @> $3::jsonb
		         or coalesce(meta, '{}'::jsonb) @> $4::jsonb
		       )
	`, imageURL, string(metaPatchRaw), string(fidFilterRaw), string(taggedURLRaw))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func (s *Store) GetMessageByDedupeKey(ctx context.Context, dedupeKey string) (Message, error) {
	messages, err := s.queryMessages(ctx, `
		where m.dedupe_key = $1
		order by m.id desc
		limit 1
	`, dedupeKey)
	if err != nil {
		return Message{}, err
	}
	if len(messages) == 0 {
		return Message{}, pgx.ErrNoRows
	}
	return messages[0], nil
}

func (s *Store) ListGroups(ctx context.Context, params ListGroupsParams) ([]Group, error) {
	limit := normalizeLimit(params.Limit, 50, 200)
	rows, err := s.pool.Query(ctx, `
		select id,
		       coalesce(weibo_group_id, ''),
		       name,
		       coalesce(avatar_url, ''),
		       last_message_at,
		       created_at,
		       updated_at
		  from weibo_groups
		 order by last_message_at desc nulls last, updated_at desc, id desc
		 limit $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	groups := make([]Group, 0)
	for rows.Next() {
		var group Group
		if err := rows.Scan(
			&group.ID,
			&group.WeiboGroupID,
			&group.Name,
			&group.AvatarURL,
			&group.LastMessageAt,
			&group.CreatedAt,
			&group.UpdatedAt,
		); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func (s *Store) ListUsers(ctx context.Context, params ListUsersParams) ([]User, error) {
	limit := normalizeLimit(params.Limit, 50, 200)
	query := strings.TrimSpace(params.Query)
	args := []any{limit}
	whereSQL := ""
	if query != "" {
		args = append([]any{"%" + query + "%"}, args...)
		whereSQL = "where nickname ilike $1 or weibo_user_id ilike $1"
	}

	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		select id,
		       coalesce(weibo_user_id, ''),
		       nickname,
		       coalesce(avatar_url, ''),
		       coalesce(profile_url, ''),
		       coalesce(raw, 'null'::jsonb),
		       created_at,
		       updated_at
		  from weibo_users
		  %s
		 order by updated_at desc, id desc
		 limit $%d
	`, whereSQL, len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]User, 0)
	for rows.Next() {
		var user User
		if err := rows.Scan(
			&user.ID,
			&user.WeiboUserID,
			&user.Nickname,
			&user.AvatarURL,
			&user.ProfileURL,
			&user.Raw,
			&user.CreatedAt,
			&user.UpdatedAt,
		); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Store) ListMessages(ctx context.Context, params ListMessagesParams) ([]Message, error) {
	limit := normalizeLimit(params.Limit, 50, 200)
	conditions := make([]string, 0, 4)
	args := make([]any, 0, 5)
	addArg := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}

	if params.GroupID > 0 {
		conditions = append(conditions, "m.group_id = "+addArg(params.GroupID))
	}
	if params.WeiboGroupID = strings.TrimSpace(params.WeiboGroupID); params.WeiboGroupID != "" {
		conditions = append(conditions, "g.weibo_group_id = "+addArg(params.WeiboGroupID))
	}
	if params.SenderID > 0 {
		conditions = append(conditions, "m.sender_id = "+addArg(params.SenderID))
	}
	if params.WeiboSenderID = strings.TrimSpace(params.WeiboSenderID); params.WeiboSenderID != "" {
		conditions = append(conditions, "u.weibo_user_id = "+addArg(params.WeiboSenderID))
	}
	if params.Query = strings.TrimSpace(params.Query); params.Query != "" {
		placeholder := addArg("%" + params.Query + "%")
		conditions = append(conditions, "(m.content ilike "+placeholder+" or m.weibo_message_id ilike "+placeholder+")")
	}
	if params.Before != nil {
		conditions = append(conditions, "coalesce(m.sent_at, m.captured_at) < "+addArg(*params.Before))
	}

	whereSQL := ""
	if len(conditions) > 0 {
		whereSQL = "where " + strings.Join(conditions, " and ")
	}
	whereSQL += fmt.Sprintf(`
		order by coalesce(m.sent_at, m.captured_at) desc, m.id desc
		limit %s
	`, addArg(limit))
	return s.queryMessages(ctx, whereSQL, args...)
}

func (s *Store) queryMessages(ctx context.Context, whereSQL string, args ...any) ([]Message, error) {
	query := fmt.Sprintf(`
		select m.id,
		       coalesce(m.weibo_message_id, ''),
		       m.group_id,
		       coalesce(g.weibo_group_id, ''),
		       g.name,
		       m.sender_id,
		       coalesce(u.weibo_user_id, ''),
		       coalesce(u.nickname, ''),
		       m.message_type,
		       coalesce(m.content, ''),
		       m.sent_at,
		       m.captured_at,
		       m.source,
		       m.dedupe_key,
		       coalesce(m.raw, 'null'::jsonb),
		       m.created_at
		  from weibo_messages m
		  join weibo_groups g on g.id = m.group_id
		  left join weibo_users u on u.id = m.sender_id
		  %s
	`, whereSQL)
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := make([]Message, 0)
	for rows.Next() {
		var message Message
		if err := rows.Scan(
			&message.ID,
			&message.WeiboMessageID,
			&message.GroupID,
			&message.WeiboGroupID,
			&message.GroupName,
			&message.SenderID,
			&message.WeiboSenderID,
			&message.SenderName,
			&message.MessageType,
			&message.Content,
			&message.SentAt,
			&message.CapturedAt,
			&message.Source,
			&message.DedupeKey,
			&message.Raw,
			&message.CreatedAt,
		); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return messages, nil
	}

	attachments, err := s.attachmentsByMessageID(ctx, messageIDs(messages))
	if err != nil {
		return nil, err
	}
	for index := range messages {
		messages[index].Attachments = attachments[messages[index].ID]
	}
	return messages, nil
}

func (s *Store) attachmentsByMessageID(ctx context.Context, ids []int64) (map[int64][]Attachment, error) {
	result := make(map[int64][]Attachment)
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := s.pool.Query(ctx, `
		select id,
		       message_id,
		       attachment_type,
		       coalesce(url, ''),
		       coalesce(local_path, ''),
		       coalesce(meta, 'null'::jsonb),
		       created_at
		  from weibo_message_attachments
		 where message_id = any($1)
		 order by id asc
	`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var attachment Attachment
		if err := rows.Scan(
			&attachment.ID,
			&attachment.MessageID,
			&attachment.AttachmentType,
			&attachment.URL,
			&attachment.LocalPath,
			&attachment.Meta,
			&attachment.CreatedAt,
		); err != nil {
			return nil, err
		}
		result[attachment.MessageID] = append(result[attachment.MessageID], attachment)
	}
	return result, rows.Err()
}

func (s *Store) updateGroupLastMessageAt(ctx context.Context, groupID int64, sentAt *time.Time) error {
	_, err := s.pool.Exec(ctx, `
		update weibo_groups
		   set last_message_at = greatest(
		         coalesce(last_message_at, '-infinity'::timestamptz),
		         coalesce($2::timestamptz, now())
		       ),
		       updated_at = now()
		 where id = $1
	`, groupID, sentAt)
	return err
}

func BuildDedupeKey(params CreateMessageParams, groupID int64, senderID int64) string {
	if strings.TrimSpace(params.WeiboMessageID) != "" {
		return "weibo_message:" + strings.TrimSpace(params.WeiboMessageID)
	}
	sentAt := ""
	if params.SentAt != nil {
		sentAt = params.SentAt.UTC().Format(time.RFC3339Nano)
	}
	raw := fmt.Sprintf("%d|%d|%s|%s|%s", groupID, senderID, params.MessageType, strings.TrimSpace(params.Content), sentAt)
	sum := sha256.Sum256([]byte(raw))
	return "hash:" + hex.EncodeToString(sum[:])
}

func jsonOrNull(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "null"
	}
	return string(raw)
}

func normalizeLimit(limit int, fallback int, max int) int {
	if limit <= 0 {
		return fallback
	}
	if limit > max {
		return max
	}
	return limit
}

func messageIDs(messages []Message) []int64 {
	ids := make([]int64, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ID)
	}
	return ids
}
