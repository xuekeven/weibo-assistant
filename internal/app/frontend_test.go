package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFrontendFileHandler(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>weibo assistant</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler, err := frontendFileHandler(dir)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "weibo assistant") {
		t.Fatalf("unexpected response: status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestUnifiedHTTPHandlerRoutesAPIAndFrontend(t *testing.T) {
	api := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("api"))
	})
	frontend := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("frontend"))
	})
	handler := unifiedHTTPHandler(api, frontend)

	for _, test := range []struct {
		path string
		want string
	}{
		{path: "/api/groups", want: "api"},
		{path: "/healthz", want: "api"},
		{path: "/assets/app.js", want: "frontend"},
		{path: "/", want: "frontend"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Body.String() != test.want {
			t.Fatalf("path %s: got %q, want %q", test.path, response.Body.String(), test.want)
		}
	}
}
