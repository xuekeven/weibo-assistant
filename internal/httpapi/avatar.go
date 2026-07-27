package httpapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	maxAvatarURLLength = 2048
	maxAvatarBytes     = 5 << 20
)

func newAvatarHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 8
	transport.ResponseHeaderTimeout = 8 * time.Second
	return &http.Client{
		Transport:     transport,
		Timeout:       10 * time.Second,
		CheckRedirect: avatarRedirectPolicy,
	}
}

func avatarRedirectPolicy(request *http.Request, via []*http.Request) error {
	if len(via) >= 3 {
		return errors.New("too many avatar redirects")
	}
	_, err := validatedAvatarURL(request.URL.String())
	return err
}

func validatedAvatarURL(rawURL string) (*url.URL, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, errors.New("avatar URL is required")
	}
	if len(rawURL) > maxAvatarURLLength {
		return nil, errors.New("avatar URL is too long")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, errors.New("invalid avatar URL")
	}
	if parsed.Scheme != "https" || parsed.User != nil {
		return nil, errors.New("avatar URL must use HTTPS without credentials")
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return nil, errors.New("avatar URL uses an unsupported port")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host != "sinaimg.cn" && !strings.HasSuffix(host, ".sinaimg.cn") {
		return nil, errors.New("avatar host is not allowed")
	}
	parsed.Fragment = ""
	return parsed, nil
}

func (s *Server) handleAvatar(w http.ResponseWriter, r *http.Request) {
	avatarURL, err := validatedAvatarURL(r.URL.Query().Get("url"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, avatarURL.String(), nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid avatar URL"))
		return
	}
	request.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,*/*;q=0.8")
	request.Header.Set("User-Agent", "Mozilla/5.0 (compatible; weibo-assistant/1.0)")

	response, err := s.avatarClient.Do(request)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("fetch avatar: %w", err))
		return
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		writeError(w, http.StatusBadGateway, fmt.Errorf("fetch avatar: upstream status %d", response.StatusCode))
		return
	}
	if response.ContentLength > maxAvatarBytes {
		writeError(w, http.StatusBadGateway, errors.New("fetch avatar: response is too large"))
		return
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxAvatarBytes+1))
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("read avatar: %w", err))
		return
	}
	if len(body) > maxAvatarBytes {
		writeError(w, http.StatusBadGateway, errors.New("fetch avatar: response is too large"))
		return
	}
	contentType := http.DetectContentType(body)
	if !strings.HasPrefix(contentType, "image/") {
		writeError(w, http.StatusBadGateway, errors.New("fetch avatar: upstream did not return an image"))
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=86400, stale-if-error=604800")
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
