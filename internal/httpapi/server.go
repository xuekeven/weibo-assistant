package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/xuekeven/weibo-assistant/internal/config"
	"github.com/xuekeven/weibo-assistant/internal/notifier"
	"github.com/xuekeven/weibo-assistant/internal/store"
)

type Server struct {
	// Server 持有所有路由共享的依赖，作用类似前端应用中的全局 service 容器。
	cfg          config.Config
	store        *store.Store
	notifier     *notifier.WeworkClient
	mux          *http.ServeMux
	wsHub        *websocketHub
	avatarClient *http.Client
}

func NewServer(cfg config.Config, st *store.Store, notify *notifier.WeworkClient) *Server {
	s := &Server{
		cfg:          cfg,
		store:        st,
		notifier:     notify,
		mux:          http.NewServeMux(),
		wsHub:        newWebsocketHub(),
		avatarClient: newAvatarHTTPClient(),
	}
	s.routes()
	return s
}

func (s *Server) Addr() string {
	return net.JoinHostPort(s.cfg.BackendBindAddr, strconv.Itoa(s.cfg.BackendPort))
}

func (s *Server) Handler() http.Handler {
	// 中间件包在路由器外层，因此所有端点都会经过同一套 CORS 处理。
	return s.withCORS(s.mux)
}

func (s *Server) routes() {
	// Go 1.22 的 ServeMux 支持 "METHOD /path/{param}" 形式的路由声明。
	// 按“运行状态 -> 业务资源 -> 实时能力 -> 开发工具”的顺序注册。
	s.mux.HandleFunc("GET /livez", s.handleLive)
	s.mux.HandleFunc("GET /readyz", s.handleReady)
	s.mux.HandleFunc("GET /healthz", s.handleReady)
	s.mux.HandleFunc("GET /metrics", s.handleMetrics)
	s.mux.HandleFunc("GET /api/config", s.handleAppConfig)
	s.mux.HandleFunc("GET /api/media/avatar", s.handleAvatar)
	s.mux.HandleFunc("GET /api/groups", s.handleListGroups)
	s.mux.HandleFunc("GET /api/users", s.handleListUsers)
	s.mux.HandleFunc("GET /api/messages", s.handleListMessages)
	s.mux.HandleFunc("POST /api/messages", s.handleCreateMessage)
	s.mux.HandleFunc("GET /api/messages/{id}", s.handleGetMessage)
	s.mux.HandleFunc("POST /api/messages/{id}/resolve-media", s.handleResolveMessageMedia)
	s.mux.HandleFunc("POST /api/messages/{id}/retry-media", s.handleRetryMessageMedia)
	s.mux.HandleFunc("GET /api/groups/{id}/messages", s.handleListGroupMessages)
	s.mux.HandleFunc("GET /api/groups/{id}/message-stats", s.handleGetGroupMessageStats)
	s.mux.HandleFunc("GET /api/groups/{id}/messages/export", s.handleExportGroupMessages)
	s.mux.HandleFunc("GET /api/groups/{id}/history-sync", s.handleGetHistorySync)
	s.mux.HandleFunc("POST /api/groups/{id}/history-sync", s.handleCreateHistorySync)
	s.mux.HandleFunc("GET /api/notification-rules", s.handleListNotificationRules)
	s.mux.HandleFunc("POST /api/notification-rules", s.handleCreateNotificationRule)
	s.mux.HandleFunc("PATCH /api/notification-rules/{id}", s.handleUpdateNotificationRule)
	s.mux.HandleFunc("DELETE /api/notification-rules/{id}", s.handleDeleteNotificationRule)
	s.mux.HandleFunc("GET /api/notification-events", s.handleListNotificationEvents)
	s.mux.HandleFunc("POST /api/notification-events", s.handleCreateNotificationEvent)
	s.mux.HandleFunc("GET /api/notification-outbox", s.handleListNotificationOutbox)
	s.mux.HandleFunc("POST /api/notification-outbox/{id}/retry", s.handleRetryNotificationOutbox)
	s.mux.HandleFunc("GET /api/monitor/status", s.handleMonitorStatus)
	s.mux.HandleFunc("GET /api/ws", s.handleWebSocket)
	s.mux.HandleFunc("GET /api/notify/probe", s.handleNotifyProbe)
	if strings.EqualFold(strings.TrimSpace(s.cfg.AppEnv), "development") {
		s.mux.HandleFunc("POST /api/dev/messages", s.handleCreateDevMessage)
	}
}

func (s *Server) handleLive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "alive",
		"time":   time.Now().Format(time.RFC3339),
	})
}

