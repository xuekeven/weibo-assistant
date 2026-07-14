package notifier

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestSendTextWithIdempotencyHeader(t *testing.T) {
	client := NewWeworkClient("http://wework.test", "secret", "")
	client.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if got := request.Header.Get("Idempotency-Key"); got != "weibo-outbox-42" {
			t.Fatalf("Idempotency-Key = %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"errcode":0,"errmsg":"ok"}`)),
			Request:    request,
		}, nil
	})

	if _, err := client.SendTextWithIdempotency(context.Background(), "hello", "weibo-outbox-42"); err != nil {
		t.Fatalf("send text: %v", err)
	}
}
