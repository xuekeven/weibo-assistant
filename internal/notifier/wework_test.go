package notifier

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"runtime"
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
		var payload map[string]string
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode text payload: %v", err)
		}
		if want := environmentTextPrefix(runtime.GOOS) + "hello"; payload["content"] != want {
			t.Fatalf("content = %q, want %q", payload["content"], want)
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

func TestEnvironmentTextPrefix(t *testing.T) {
	for _, test := range []struct {
		goos string
		want string
	}{
		{goos: "darwin", want: "【开发】"},
		{goos: "linux", want: "【生产】"},
		{goos: "windows", want: ""},
	} {
		if got := environmentTextPrefix(test.goos); got != test.want {
			t.Fatalf("environmentTextPrefix(%q) = %q, want %q", test.goos, got, test.want)
		}
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
