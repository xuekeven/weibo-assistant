package httpapi

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/xuekeven/weibo-assistant/internal/config"
	"github.com/xuekeven/weibo-assistant/internal/store"
)

func TestDevMessageRouteDependsOnAppEnvironment(t *testing.T) {
	tests := []struct {
		name       string
		appEnv     string
		wantStatus int
	}{
		{name: "development", appEnv: "development", wantStatus: http.StatusBadRequest},
		{name: "production", appEnv: "production", wantStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := NewServer(config.Config{AppEnv: tt.appEnv}, nil, nil)
			request := httptest.NewRequest(http.MethodPost, "/api/dev/messages", strings.NewReader("{"))
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tt.wantStatus)
			}
		})
	}
}

func TestStrictRequestValidation(t *testing.T) {
	server := NewServer(config.Config{AppEnv: "development"}, nil, nil)
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "invalid limit", method: http.MethodGet, path: "/api/groups?limit=abc"},
		{name: "limit too large", method: http.MethodGet, path: "/api/groups?limit=201"},
		{name: "invalid boolean", method: http.MethodGet, path: "/api/messages?includeRaw=maybe"},
		{name: "invalid since time", method: http.MethodGet, path: "/api/messages?since=yesterday"},
		{name: "invalid delete rule id", method: http.MethodDelete, path: "/api/notification-rules/not-a-number"},
		{name: "invalid retry job id", method: http.MethodPost, path: "/api/notification-outbox/not-a-number/retry"},
		{name: "invalid message detail id", method: http.MethodGet, path: "/api/messages/not-a-number"},
		{name: "invalid media resolution message id", method: http.MethodPost, path: "/api/messages/not-a-number/resolve-media"},
		{name: "invalid media retry message id", method: http.MethodPost, path: "/api/messages/not-a-number/retry-media"},
		{name: "missing daily stats range", method: http.MethodGet, path: "/api/groups/3/message-stats"},
		{name: "unordered daily stats range", method: http.MethodGet, path: "/api/groups/3/message-stats?yesterdayStart=2026-07-22T00%3A00%3A00Z&todayStart=2026-07-21T00%3A00%3A00Z&tomorrowStart=2026-07-23T00%3A00%3A00Z"},
		{name: "missing message export range", method: http.MethodGet, path: "/api/groups/3/messages/export"},
		{name: "unknown json field", method: http.MethodPost, path: "/api/dev/messages", body: `{"unknown":true}`},
		{name: "multiple json values", method: http.MethodPost, path: "/api/dev/messages", body: `{} {}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestWebSocketRequiresVersion13(t *testing.T) {
	server := NewServer(config.Config{AppEnv: "production"}, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/ws", nil)
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Connection", "Upgrade")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUpgradeRequired {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUpgradeRequired)
	}
}

func TestLiveEndpointDoesNotRequireDependencies(t *testing.T) {
	server := NewServer(config.Config{AppEnv: "production"}, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/livez", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
}

func TestCORSAllowsRuleDeletionAndExposesDownloadFilename(t *testing.T) {
	server := NewServer(config.Config{AppEnv: "production", FrontendPort: 6002}, nil, nil)
	request := httptest.NewRequest(http.MethodOptions, "/api/notification-rules/1", nil)
	request.Header.Set("Origin", "http://localhost:6002")
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if methods := response.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(methods, "DELETE") {
		t.Fatalf("Access-Control-Allow-Methods = %q, want DELETE", methods)
	}
	if exposed := response.Header().Get("Access-Control-Expose-Headers"); exposed != "Content-Disposition, X-Request-ID" {
		t.Fatalf("Access-Control-Expose-Headers = %q", exposed)
	}
}

func TestCORSSupportsTailscaleIPAndRejectsPublicOriginsByDefault(t *testing.T) {
	server := NewServer(config.Config{AppEnv: "production", FrontendPort: 6002}, nil, nil)
	for _, test := range []struct {
		origin string
		want   bool
	}{
		{origin: "http://100.80.90.100:6002", want: true},
		{origin: "http://192.168.1.10:6002", want: true},
		{origin: "https://server.example.com:6002", want: false},
	} {
		if got := server.isAllowedOrigin(test.origin); got != test.want {
			t.Fatalf("isAllowedOrigin(%q) = %v, want %v", test.origin, got, test.want)
		}
	}
}

func TestServerUsesConfiguredTailscaleBindAddress(t *testing.T) {
	server := NewServer(config.Config{
		BackendBindAddr: "100.108.244.64",
		BackendPort:     6001,
	}, nil, nil)
	if got := server.Addr(); got != "100.108.244.64:6001" {
		t.Fatalf("server address = %q", got)
	}
}

func TestWriteErrorHidesInternalDetails(t *testing.T) {
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previousLogger)
	})

	response := httptest.NewRecorder()
	response.Header().Set("X-Request-ID", "request-123")
	writeError(response, http.StatusInternalServerError, errors.New("postgresql://user:secret@db.internal/app?token=abc api_key=private"))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("internal error leaked: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "request-123") {
		t.Fatalf("request id missing: %s", response.Body.String())
	}
	if strings.Contains(logs.String(), "secret") || strings.Contains(logs.String(), "private") || strings.Contains(logs.String(), "token=abc") {
		t.Fatalf("sensitive error detail leaked to log: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "db.internal/app") || !strings.Contains(logs.String(), "request-123") {
		t.Fatalf("useful error context missing from log: %s", logs.String())
	}
}

func TestMonitorFreshness(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	heartbeat := now.Add(-30 * time.Second)
	fresh, age := monitorFreshness(store.MonitorStatus{LastHeartbeatAt: &heartbeat}, now, time.Minute)
	if !fresh || age != 30 {
		t.Fatalf("fresh=%v age=%d", fresh, age)
	}
	fresh, age = monitorFreshness(store.MonitorStatus{}, now, time.Minute)
	if fresh || age != -1 {
		t.Fatalf("missing heartbeat should be stale: fresh=%v age=%d", fresh, age)
	}
}

func TestReadinessStatusRequiresFreshMonitor(t *testing.T) {
	if status, label := readinessHTTPStatus(false); status != http.StatusServiceUnavailable || label != "not_ready" {
		t.Fatalf("stale monitor readiness = %d %q", status, label)
	}
	if status, label := readinessHTTPStatus(true); status != http.StatusOK || label != "ready" {
		t.Fatalf("fresh monitor readiness = %d %q", status, label)
	}
}
