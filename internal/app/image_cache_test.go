package app

import (
	"testing"
	"time"
)

func TestImageURLCacheExpiresAndEvicts(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	cache := newImageURLCache(time.Minute, 2)
	cache.now = func() time.Time { return now }

	cache.Set("fid-1", "https://example.com/1.jpg")
	now = now.Add(time.Second)
	cache.Set("fid-2", "https://example.com/2.jpg")
	now = now.Add(time.Second)
	cache.Set("fid-3", "https://example.com/3.jpg")

	if _, ok := cache.Get("fid-1"); ok {
		t.Fatal("expected oldest entry to be evicted")
	}
	if got, ok := cache.Get("fid-3"); !ok || got != "https://example.com/3.jpg" {
		t.Fatalf("unexpected cached image: %q, %v", got, ok)
	}

	now = now.Add(2 * time.Minute)
	if cache.Len() != 0 {
		t.Fatalf("expected expired cache to be empty, got %d", cache.Len())
	}
}
