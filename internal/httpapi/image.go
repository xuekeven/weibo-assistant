package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
)

const maxMessageImageBytes int64 = 25 << 20

func validatedMessageImageURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil {
		return nil, errors.New("invalid Weibo image URL")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host != "upload.api.weibo.com" || (parsed.Path != "/2/mss/msget" && parsed.Path != "/2/mss/msget_thumbnail") {
		return nil, errors.New("Weibo image URL is not allowed")
	}
	if parsed.Port() != "" && parsed.Port() != "443" {
		return nil, errors.New("unsupported image port")
	}
	parsed.Fragment = ""
	return parsed, nil
}

func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	imageURL, err := validatedMessageImageURL(r.URL.Query().Get("url"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	data, contentType, err := s.imageFetcher(r.Context(), s.cfg.ChromeDebugPort, imageURL.String(), maxMessageImageBytes)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400, stale-if-error=604800")
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
