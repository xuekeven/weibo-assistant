package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type GroupDailyMessageStats struct {
	Today     int64 `json:"today"`
	Yesterday int64 `json:"yesterday"`
}

type ExportMessage struct {
	ID             int64     `json:"id"`
	WeiboMessageID string    `json:"weiboMessageId,omitempty"`
	GroupName      string    `json:"groupName"`
	WeiboSenderID  string    `json:"weiboSenderId,omitempty"`
	SenderName     string    `json:"senderName,omitempty"`
	MessageType    string    `json:"messageType"`
	Content        string    `json:"content,omitempty"`
	SentAt         time.Time `json:"sentAt"`
	Source         string    `json:"source"`
}

func (s *Store) GetGroupDailyMessageStats(
	ctx context.Context,
	groupID int64,
	yesterdayStart time.Time,
	todayStart time.Time,
	tomorrowStart time.Time,
) (GroupDailyMessageStats, error) {
	if groupID <= 0 || !yesterdayStart.Before(todayStart) || !todayStart.Before(tomorrowStart) {
		return GroupDailyMessageStats{}, errors.New("invalid daily message stats range")
	}
	var stats GroupDailyMessageStats
	err := s.pool.QueryRow(ctx, `
		select count(*) filter (
		         where coalesce(m.sent_at, m.captured_at) >= $3
		           and coalesce(m.sent_at, m.captured_at) < $4
		       ) as today,
		       count(*) filter (
		         where coalesce(m.sent_at, m.captured_at) >= $2
		           and coalesce(m.sent_at, m.captured_at) < $3
		       ) as yesterday
		  from weibo_messages m
		  join weibo_users u on u.id = m.sender_id
		 where m.group_id = $1
		   and u.weibo_user_id ~ '^[0-9]+$'
		   and m.message_type not in ('system', 'event', 'recall')
		   and coalesce(m.content, '') !~ '^微博群聊事件（类型 [0-9]+）$'
	`, groupID, yesterdayStart, todayStart, tomorrowStart).Scan(&stats.Today, &stats.Yesterday)
	return stats, err
}

func (s *Store) ListGroupMessagesForExport(
	ctx context.Context,
	groupID int64,
	since time.Time,
	before time.Time,
	weiboSenderIDs []string,
) ([]ExportMessage, error) {
	if groupID <= 0 || !since.Before(before) {
		return nil, errors.New("invalid message export range")
	}
	args := []any{groupID, since, before}
	senderCondition := ""
	if len(weiboSenderIDs) > 0 {
		args = append(args, weiboSenderIDs)
		senderCondition = "and u.weibo_user_id = any($4::text[])"
	}
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		select m.id,
		       coalesce(m.weibo_message_id, ''),
		       g.name,
		       coalesce(u.weibo_user_id, ''),
		       coalesce(u.nickname, ''),
		       m.message_type,
		       coalesce(m.content, ''),
		       coalesce(m.sent_at, m.captured_at),
		       m.source
		  from weibo_messages m
		  join weibo_groups g on g.id = m.group_id
		  join weibo_users u on u.id = m.sender_id
		 where m.group_id = $1
		   and coalesce(m.sent_at, m.captured_at) >= $2
		   and coalesce(m.sent_at, m.captured_at) < $3
		   and u.weibo_user_id ~ '^[0-9]+$'
		   and m.message_type not in ('system', 'event', 'recall')
		   and coalesce(m.content, '') !~ '^微博群聊事件（类型 [0-9]+）$'
		   %s
		 order by coalesce(m.sent_at, m.captured_at), m.id
	`, senderCondition), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := make([]ExportMessage, 0)
	for rows.Next() {
		var message ExportMessage
		if err := rows.Scan(
			&message.ID,
			&message.WeiboMessageID,
			&message.GroupName,
			&message.WeiboSenderID,
			&message.SenderName,
			&message.MessageType,
			&message.Content,
			&message.SentAt,
			&message.Source,
		); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}
