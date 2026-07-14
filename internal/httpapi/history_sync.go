package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/xuekeven/weibo-assistant/internal/store"
)

type createHistorySyncRequest struct {
	From  *time.Time `json:"from"`
	To    *time.Time `json:"to"`
	Pages int        `json:"pages"`
}

func (s *Server) handleCreateHistorySync(w http.ResponseWriter, r *http.Request) {
	groupID, err := pathInt64(r, "/api/groups/", "/history-sync")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if _, err := s.store.GetGroupByID(r.Context(), groupID); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, pgx.ErrNoRows) {
			status = http.StatusNotFound
		}
		writeError(w, status, err)
		return
	}
	var request createHistorySyncRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	maxPages := request.Pages
	if maxPages <= 0 {
		maxPages = 1000
	}
	if maxPages > 1000 {
		writeError(w, http.StatusBadRequest, errors.New("pages must be between 1 and 1000"))
		return
	}
	job, err := s.store.EnqueueHistorySync(r.Context(), store.EnqueueHistorySyncParams{
		GroupID:       groupID,
		Mode:          "manual",
		RequestedFrom: request.From,
		RequestedTo:   request.To,
		MaxPages:      maxPages,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) handleGetHistorySync(w http.ResponseWriter, r *http.Request) {
	groupID, err := pathInt64(r, "/api/groups/", "/history-sync")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	job, err := s.store.GetLatestHistorySyncForGroup(r.Context(), groupID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, map[string]any{"job": nil})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job})
}
