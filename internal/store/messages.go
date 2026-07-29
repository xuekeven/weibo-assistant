package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	neturl "net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type messageDB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type Group struct {
	ID                 int64      `json:"id"`
	WeiboGroupID       string     `json:"weiboGroupId,omitempty"`
	Name               string     `json:"name"`
	AvatarURL          string     `json:"avatarUrl,omitempty"`
	LastMessageAt      *time.Time `json:"lastMessageAt,omitempty"`
	LastMessagePreview string     `json:"lastMessagePreview,omitempty"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
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
	SenderAvatar   string          `json:"senderAvatar,omitempty"`
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
	WeiboMessageID string                     `json:"weiboMessageId"`
	Group          UpsertGroupParams          `json:"group"`
	Sender         UpsertUserParams           `json:"sender"`
	MessageType    string                     `json:"messageType"`
	Content        string                     `json:"content"`
	SentAt         *time.Time                 `json:"sentAt"`
	Source         string                     `json:"source"`
	DedupeKey      string                     `json:"dedupeKey"`
	Raw            json.RawMessage            `json:"raw"`
	Attachments    []Attachment               `json:"attachments"`
	Notification   *EnqueueNotificationParams `json:"-"`
}

type CreateMessageResult struct {
	Message             Message `json:"message"`
	Created             bool    `json:"created"`
	NotificationsQueued int     `json:"notificationsQueued"`
}

type ResolveImageAttachmentParams struct {
	FID       string
	URL       string
	SourceURL string
	Force     bool
}

type ListGroupsParams struct {
	Limit int
}

type ListUsersParams struct {
	Query string
	Limit int
}

type ListMessagesParams struct {
	GroupID        int64
	WeiboGroupID   string
	SenderID       int64
	WeiboSenderIDs []string
	MessageType    string
	Query          string
	Since          *time.Time
	Before         *time.Time
	Limit          int
	IncludeRaw     bool
}

func (s *Store) UpsertGroup(ctx context.Context, params UpsertGroupParams) (Group, error) {
	return upsertGroup(ctx, s.pool, params)
}

func upsertGroup(ctx context.Context, db messageDB, params UpsertGroupParams) (Group, error) {
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return Group{}, errors.New("group name is required")
	}
	if params.WeiboGroupID == "" {
		return upsertGroupByName(ctx, db, name, params.AvatarURL)
	}

	var group Group
	err := db.QueryRow(ctx, `
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

