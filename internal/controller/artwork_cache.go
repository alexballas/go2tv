package controller

import (
	"container/list"
	"context"
	"fmt"
	"mime"
	"strings"
	"sync"
)

// ArtworkLoader returns artwork bytes and their MIME type. The returned slice
// remains caller-owned; ArtworkCache copies it before retaining or returning it.
type ArtworkLoader func(context.Context) ([]byte, string, error)

// ArtworkValue is an immutable-by-convention, serialization-safe cache value.
// Cache methods return independent Data slices.
type ArtworkValue struct {
	Data []byte `json:"Data"`
	MIME string `json:"MIME"`
}

// Validate requires non-empty image data and a syntactically valid image MIME.
func (v ArtworkValue) Validate() error {
	if len(v.Data) == 0 {
		return fmt.Errorf("data: %w", ErrInvalidArtwork)
	}
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(v.MIME))
	if err != nil || !strings.HasPrefix(strings.ToLower(mediaType), "image/") {
		return fmt.Errorf("MIME type: %w", ErrInvalidArtwork)
	}
	return nil
}

type artworkEntry struct {
	id    string
	value ArtworkValue
	size  int64
}

type artworkCall struct {
	done  chan struct{}
	value ArtworkValue
	err   error
}

// ArtworkCache is a concurrency-safe, byte-bounded LRU with per-ID load
// coalescing. Its zero value is ready to use with ArtworkCacheBytes as its
// limit. Values larger than the limit are returned but not retained.
type ArtworkCache struct {
	mu       sync.Mutex
	limit    int64
	used     int64
	items    map[string]*list.Element
	lru      *list.List
	inflight map[string]*artworkCall
}

// NewArtworkCache creates an artwork cache. A non-positive limit selects
// ArtworkCacheBytes.
func NewArtworkCache(limit int64) *ArtworkCache {
	if limit <= 0 {
		limit = ArtworkCacheBytes
	}
	return &ArtworkCache{limit: limit, items: make(map[string]*list.Element), lru: list.New(), inflight: make(map[string]*artworkCall)}
}

// Lookup returns an independent copy of cached artwork without invoking a
// loader. A blank ID, nil cache, or miss returns false.
func (c *ArtworkCache) Lookup(id string) (ArtworkValue, bool) {
	if c == nil || strings.TrimSpace(id) == "" {
		return ArtworkValue{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	elem := c.items[id]
	if elem == nil {
		return ArtworkValue{}, false
	}
	c.lru.MoveToFront(elem)
	return cloneArtwork(elem.Value.(*artworkEntry).value), true
}

// Get returns cached artwork or invokes loader. Concurrent misses for the same
// ID share the first loader call. Each waiter may cancel only its own wait; the
// first caller's context controls the shared loader call.
func (c *ArtworkCache) Get(ctx context.Context, id string, loader ArtworkLoader) (ArtworkValue, error) {
	if c == nil || ctx == nil || loader == nil || strings.TrimSpace(id) == "" {
		return ArtworkValue{}, ErrInvalidOperation
	}
	if err := ctx.Err(); err != nil {
		return ArtworkValue{}, err
	}
	c.mu.Lock()
	c.initLocked()
	if elem := c.items[id]; elem != nil {
		c.lru.MoveToFront(elem)
		value := cloneArtwork(elem.Value.(*artworkEntry).value)
		c.mu.Unlock()
		return value, nil
	}
	if call := c.inflight[id]; call != nil {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return ArtworkValue{}, ctx.Err()
		case <-call.done:
			return cloneArtwork(call.value), call.err
		}
	}
	call := &artworkCall{done: make(chan struct{})}
	c.inflight[id] = call
	c.mu.Unlock()

	data, mimeType, err := loader(ctx)
	value := ArtworkValue{Data: append([]byte(nil), data...), MIME: mimeType}
	if err == nil {
		err = value.Validate()
	}
	c.mu.Lock()
	delete(c.inflight, id)
	if err == nil && int64(len(value.Data)) <= c.limit {
		entry := &artworkEntry{id: id, value: cloneArtwork(value), size: int64(len(value.Data))}
		c.items[id] = c.lru.PushFront(entry)
		c.used += entry.size
		for c.used > c.limit {
			last := c.lru.Back()
			old := last.Value.(*artworkEntry)
			delete(c.items, old.id)
			c.used -= old.size
			c.lru.Remove(last)
		}
	}
	call.value, call.err = cloneArtwork(value), err
	close(call.done)
	c.mu.Unlock()
	return value, err
}

func (c *ArtworkCache) initLocked() {
	if c.limit <= 0 {
		c.limit = ArtworkCacheBytes
	}
	if c.items == nil {
		c.items = make(map[string]*list.Element)
	}
	if c.lru == nil {
		c.lru = list.New()
	}
	if c.inflight == nil {
		c.inflight = make(map[string]*artworkCall)
	}
}

func cloneArtwork(value ArtworkValue) ArtworkValue {
	value.Data = append([]byte(nil), value.Data...)
	return value
}
