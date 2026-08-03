package browser

import (
	"net/url"
	"strings"
	"testing"
)

func TestImageMetadataRequestURL(t *testing.T) {
	rawURL := imageMetadataRequestURL("5322532208255108", "__callback")
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "https" || parsed.Host != "upload.api.weibo.com" || parsed.Path != "/2/mss/meta_query.json" {
		t.Fatalf("unexpected metadata URL: %s", rawURL)
	}
	query := parsed.Query()
	if query.Get("fid") != "5322532208255108" || query.Get("source") != "209678993" || query.Get("callback") != "__callback" {
		t.Fatalf("unexpected metadata query: %v", query)
	}
	if query.Get("replace") != "false" {
		t.Fatalf("replace = %q, want false", query.Get("replace"))
	}
}

func TestImageMetadataCallbackName(t *testing.T) {
	first, err := imageMetadataCallbackName()
	if err != nil {
		t.Fatal(err)
	}
	second, err := imageMetadataCallbackName()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first, "__weiboAssistantImage_") || first == second {
		t.Fatalf("unexpected callback names: %q %q", first, second)
	}
}

func TestNewImageMetadataHTTPErrorIncludesWeiboError(t *testing.T) {
	err := newImageMetadataHTTPError(403, "Forbidden", `{"error":"auth failed! auth by Null spi!","error_code":40302}`)
	if got := err.Error(); got != "weibo image metadata returned HTTP 403 (error 40302): auth failed! auth by Null spi!" {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestNewImageMetadataHTTPErrorFallsBackToStatus(t *testing.T) {
	err := newImageMetadataHTTPError(503, "Service Unavailable", "not json")
	if got := err.Error(); got != "weibo image metadata returned HTTP 503 Service Unavailable" {
		t.Fatalf("unexpected error: %s", got)
	}
}
