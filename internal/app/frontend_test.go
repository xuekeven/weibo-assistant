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
