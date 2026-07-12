package httpapi

import (
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