func (s *Server) handleAppConfig(w http.ResponseWriter, r *http.Request) {
	currentUserID, err := s.store.GetCurrentWeiboUserID(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"targetSenderId": s.cfg.TargetSenderID,
		"currentUserId":  currentUserID,
		"weiboChatUrl":   s.cfg.WeiboChatURL,
	})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	status, err := s.store.GetMonitorStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	fresh, ageSeconds := monitorFreshness(status, time.Now(), s.cfg.MonitorStaleAfter)
	backlog, err := s.store.GetNotificationBacklog(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	responseStatus, statusText := readinessHTTPStatus(fresh)
	writeJSON(w, responseStatus, map[string]any{
		"status":                  statusText,
		"database":                "ok",
		"monitorFresh":            fresh,
		"monitorHeartbeatAgeSecs": ageSeconds,
		"monitorStatus":           status.Status,
		"lastMessageAt":           status.LastMessageAt,
		"notificationBacklog":     backlog,
		"time":                    time.Now().Format(time.RFC3339),
	})
}

func (s *Server) handleMonitorStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.store.GetMonitorStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.monitorStatusView(status))
}

type monitorStatusView struct {
	Status           string     `json:"status"`
	CurrentURL       string     `json:"currentUrl,omitempty"`
	LastMessageAt    *time.Time `json:"lastMessageAt,omitempty"`
	LastHeartbeatAt  *time.Time `json:"lastHeartbeatAt,omitempty"`
	LastError        string     `json:"lastError,omitempty"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	Fresh            bool       `json:"fresh"`
	HeartbeatAgeSecs int64      `json:"heartbeatAgeSecs"`
	StaleAfterSecs   int64      `json:"staleAfterSecs"`
}

func (s *Server) monitorStatusView(status store.MonitorStatus) monitorStatusView {
	fresh, ageSeconds := monitorFreshness(status, time.Now(), s.cfg.MonitorStaleAfter)
	return monitorStatusView{
		Status: status.Status, CurrentURL: status.CurrentURL, LastMessageAt: status.LastMessageAt,
		LastHeartbeatAt: status.LastHeartbeatAt, LastError: status.LastError, UpdatedAt: status.UpdatedAt,
		Fresh: fresh, HeartbeatAgeSecs: ageSeconds, StaleAfterSecs: int64(s.cfg.MonitorStaleAfter.Seconds()),
	}
}

func monitorFreshness(status store.MonitorStatus, now time.Time, staleAfter time.Duration) (bool, int64) {
	if staleAfter <= 0 {
		staleAfter = 90 * time.Second
	}
	if status.LastHeartbeatAt == nil {
		return false, -1
	}
	age := now.Sub(*status.LastHeartbeatAt)
	if age < 0 {
		age = 0
	}
	return age <= staleAfter, int64(age.Seconds())
}

func readinessHTTPStatus(monitorFresh bool) (int, string) {
	if !monitorFresh {
		return http.StatusServiceUnavailable, "not_ready"
	}
	return http.StatusOK, "ready"
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	status, err := s.store.GetMonitorStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	metrics, err := s.store.GetRuntimeMetrics(r.Context(), "monitor")
	if errors.Is(err, pgx.ErrNoRows) {
		metrics = store.RuntimeMetrics{ServiceName: "monitor"}
	} else if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	backlog, err := s.store.GetNotificationBacklog(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	fresh, ageSeconds := monitorFreshness(status, time.Now(), s.cfg.MonitorStaleAfter)
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	writeMetric := func(name string, value any) {
		_, _ = fmt.Fprintf(w, "%s %v\n", name, value)
	}
	writeMetric("weibo_assistant_server_up", 1)
	writeMetric("weibo_assistant_server_alloc_bytes", memory.Alloc)
	writeMetric("weibo_assistant_server_goroutines", runtime.NumGoroutine())
	writeMetric("weibo_assistant_monitor_fresh", boolMetric(fresh))
	writeMetric("weibo_assistant_monitor_heartbeat_age_seconds", ageSeconds)
	writeMetric("weibo_assistant_monitor_browser_running", boolMetric(metrics.BrowserRunning))
	writeMetric("weibo_assistant_monitor_cdp_connected", boolMetric(metrics.CDPConnected))
	writeMetric("weibo_assistant_monitor_cdp_reconnect_total", metrics.CDPReconnectTotal)
	writeMetric("weibo_assistant_monitor_messages_parsed_total", metrics.MessagesParsedTotal)
	writeMetric("weibo_assistant_monitor_messages_parsed_last_minute", metrics.MessagesParsedLastMinute)
	writeMetric("weibo_assistant_monitor_persist_success_total", metrics.PersistSuccessTotal)
	writeMetric("weibo_assistant_monitor_persist_failure_total", metrics.PersistFailureTotal)
	writeMetric("weibo_assistant_monitor_dedupe_total", metrics.DedupeTotal)
	writeMetric("weibo_assistant_monitor_notification_sent_total", metrics.NotificationSentTotal)
	writeMetric("weibo_assistant_monitor_notification_retry_total", metrics.NotificationRetryTotal)
	writeMetric("weibo_assistant_monitor_notification_failed_total", metrics.NotificationFailedTotal)
	writeMetric("weibo_assistant_monitor_tracked_requests", metrics.TrackedRequests)
	writeMetric("weibo_assistant_monitor_image_cache_items", metrics.ImageCacheItems)
	writeMetric("weibo_assistant_monitor_alloc_bytes", metrics.AllocBytes)
	writeMetric("weibo_assistant_monitor_goroutines", metrics.Goroutines)
	if !metrics.ProcessStartedAt.IsZero() {
		writeMetric("weibo_assistant_monitor_process_started_timestamp_seconds", metrics.ProcessStartedAt.Unix())
	}
	if !metrics.UpdatedAt.IsZero() {
		writeMetric("weibo_assistant_monitor_metrics_updated_timestamp_seconds", metrics.UpdatedAt.Unix())
	}
	writeMetric("weibo_assistant_notification_backlog{status=\"pending\"}", backlog.Pending)
	writeMetric("weibo_assistant_notification_backlog{status=\"sending\"}", backlog.Sending)
	writeMetric("weibo_assistant_notification_backlog{status=\"failed\"}", backlog.Failed)
	if status.LastMessageAt != nil {
		writeMetric("weibo_assistant_monitor_last_message_timestamp_seconds", status.LastMessageAt.Unix())
	}
}

func boolMetric(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Server) handleNotifyProbe(w http.ResponseWriter, r *http.Request) {
	if err := s.notifier.Probe(r.Context()); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"url":    s.cfg.WeworkRelayURL,
	})
}

func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := newRequestID()
		w.Header().Set("X-Request-ID", requestID)
		origin := r.Header.Get("Origin")
		if s.isAllowedOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Expose-Headers", "Content-Disposition, X-Request-ID")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) isAllowedOrigin(origin string) bool {
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	normalizedOrigin := strings.TrimRight(origin, "/")
	if len(s.cfg.CORSAllowedOrigins) > 0 {
		for _, allowed := range s.cfg.CORSAllowedOrigins {
			if normalizedOrigin == allowed {
				return true
			}
		}
		return false
	}
	if parsed.Port() != strconv.Itoa(s.cfg.FrontendPort) {
		return false
	}
	host := strings.Trim(strings.ToLower(parsed.Hostname()), "[]")
	if host == "localhost" || strings.HasSuffix(host, ".ts.net") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || isTailscaleIP(ip))
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	// 必须先设置 header 和状态码，再写 body；WriteHeader 后修改 header 不会生效。
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("write json failed", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, err error) {
	requestID := w.Header().Get("X-Request-ID")
	message := err.Error()
	if status >= http.StatusInternalServerError {
		slog.Error("http request failed", "requestID", requestID, "status", status, "error", errorLogValue(err))
		switch status {
		case http.StatusBadGateway:
			message = "upstream service unavailable"
		case http.StatusServiceUnavailable:
			message = "service unavailable"
		default:
			message = "internal server error"
		}
	}
	payload := map[string]any{"error": message}
	if requestID != "" {
		payload["requestId"] = requestID
	}
	writeJSON(w, status, payload)
}

var (
	logURLPattern    = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s"'<>]+`)
	logSecretPattern = regexp.MustCompile(`(?i)\b(password|passwd|token|api[_-]?key|secret)=([^\s&]+)`)
)

func errorLogValue(err error) string {
	if err == nil {
		return ""
	}
	message := logURLPattern.ReplaceAllStringFunc(err.Error(), func(rawURL string) string {
		parsed, parseErr := url.Parse(rawURL)
		if parseErr != nil || parsed.Host == "" {
			return "<redacted-url>"
		}
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		return parsed.String()
	})
	message = logSecretPattern.ReplaceAllString(message, "$1=<redacted>")
	const maxLoggedErrorLength = 1000
	if len(message) > maxLoggedErrorLength {
		return message[:maxLoggedErrorLength] + "…"
	}
	return message
}

func newRequestID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err == nil {
		return hex.EncodeToString(value[:])
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func isTailscaleIP(ip net.IP) bool {
	ipv4 := ip.To4()
	return ipv4 != nil &&
		ipv4[0] == 100 &&
		ipv4[1] >= 64 &&
		ipv4[1] <= 127
}
