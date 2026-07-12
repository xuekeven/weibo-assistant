package app

import (
	"strings"
	"sync"
	"time"
)

type imageURLCacheEntry struct {
	url       string
	expiresAt time.Time
	updatedAt time.Time
}

type imageURLCache struct {
	mu       sync.Mutex
	items    map[string]imageURLCacheEntry
	ttl      time.Duration
	maxItems int
	now      func() time.Time
}

func newImageURLCache(ttl time.Duration, maxItems int) *imageURLCache {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if maxItems <= 0 {
		maxItems = 1000
	}
	return &imageURLCache{
		items:    make(map[string]imageURLCacheEntry),
		ttl:      ttl,
		maxItems: maxItems,
		now:      time.Now,
	}
}

func (c *imageURLCache) Set(fid string, imageURL string) {
	if c == nil {
		return
	}
	fid = strings.TrimSpace(fid)
	imageURL = strings.TrimSpace(imageURL)
	if fid == "" || imageURL == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.removeExpiredLocked(now)
	if _, exists := c.items[fid]; !exists && len(c.items) >= c.maxItems {
		c.removeOldestLocked()
	}
	c.items[fid] = imageURLCacheEntry{
		url:       imageURL,
		expiresAt: now.Add(c.ttl),
		updatedAt: now,
	}
}

func (c *imageURLCache) Get(fid string) (string, bool) {
	if c == nil {
		return "", false
	}
	fid = strings.TrimSpace(fid)
	if fid == "" {
		return "", false
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.items[fid]
	if !ok {
		return "", false
	}
	if !entry.expiresAt.After(c.now()) {
		delete(c.items, fid)
		return "", false
	}
	return entry.url, true
}

func (c *imageURLCache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeExpiredLocked(c.now())
	return len(c.items)
}

func (c *imageURLCache) removeExpiredLocked(now time.Time) {
	for fid, entry := range c.items {
		if !entry.expiresAt.After(now) {
			delete(c.items, fid)
		}
	}
}

func (c *imageURLCache) removeOldestLocked() {
	oldestFID := ""
	var oldestTime time.Time
	for fid, entry := range c.items {
		if oldestFID == "" || entry.updatedAt.Before(oldestTime) {
			oldestFID = fid
			oldestTime = entry.updatedAt
		}
	}
	if oldestFID != "" {
		delete(c.items, oldestFID)
	}
}
