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
