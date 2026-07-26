package app

import (
	neturl "net/url"
	"strings"
)

func mediaURLLogValue(rawURL string) string {
	parsed, err := neturl.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return "<invalid-url>"
	}
	return parsed.Host + parsed.EscapedPath()
}