func upsertGroupByName(ctx context.Context, db messageDB, name string, avatarURL string) (Group, error) {
	var group Group
	err := db.QueryRow(ctx, `
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
	return upsertUser(ctx, s.pool, params)
}

func upsertUser(ctx context.Context, db messageDB, params UpsertUserParams) (User, error) {
	nickname := strings.TrimSpace(params.Nickname)
	if nickname == "" {
		return User{}, errors.New("sender nickname is required")
	}
	if params.WeiboUserID == "" {
		return upsertUserByNickname(ctx, db, nickname, params)
	}

	var user User
	err := db.QueryRow(ctx, `
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

func upsertUserByNickname(ctx context.Context, db messageDB, nickname string, params UpsertUserParams) (User, error) {
	var user User
	err := db.QueryRow(ctx, `
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

	// 群、用户、消息、附件和通知任务必须作为一个整体提交。
	// 任一步失败都会回滚，避免出现“消息存在但附件或通知任务丢失”的半完成状态。
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CreateMessageResult{}, err
	}
	defer tx.Rollback(ctx)

	group, err := upsertGroup(ctx, tx, params.Group)
	if err != nil {
		return CreateMessageResult{}, err
	}
	user, err := upsertUser(ctx, tx, params.Sender)
	if err != nil {
		return CreateMessageResult{}, err
	}

	// 微博事件可能因重连被重复采集。dedupe_key 是数据库级幂等键，
	// 比仅在内存中去重可靠，因为服务重启后仍然有效。
	dedupeKey := strings.TrimSpace(params.DedupeKey)
	if dedupeKey == "" {
		dedupeKey = BuildDedupeKey(params, group.ID, user.ID)
	}

	var message Message
	err = tx.QueryRow(ctx, `
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
		// ON CONFLICT DO NOTHING 在重复消息时不会返回行。ErrNoRows 在这里
		// 不是故障，而是“这条消息已存在”的正常分支。
		existing, loadErr := getMessageByDedupeKey(ctx, tx, dedupeKey)
		if loadErr != nil {
			return CreateMessageResult{}, loadErr
		}
		updated := false
		if len(existing.Attachments) == 0 && len(params.Attachments) > 0 {
			attachments, insertErr := insertAttachments(ctx, tx, existing.ID, params.Attachments)
			if insertErr != nil {
				return CreateMessageResult{}, insertErr
			}
			existing.Attachments = attachments
			updated = true
		}
		notificationsQueued := 0
		if params.Notification != nil {
			notificationsQueued, err = enqueueMatchingNotifications(ctx, tx, existing.ID, group, user, params.MessageType, *params.Notification)
			if err != nil {
				return CreateMessageResult{}, err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return CreateMessageResult{}, err
		}
		if updated {
			s.notifyRealtimeBestEffort(ctx, RealtimeEvent{Type: "message_updated", MessageID: existing.ID})
		}
		return CreateMessageResult{Message: existing, Created: false, NotificationsQueued: notificationsQueued}, nil
	}
	if err != nil {
		return CreateMessageResult{}, err
	}

	message.GroupName = group.Name
	message.WeiboGroupID = group.WeiboGroupID
	message.SenderName = user.Nickname
	message.WeiboSenderID = user.WeiboUserID
	message.SenderAvatar = user.AvatarURL
	if len(params.Attachments) > 0 {
		attachments, err := insertAttachments(ctx, tx, message.ID, params.Attachments)
		if err != nil {
			return CreateMessageResult{}, err
		}
		message.Attachments = attachments
	}

	if params.MessageType != "event" {
		if err := updateGroupLastMessageAt(ctx, tx, group.ID, params.SentAt); err != nil {
			return CreateMessageResult{}, err
		}
	}
	notificationsQueued := 0
	if params.Notification != nil {
		notificationsQueued, err = enqueueMatchingNotifications(ctx, tx, message.ID, group, user, params.MessageType, *params.Notification)
		if err != nil {
			return CreateMessageResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateMessageResult{}, err
	}
	// 实时通知发生在事务提交之后。即使通知失败，已提交的消息也不能回滚。
	s.notifyRealtimeBestEffort(ctx, RealtimeEvent{Type: "message_created", MessageID: message.ID})
	return CreateMessageResult{Message: message, Created: true, NotificationsQueued: notificationsQueued}, nil
}

func (s *Store) InsertAttachments(ctx context.Context, messageID int64, attachments []Attachment) ([]Attachment, error) {
	return insertAttachments(ctx, s.pool, messageID, attachments)
}

func insertAttachments(ctx context.Context, db messageDB, messageID int64, attachments []Attachment) ([]Attachment, error) {
	result := make([]Attachment, 0, len(attachments))
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.AttachmentType) == "" {
			attachment.AttachmentType = "unknown"
		}
		var saved Attachment
		err := db.QueryRow(ctx, `
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
	sourceURL := strings.TrimSpace(params.SourceURL)
	if fid == "" || imageURL == "" {
		return 0, nil
	}
	if !imageURLMatchesFID(imageURL, fid) {
		return 0, fmt.Errorf("image URL fid does not match attachment fid %s", fid)
	}
	imageURL = normalizeWeiboThumbnailURL(imageURL, sourceURL)

	metaPatch := map[string]any{
		"resolved_url": imageURL,
	}
	if sourceURL != "" {
		metaPatch["resolved_url_source"] = sourceURL
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

	rows, err := s.pool.Query(ctx, `
		with updated as (
		  update weibo_message_attachments
		     set url = $1,
		         meta = coalesce(meta, '{}'::jsonb) || $2::jsonb
		   where attachment_type = 'image'
		     and (
		           coalesce(url, '') = ''
			           or (
			                url like '%size=111,240%'
			                and not (coalesce(meta, '{}'::jsonb) ? 'resolved_url')
			           )
		           or (
		                $5
		                and coalesce(meta->>'resolved_url_source', '') like '%/query_messages.json%'
		           )
		           or $6
		     )
		     and (
		           coalesce(meta, '{}'::jsonb) @> $3::jsonb
		           or coalesce(meta, '{}'::jsonb) @> $4::jsonb
		     )
		  returning message_id
		)
		select distinct message_id
		  from updated
	`, imageURL, string(metaPatchRaw), string(fidFilterRaw), string(taggedURLRaw), isDirectImageReference(sourceURL, imageURL), params.Force)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var count int64
	for rows.Next() {
		var messageID int64
		if err := rows.Scan(&messageID); err != nil {
			return 0, err
		}
		count++
		if err := s.NotifyRealtimeEvent(ctx, RealtimeEvent{Type: "message_updated", MessageID: messageID}); err != nil {
			return 0, err
		}
	}
	return count, rows.Err()
}

func isDirectImageReference(sourceURL string, imageURL string) bool {
	return strings.TrimSpace(sourceURL) != "" && strings.TrimSpace(sourceURL) == strings.TrimSpace(imageURL)
}

func imageURLMatchesFID(rawURL string, fid string) bool {
	parsed, err := neturl.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	path := strings.ToLower(parsed.Path)
	nameIndex := strings.LastIndex(path, "/")
	if nameIndex >= 0 {
		path = path[nameIndex+1:]
	}
	if strings.HasPrefix(path, "videopicfeed") && strings.HasSuffix(path, ".png") {
		return false
	}
	if !strings.HasSuffix(parsed.Path, "/msget_thumbnail") && !strings.HasSuffix(parsed.Path, "/msget") {
		return true
	}
	return strings.TrimSpace(parsed.Query().Get("fid")) == strings.TrimSpace(fid)
}

func normalizeWeiboThumbnailURL(rawURL string, sourceURL string) string {
	parsed, err := neturl.Parse(strings.TrimSpace(rawURL))
	if err != nil || !strings.HasSuffix(parsed.Path, "/msget_thumbnail") {
		return strings.TrimSpace(rawURL)
	}
	query := parsed.Query()
	if query.Get("fid") == "" {
		return strings.TrimSpace(rawURL)
	}
	source := query.Get("source")
	if source == "" {
		if sourceFromURL := sourceQueryValue(sourceURL); sourceFromURL != "" {
			source = sourceFromURL
		} else {
			source = "209678993"
		}
		query.Set("source", source)
	}
	if query.Get("imageType") == "" {
		query.Set("imageType", "origin")
	}
	if size := normalizedThumbnailSize(query.Get("size")); size != "" {
		query.Set("size", size)
		query.Set("width", "240")
		query.Set("high", "240")
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func sourceQueryValue(rawURL string) string {
	parsed, err := neturl.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(parsed.Query().Get("source"))
}

func normalizedThumbnailSize(size string) string {
	parts := strings.Split(strings.TrimSpace(size), ",")
	if len(parts) != 2 {
		return ""
	}
	width, errW := strconv.Atoi(parts[0])
	height, errH := strconv.Atoi(parts[1])
	if errW != nil || errH != nil || width <= 0 || height <= 0 {
		return ""
	}
	if width <= 240 && height <= 240 {
		return ""
	}
	if width >= height {
		height = max(1, height*240/width)
		width = 240
	} else {
		width = max(1, width*240/height)
		height = 240
	}
	return fmt.Sprintf("%d,%d", width, height)
}

func (s *Store) GetMessageByDedupeKey(ctx context.Context, dedupeKey string) (Message, error) {
	return getMessageByDedupeKey(ctx, s.pool, dedupeKey)
}

func getMessageByDedupeKey(ctx context.Context, db messageDB, dedupeKey string) (Message, error) {
	messages, err := queryMessages(ctx, db, true, `
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

func (s *Store) GetMessageByID(ctx context.Context, id int64, includeRaw bool) (Message, error) {
	messages, err := queryMessages(ctx, s.pool, includeRaw, `
		where m.id = $1
		limit 1
	`, id)
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
		select g.id,
		       coalesce(g.weibo_group_id, ''),
		       g.name,
		       coalesce(g.avatar_url, ''),
		       latest.sent_at,
		       coalesce(latest.preview, ''),
		       g.created_at,
		       g.updated_at
		  from weibo_groups g
		  left join lateral (
		    select coalesce(m.sent_at, m.captured_at) as sent_at,
		           coalesce(nullif(m.content, ''), '[' || m.message_type || ']') as preview
		      from weibo_messages m
		     where m.group_id = g.id
		       and m.message_type <> 'event'
		       and coalesce(m.content, '') !~ '^微博群聊事件（类型 [0-9]+）$'
		     order by coalesce(m.sent_at, m.captured_at) desc, m.id desc
		     limit 1
		  ) latest on true
		 order by latest.sent_at desc nulls last, g.updated_at desc, g.id desc
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
			&group.LastMessagePreview,
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
		conditions = append(conditions, "m.message_type not in ('system', 'event')")
	}
	if len(params.WeiboSenderIDs) > 0 {
		conditions = append(conditions, "u.weibo_user_id = any("+addArg(params.WeiboSenderIDs)+"::text[])")
		conditions = append(conditions, "m.message_type not in ('system', 'event')")
	}
	if params.MessageType = strings.TrimSpace(params.MessageType); params.MessageType != "" {
		conditions = append(conditions, "m.message_type = "+addArg(params.MessageType))
	} else {
		conditions = append(conditions, "m.message_type <> 'event'")
		conditions = append(conditions, "coalesce(m.content, '') !~ '^微博群聊事件（类型 [0-9]+）$'")
	}
	if params.Query = strings.TrimSpace(params.Query); params.Query != "" {
		conditions = append(conditions, "strpos(lower(coalesce(m.content, '')), lower("+addArg(params.Query)+")) > 0")
	}
	if params.Since != nil {
		conditions = append(conditions, "coalesce(m.sent_at, m.captured_at) >= "+addArg(*params.Since))
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
	return queryMessages(ctx, s.pool, params.IncludeRaw, whereSQL, args...)
}

func queryMessages(ctx context.Context, db messageDB, includeRaw bool, whereSQL string, args ...any) ([]Message, error) {
	rawSelect := "'null'::jsonb"
	if includeRaw {
		rawSelect = "coalesce(m.raw, 'null'::jsonb)"
	}
	query := fmt.Sprintf(`
		select m.id,
		       coalesce(m.weibo_message_id, ''),
		       m.group_id,
		       coalesce(g.weibo_group_id, ''),
		       g.name,
		       m.sender_id,
		       coalesce(u.weibo_user_id, ''),
		       coalesce(u.nickname, ''),
		       coalesce(u.avatar_url, ''),
		       m.message_type,
		       coalesce(m.content, ''),
		       m.sent_at,
		       m.captured_at,
		       m.source,
		       m.dedupe_key,
		       %s,
		       m.created_at
		  from weibo_messages m
		  join weibo_groups g on g.id = m.group_id
		  left join weibo_users u on u.id = m.sender_id
		  %s
	`, rawSelect, whereSQL)
	rows, err := db.Query(ctx, query, args...)
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
			&message.SenderAvatar,
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

	attachments, err := attachmentsByMessageID(ctx, db, messageIDs(messages))
	if err != nil {
		return nil, err
	}
	for index := range messages {
		messages[index].Attachments = attachments[messages[index].ID]
	}
	return messages, nil
}

func attachmentsByMessageID(ctx context.Context, db messageDB, ids []int64) (map[int64][]Attachment, error) {
	result := make(map[int64][]Attachment)
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := db.Query(ctx, `
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
		if isUnresolvedLegacyThumbnail(attachment) {
			attachment.URL = ""
		} else if strings.TrimSpace(attachment.URL) != "" {
			attachment.URL = normalizeWeiboThumbnailURL(attachment.URL, attachmentResolvedSourceURL(attachment.Meta))
		}
		result[attachment.MessageID] = append(result[attachment.MessageID], attachment)
	}
	return result, rows.Err()
}

func attachmentResolvedSourceURL(meta json.RawMessage) string {
	var value struct {
		ResolvedURLSource string `json:"resolved_url_source"`
	}
	if json.Unmarshal(meta, &value) != nil {
		return ""
	}
	return value.ResolvedURLSource
}

func isLegacyDefaultThumbnailURL(rawURL string) bool {
	parsed, err := neturl.Parse(strings.TrimSpace(rawURL))
	if err != nil || !strings.HasSuffix(parsed.Path, "/msget_thumbnail") {
		return false
	}
	query := parsed.Query()
	return query.Get("size") == "111,240" && query.Get("width") == "240" && query.Get("high") == "240"
}

func isUnresolvedLegacyThumbnail(attachment Attachment) bool {
	if !isLegacyDefaultThumbnailURL(attachment.URL) {
		return false
	}
	var meta struct {
		ResolvedURL string `json:"resolved_url"`
	}
	return json.Unmarshal(attachment.Meta, &meta) != nil || strings.TrimSpace(meta.ResolvedURL) == ""
}

func updateGroupLastMessageAt(ctx context.Context, db messageDB, groupID int64, sentAt *time.Time) error {
	_, err := db.Exec(ctx, `
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

func (s *Store) notifyRealtimeBestEffort(ctx context.Context, event RealtimeEvent) {
	if err := s.NotifyRealtimeEvent(ctx, event); err != nil {
		slog.Warn("notify realtime event failed after database commit",
			"type", event.Type,
			"messageID", event.MessageID,
			"error", err,
		)
	}
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
