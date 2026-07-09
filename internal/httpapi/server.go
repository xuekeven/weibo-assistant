package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/xuekeven/weibo-assistant/internal/config"
	"github.com/xuekeven/weibo-assistant/internal/notifier"
	"github.com/xuekeven/weibo-assistant/internal/store"
)

type Server struct {
	cfg      config.Config
	store    *store.Store
	notifier *notifier.WeworkClient
	mux      *http.ServeMux
}

func NewServer(cfg config.Config, st *store.Store, notify *notifier.WeworkClient) *Server {
	s := &Server{
		cfg:      cfg,
		store:    st,
		notifier: notify,
		mux:      http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) Addr() string {
	return ":" + strconv.Itoa(s.cfg.BackendPort)
}

func (s *Server) Handler() http.Handler {
	return s.withCORS(s.mux)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /api/groups", s.handleListGroups)
	s.mux.HandleFunc("GET /api/users", s.handleListUsers)
	s.mux.HandleFunc("GET /api/messages", s.handleListMessages)
	s.mux.HandleFunc("POST /api/messages", s.handleCreateMessage)
	s.mux.HandleFunc("GET /api/groups/{id}/messages", s.handleListGroupMessages)
	s.mux.HandleFunc("GET /api/notification-rules", s.handleListNotificationRules)
	s.mux.HandleFunc("POST /api/notification-rules", s.handleCreateNotificationRule)
	s.mux.HandleFunc("PATCH /api/notification-rules/{id}", s.handleUpdateNotificationRule)
	s.mux.HandleFunc("GET /api/notification-events", s.handleListNotificationEvents)
	s.mux.HandleFunc("POST /api/notification-events", s.handleCreateNotificationEvent)
	s.mux.HandleFunc("GET /api/monitor/status", s.handleMonitorStatus)
	s.mux.HandleFunc("GET /api/notify/probe", s.handleNotifyProbe)
	s.mux.HandleFunc("POST /api/dev/messages", s.handleCreateDevMessage)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"time":   time.Now().Format(time.RFC3339),
	})
}

func (s *Server) handleMonitorStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.store.GetMonitorStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
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
		origin := r.Header.Get("Origin")
		if s.isAllowedOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
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
	return parsed.Port() == strconv.Itoa(s.cfg.FrontendPort)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("write json failed", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{
		"error": err.Error(),
	})
}
