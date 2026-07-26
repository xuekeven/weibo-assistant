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

func TestSendImageBytesUsesRelayMultipartContract(t *testing.T) {
	client := NewWeworkClient("http://wework.test", "secret", "XueKai")
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x01}
	client.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if got := request.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("Authorization = %q", got)
		}
		reader, err := request.MultipartReader()
		if err != nil {
			t.Fatalf("MultipartReader: %v", err)
		}

		var mediaFound bool
		var touserFound bool
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("NextPart: %v", err)
			}
			body, err := io.ReadAll(part)
			if err != nil {
				t.Fatalf("read part: %v", err)
			}
			switch part.FormName() {
			case "media":
				mediaFound = true
				if part.FileName() != "weibo-login.png" {
					t.Fatalf("filename = %q", part.FileName())
				}
				if part.Header.Get("Content-Type") != "image/png" {
					t.Fatalf("media Content-Type = %q", part.Header.Get("Content-Type"))
				}
				if string(body) != string(png) {
					t.Fatalf("unexpected media bytes: %v", body)
				}
			case "touser":
				touserFound = true
				if string(body) != "XueKai" {
					t.Fatalf("touser = %q", string(body))
				}
			}
		}
		if !mediaFound || !touserFound {
			t.Fatalf("multipart fields media=%v touser=%v", mediaFound, touserFound)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"errcode":0,"errmsg":"ok","media_id":"media-1"}`)),
			Request:    request,
		}, nil
	})

	response, err := client.SendImageBytes(context.Background(), "weibo-login.png", png)
	if err != nil {
		t.Fatalf("send image: %v", err)
	}
	if response.MediaID != "media-1" {
		t.Fatalf("MediaID = %q", response.MediaID)
	}
}
