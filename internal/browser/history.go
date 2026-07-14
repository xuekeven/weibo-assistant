package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const groupHistoryEndpoint = "https://api.weibo.com/webim/groupchat/query_messages.json"

type GroupHistoryRequest struct {
	GroupID string
	MaxMID  string
	Count   int
	Source  string
}

type GroupHistoryPage struct {
	Messages  []NetworkMessage
	OldestMID string
	NewestMID string
	Raw       json.RawMessage
}

func FetchGroupHistoryPage(ctx context.Context, webSocketDebuggerURL string, request GroupHistoryRequest, messageSource string) (GroupHistoryPage, error) {
	if strings.TrimSpace(webSocketDebuggerURL) == "" {
		return GroupHistoryPage{}, errors.New("chat page debugger URL is required")
	}
	if strings.TrimSpace(request.GroupID) == "" {
		return GroupHistoryPage{}, errors.New("history group ID is required")
	}
	if request.Count <= 0 || request.Count > 100 {
		request.Count = 20
	}
	if strings.TrimSpace(request.MaxMID) == "" {
		request.MaxMID = "0"
	}
	if strings.TrimSpace(request.Source) == "" {
		request.Source = "209678993"
	}

	params := url.Values{
		"convert_emoji": {"1"},
		"query_sender":  {"1"},
		"count":         {strconv.Itoa(request.Count)},
		"id":            {request.GroupID},
		"max_mid":       {request.MaxMID},
		"source":        {request.Source},
		"t":             {strconv.FormatInt(time.Now().UnixMilli(), 10)},
	}
	requestURL := groupHistoryEndpoint + "?" + params.Encode()
	encodedURL, _ := json.Marshal(requestURL)
	expression := fmt.Sprintf(`(async () => {
		const response = await fetch(%s, { credentials: 'include' });
		const body = await response.text();
		return { status: response.status, body };
	})()`, encodedURL)

	client, err := newCDPClient(ctx, webSocketDebuggerURL)
	if err != nil {
		return GroupHistoryPage{}, err
	}
	defer client.Close()
	var result struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	if err := client.evaluatePromise(ctx, expression, &result); err != nil {
		return GroupHistoryPage{}, fmt.Errorf("fetch group history: %w", err)
	}
	if result.Status < 200 || result.Status >= 300 {
		return GroupHistoryPage{}, fmt.Errorf("group history returned HTTP %d", result.Status)
	}
	page, err := ParseGroupHistoryResponse([]byte(result.Body), request.GroupID, messageSource)
	if err != nil {
		return GroupHistoryPage{}, err
	}
	return page, nil
}

func ParseGroupHistoryResponse(body []byte, fallbackGroupID string, messageSource string) (GroupHistoryPage, error) {
	var response struct {
		Result      bool              `json:"result"`
		Messages    []json.RawMessage `json:"messages"`
		GroupTips   json.RawMessage   `json:"group_tips"`
		Significant json.RawMessage   `json:"significant_msgs"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return GroupHistoryPage{}, fmt.Errorf("decode group history response: %w", err)
	}
	if !response.Result && len(response.Messages) == 0 {
		return GroupHistoryPage{}, errors.New("group history response reported failure")
	}
	rawMessages := append([]json.RawMessage(nil), response.Messages...)
	rawMessages = append(rawMessages, historyMessageList(response.GroupTips)...)
	rawMessages = append(rawMessages, historyMessageList(response.Significant)...)

	messages := make([]NetworkMessage, 0, len(rawMessages))
	seen := make(map[string]struct{}, len(rawMessages))
	for _, raw := range rawMessages {
		normalized, eventType, err := normalizeHistoryMessage(raw, fallbackGroupID)
		if err != nil {
			continue
		}
		message, ok := parseGroupChatMessage(normalized, normalized, eventType)
		if !ok {
			continue
		}
		message.Source = strings.TrimSpace(messageSource)
		if message.Source == "" {
			message.Source = "weibo_history"
		}
		message.Raw = normalized
		if _, exists := seen[message.DedupeKey]; exists {
			continue
		}
		seen[message.DedupeKey] = struct{}{}
		messages = append(messages, message)
	}
	sort.SliceStable(messages, func(i, j int) bool {
		if messages[i].SentAt != nil && messages[j].SentAt != nil && !messages[i].SentAt.Equal(*messages[j].SentAt) {
			return messages[i].SentAt.Before(*messages[j].SentAt)
		}
		return messages[i].WeiboMessageID < messages[j].WeiboMessageID
	})

	page := GroupHistoryPage{Messages: messages, Raw: append(json.RawMessage(nil), body...)}
	if len(messages) > 0 {
		page.OldestMID = messages[0].WeiboMessageID
		page.NewestMID = messages[len(messages)-1].WeiboMessageID
	}
	return page, nil
}

func normalizeHistoryMessage(raw json.RawMessage, fallbackGroupID string) (json.RawMessage, int, error) {
	var value map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, 0, err
	}
	if _, ok := value["gid"]; !ok && strings.TrimSpace(fallbackGroupID) != "" {
		value["gid"] = fallbackGroupID
	}
	eventType := numberAsInt(value["sub_type"])
	if eventType == 0 {
		eventType = numberAsInt(value["type"])
	}
	normalized, err := json.Marshal(value)
	return normalized, eventType, err
}

func historyMessageList(raw json.RawMessage) []json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var wrapper struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if json.Unmarshal(raw, &wrapper) == nil {
		return wrapper.Messages
	}
	return nil
}

func numberAsInt(value any) int {
	switch typed := value.(type) {
	case json.Number:
		result, _ := strconv.Atoi(typed.String())
		return result
	case float64:
		return int(typed)
	case string:
		result, _ := strconv.Atoi(typed)
		return result
	default:
		return 0
	}
}
