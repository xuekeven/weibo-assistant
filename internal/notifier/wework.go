package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type WeworkClient struct {
	// client 设置统一超时，避免第三方服务无响应时永久占住 outbox worker。
	baseURL string
	apiKey  string
	toUser  string
	client  *http.Client
}

type SendResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
	MediaID string `json:"media_id,omitempty"`
}

func NewWeworkClient(baseURL, apiKey, toUser string) *WeworkClient {
	return &WeworkClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		toUser:  toUser,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *WeworkClient) Probe(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/wework/send", nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("wework probe failed: status=%d", resp.StatusCode)
	}
	return nil
}

func (c *WeworkClient) SendText(ctx context.Context, content string) (SendResponse, error) {
	return c.SendTextWithIdempotency(ctx, content, "")
}

func (c *WeworkClient) SendTextWithIdempotency(ctx context.Context, content string, idempotencyKey string) (SendResponse, error) {
	payload := map[string]string{"content": content}
	if c.toUser != "" {
		payload["touser"] = c.toUser
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return SendResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/wework/send/message", bytes.NewReader(body))
	if err != nil {
		return SendResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	return c.doSend(req)
}

func (c *WeworkClient) SendImage(ctx context.Context, imagePath string) (SendResponse, error) {
	file, err := os.Open(imagePath)
	if err != nil {
		return SendResponse{}, err
	}
	defer file.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("media", filepath.Base(imagePath))
	if err != nil {
		return SendResponse{}, err
	}
	if _, err := io.Copy(part, file); err != nil {
		return SendResponse{}, err
	}
	if c.toUser != "" {
		if err := writer.WriteField("touser", c.toUser); err != nil {
			return SendResponse{}, err
		}
	}
	if err := writer.Close(); err != nil {
		return SendResponse{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/wework/send/message", &body)
	if err != nil {
		return SendResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return c.doSend(req)
}

func (c *WeworkClient) doSend(req *http.Request) (SendResponse, error) {
	// 中继可能用 HTTP 200 返回业务错误，因此 HTTP 状态和 errcode 都要检查。
	resp, err := c.client.Do(req)
	if err != nil {
		return SendResponse{}, err
	}
	defer resp.Body.Close()

	var result SendResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return SendResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, fmt.Errorf("wework send failed: status=%d errcode=%d errmsg=%s", resp.StatusCode, result.ErrCode, result.ErrMsg)
	}
	if result.ErrCode != 0 {
		return result, fmt.Errorf("wework send failed: errcode=%d errmsg=%s", result.ErrCode, result.ErrMsg)
	}
	return result, nil
}
