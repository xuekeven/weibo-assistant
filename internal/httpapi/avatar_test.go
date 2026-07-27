package httpapi

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xuekeven/weibo-assistant/internal/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestValidatedAvatarURL(t *testing.T) {
	tests := []struct {
		name    string
		rawURL  string
		wantErr bool
	}{
		{name: "sina image CDN", rawURL: "https://tvax1.sinaimg.cn/crop.avatar.jpg"},
		{name: "uppercase host", rawURL: "https://TVA1.SINAIMG.CN/avatar.jpg"},
		{name: "http rejected", rawURL: "http://tvax1.sinaimg.cn/avatar.jpg", wantErr: true},
		{name: "lookalike domain rejected", rawURL: "https://sinaimg.cn.example.com/avatar.jpg", wantErr: true},
		{name: "credentials rejected", rawURL: "https://user:password@tvax1.sinaimg.cn/avatar.jpg", wantErr: true},
		{name: "custom port rejected", rawURL: "https://tvax1.sinaimg.cn:8443/avatar.jpg", wantErr: true},
		{name: "internal host rejected", rawURL: "https://127.0.0.1/avatar.jpg", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := validatedAvatarURL(test.rawURL)
			if (err != nil) != test.wantErr {
				t.Fatalf("validatedAvatarURL() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestAvatarProxyReturnsValidatedImageWithoutReferer(t *testing.T) {
	server := NewServer(config.Config{AppEnv: "production"}, nil, nil)
	var upstreamRequest *http.Request
	server.avatarClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		upstreamRequest = request
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{"Content-Type": []string{"image/jpeg"}},
			Body:          io.NopCloser(bytes.NewReader([]byte{0xff, 0xd8, 0xff, 0xdb})),
			ContentLength: 4,
			Request:       request,
		}, nil
	})}

	request := httptest.NewRequest(http.MethodGet, "/api/media/avatar?url=https%3A%2F%2Ftvax1.sinaimg.cn%2Favatar.jpg", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if upstreamRequest == nil {
		t.Fatal("upstream request was not sent")
	}
	if referer := upstreamRequest.Header.Get("Referer"); referer != "" {
		t.Fatalf("Referer = %q, want empty", referer)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "image/jpeg" {
		t.Fatalf("Content-Type = %q", contentType)
	}
	if cacheControl := response.Header().Get("Cache-Control"); !strings.Contains(cacheControl, "max-age=86400") {
		t.Fatalf("Cache-Control = %q", cacheControl)
	}
}

func TestAvatarProxyRejectsUnsafeURLBeforeFetching(t *testing.T) {
	server := NewServer(config.Config{AppEnv: "production"}, nil, nil)
	called := false
	server.avatarClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		called = true
		return nil, nil
	})}

	request := httptest.NewRequest(http.MethodGet, "/api/media/avatar?url=https%3A%2F%2F127.0.0.1%2Fsecret", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if called {
		t.Fatal("unsafe avatar URL reached the upstream client")
	}
}

func TestAvatarProxyRejectsNonImageResponse(t *testing.T) {
	server := NewServer(config.Config{AppEnv: "production"}, nil, nil)
	server.avatarClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/html"}},
			Body:       io.NopCloser(strings.NewReader("<html></html>")),
			Request:    request,
		}, nil
	})}

	request := httptest.NewRequest(http.MethodGet, "/api/media/avatar?url=https%3A%2F%2Ftvax1.sinaimg.cn%2Favatar.jpg", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadGateway)
	}
}
