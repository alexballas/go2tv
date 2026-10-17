package gui

import (
	"context"
	"sync"
	"time"

	"go2tv.app/go2tv/v2/internal/radiobrowser"
	"go2tv.app/go2tv/v2/metadata"
)

const radioArtworkRetryDelay = 30 * time.Second

type radioArtworkEntry struct {
	asset   *metadata.ArtworkAsset
	err     error
	retryAt time.Time
}

// Shared across station rows and playback. Failures expire, concurrent callers
// share fetches, and a canceled caller never poisons the next request.
type radioArtworkCache struct {
	mu      sync.Mutex
	entries map[string]radioArtworkEntry
	order   []string
	pending map[string]chan struct{}
	slots   chan struct{}
	now     func() time.Time
}

func newRadioArtworkCache() *radioArtworkCache {
	return &radioArtworkCache{
		entries: make(map[string]radioArtworkEntry), pending: make(map[string]chan struct{}),
		slots: make(chan struct{}, 4), now: time.Now,
	}
}

func (s *FyneScreen) getRadioArtworkCache() *radioArtworkCache {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.radioArtworkCache == nil {
		s.radioArtworkCache = newRadioArtworkCache()
	}
	return s.radioArtworkCache
}

func (c *radioArtworkCache) lookup(url string) (*metadata.ArtworkAsset, error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lookupLocked(url)
}

func (c *radioArtworkCache) lookupLocked(url string) (*metadata.ArtworkAsset, error, bool) {
	entry, exists := c.entries[url]
	if !exists || (entry.err != nil && !c.now().Before(entry.retryAt)) {
		return nil, nil, false
	}
	return entry.asset, entry.err, true
}

func (c *radioArtworkCache) fetch(ctx context.Context, url string) (*metadata.ArtworkAsset, error) {
	if url == "" {
		return nil, nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c.mu.Lock()
		if asset, err, cached := c.lookupLocked(url); cached {
			c.mu.Unlock()
			return asset, err
		}
		if done := c.pending[url]; done != nil {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
				continue
			}
		}
		done := make(chan struct{})
		c.pending[url] = done
		c.mu.Unlock()

		var asset *metadata.ArtworkAsset
		var err error
		select {
		case c.slots <- struct{}{}:
			asset, err = radiobrowser.FetchIcon(ctx, url)
			<-c.slots
		case <-ctx.Done():
			err = ctx.Err()
		}
		c.mu.Lock()
		if ctx.Err() == nil {
			c.storeLocked(url, asset, err)
		}
		delete(c.pending, url)
		close(done)
		c.mu.Unlock()
		return asset, err
	}
}

func (c *radioArtworkCache) storeLocked(url string, asset *metadata.ArtworkAsset, err error) {
	if _, exists := c.entries[url]; !exists {
		if len(c.order) >= maxRadioIconCacheEntries {
			delete(c.entries, c.order[0])
			c.order = c.order[1:]
		}
		c.order = append(c.order, url)
	}
	entry := radioArtworkEntry{asset: asset, err: err}
	if err != nil {
		entry.retryAt = c.now().Add(radioArtworkRetryDelay)
	}
	c.entries[url] = entry
}
