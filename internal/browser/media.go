package browser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const imageMetadataEndpoint = "https://upload.api.weibo.com/2/mss/meta_query.json"

type imageMetadataResponse struct {
	Thumbnail240 string `json:"thumbnail_240"`
	Error        string `json:"error"`
	ErrorCode    int    `json:"error_code"`
}

// FetchImageThumbnailURL uses the already logged-in Weibo page to resolve an
// image fid. The metadata endpoint is JSONP-only in the chat page, so a script
// element is used instead of fetch to keep the request inside the browser's
// authenticated session without being blocked by CORS.
func FetchImageThumbnailURL(ctx context.Context, webSocketDebuggerURL string, fid string) (string, string, error) {
	fid = strings.TrimSpace(fid)
	if fid == "" {
		return "", "", errors.New("image fid is required")
	}
	if strings.TrimSpace(webSocketDebuggerURL) == "" {
		return "", "", errors.New("chat page debugger URL is required")
	}

	callback, err := imageMetadataCallbackName()
	if err != nil {
		return "", "", err
	}
	requestURL := imageMetadataRequestURL(fid, callback)
	callbackJSON, _ := json.Marshal(callback)
	requestURLJSON, _ := json.Marshal(requestURL)
	expression := fmt.Sprintf(`new Promise((resolve, reject) => {
		const callbackName = %s;
		const requestURL = %s;
		const script = document.createElement('script');
		let timer;
		const cleanup = () => {
			if (timer) clearTimeout(timer);
			delete window[callbackName];
			script.remove();
		};
		window[callbackName] = (payload) => {
			cleanup();
			resolve(payload);
		};
		script.onerror = () => {
			cleanup();
			reject(new Error('Weibo image metadata request failed'));
		};
		timer = setTimeout(() => {
			cleanup();
			reject(new Error('Weibo image metadata request timed out'));
		}, 8000);
		script.src = requestURL;
		document.head.appendChild(script);
	})`, string(callbackJSON), string(requestURLJSON))

	client, err := newCDPClient(ctx, webSocketDebuggerURL)
	if err != nil {
		return "", "", err
	}
	defer client.Close()
	if err := client.call(ctx, "Network.enable", map[string]any{}, nil); err != nil {
		return "", requestURL, fmt.Errorf("enable network diagnostics: %w", err)
	}

	var metadata imageMetadataResponse
	if err := client.evaluatePromise(ctx, expression, &metadata); err != nil {
		if responseErr := imageMetadataNetworkError(ctx, client, requestURL); responseErr != nil {
			return "", requestURL, fmt.Errorf("fetch image metadata for fid %s: %w", fid, responseErr)
		}
		return "", requestURL, fmt.Errorf("fetch image metadata for fid %s: %w", fid, err)
	}
	if metadata.Error != "" {
		return "", requestURL, fmt.Errorf("weibo image metadata error %d: %s", metadata.ErrorCode, metadata.Error)
	}
	thumbnail := strings.TrimSpace(metadata.Thumbnail240)
	if thumbnail == "" {
		return "", requestURL, fmt.Errorf("weibo image metadata for fid %s has no thumbnail_240", fid)
	}
	return thumbnail, requestURL, nil
}

func imageMetadataNetworkError(ctx context.Context, client *cdpClient, requestURL string) error {
	var response struct {
		RequestID string `json:"requestId"`
		Response  struct {
			URL        string `json:"url"`
			Status     int    `json:"status"`
			StatusText string `json:"statusText"`
		} `json:"response"`
	}
	for index := len(client.pending) - 1; index >= 0; index-- {
		envelope := client.pending[index]
		if envelope.Method != "Network.responseReceived" || json.Unmarshal(envelope.Params, &response) != nil {
			continue
		}
		if response.Response.URL != requestURL {
			continue
		}
		body, _ := getNetworkResponseBody(ctx, client, response.RequestID)
		return newImageMetadataHTTPError(response.Response.Status, response.Response.StatusText, body)
	}
	return nil
}

func newImageMetadataHTTPError(status int, statusText string, body string) error {
	var payload imageMetadataResponse
	if json.Unmarshal([]byte(strings.TrimSpace(body)), &payload) == nil && payload.Error != "" {
		if payload.ErrorCode != 0 {
			return fmt.Errorf("weibo image metadata returned HTTP %d (error %d): %s", status, payload.ErrorCode, payload.Error)
		}
		return fmt.Errorf("weibo image metadata returned HTTP %d: %s", status, payload.Error)
	}
	statusText = strings.TrimSpace(statusText)
	if statusText != "" {
		return fmt.Errorf("weibo image metadata returned HTTP %d %s", status, statusText)
	}
	return fmt.Errorf("weibo image metadata returned HTTP %d", status)
}

func imageMetadataRequestURL(fid string, callback string) string {
	query := url.Values{
		"source":   {"209678993"},
		"fid":      {strings.TrimSpace(fid)},
		"replace":  {"false"},
		"callback": {callback},
	}
	return imageMetadataEndpoint + "?" + query.Encode()
}

func imageMetadataCallbackName() (string, error) {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate image metadata callback: %w", err)
	}
	return "__weiboAssistantImage_" + hex.EncodeToString(value), nil
}
