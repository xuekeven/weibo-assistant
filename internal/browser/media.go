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

	var metadata imageMetadataResponse
	if err := client.evaluatePromise(ctx, expression, &metadata); err != nil {
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
