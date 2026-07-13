package controller

import (
	"container/list"
	"context"
	"errors"
	"sync"
)

type ArtworkLoader func(context.Context) ([]byte, string, error)

type ArtworkValue struct {
	Data []byte
	MIME string
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

// ArtworkCache is a byte-bounded LRU with per-ID load coalescing.
type ArtworkCache struct {
	mu       sync.Mutex
	limit    int64
	used     int64
	items    map[string]*list.Element
	lru      *list.List
	inflight map[string]*artworkCall
}

func NewArtworkCache(limit int64) *ArtworkCache {
	if limit <= 0 {
		limit = ArtworkCacheBytes
	}
	return &ArtworkCache{limit: limit, items: make(map[string]*list.Element), lru: list.New(), inflight: make(map[string]*artworkCall)}
}

func (c *ArtworkCache) Get(ctx context.Context, id string, loader ArtworkLoader) (ArtworkValue, error) {
	if c == nil || loader == nil || id == "" {
		return ArtworkValue{}, errors.New("artwork cache input invalid")
	}
	c.mu.Lock()
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

	data, mime, err := loader(ctx)
	value := ArtworkValue{Data: append([]byte(nil), data...), MIME: mime}
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

func cloneArtwork(value ArtworkValue) ArtworkValue {
	value.Data = append([]byte(nil), value.Data...)
	return value
}
