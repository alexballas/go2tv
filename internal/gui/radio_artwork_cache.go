package gui

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/alexballas/refyne/v2"

	"go2tv.app/go2tv/v2/internal/mediaartwork"
	"go2tv.app/go2tv/v2/internal/radiobrowser"
	"go2tv.app/go2tv/v2/metadata"
)

const (
	radioArtworkRetryDelay = 30 * time.Second
	// Station logos rarely change; an expired one still serves when its
	// refresh fails, and unused ones are pruned after radioIconPruneAge.
	radioIconTTL      = 30 * 24 * time.Hour
	radioIconPruneAge = 3 * radioIconTTL
)

type radioArtworkEntry struct {
	asset   *metadata.ArtworkAsset
	err     error
	retryAt time.Time
}

// Shared across station rows and playback. Failures expire, concurrent callers
// share fetches, and a canceled caller never poisons the next request. With a
// dir, logos also persist across restarts.
type radioArtworkCache struct {
	mu      sync.Mutex
	entries map[string]radioArtworkEntry
	order   []string
	pending map[string]chan struct{}
	slots   chan struct{}
	now     func() time.Time
	dir     string
	prune   sync.Once
}

func newRadioArtworkCache(dir string) *radioArtworkCache {
	return &radioArtworkCache{
		entries: make(map[string]radioArtworkEntry), pending: make(map[string]chan struct{}),
		slots: make(chan struct{}, 4), now: time.Now, dir: dir,
	}
}

// radioIconStoreDir keeps logos in the app storage root, e.g.
// ~/Library/Preferences/fyne/app.go2tv.go2tv on macOS.
func radioIconStoreDir(app fyne.App) string {
	root := app.Storage().RootURI()
	if root == nil || root.Scheme() != "file" || root.Path() == "" {
		return ""
	}
	return filepath.Join(root.Path(), "radio-icons")
}

func (s *FyneScreen) getRadioArtworkCache() *radioArtworkCache {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.radioArtworkCache == nil {
		s.radioArtworkCache = newRadioArtworkCache(s.radioIconDir)
	}
	return s.radioArtworkCache
}

func (c *radioArtworkCache) lookup(source radiobrowser.IconSource) (*metadata.ArtworkAsset, error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lookupLocked(source.Key())
}

func (c *radioArtworkCache) lookupLocked(key string) (*metadata.ArtworkAsset, error, bool) {
	entry, exists := c.entries[key]
	if !exists || (entry.err != nil && !c.now().Before(entry.retryAt)) {
		return nil, nil, false
	}
	return entry.asset, entry.err, true
}

func (c *radioArtworkCache) fetch(ctx context.Context, source radiobrowser.IconSource) (*metadata.ArtworkAsset, error) {
	if source.Empty() {
		return nil, nil
	}
	key := source.Key()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c.mu.Lock()
		if asset, err, cached := c.lookupLocked(key); cached {
			c.mu.Unlock()
			return asset, err
		}
		if done := c.pending[key]; done != nil {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
				continue
			}
		}
		done := make(chan struct{})
		c.pending[key] = done
		c.mu.Unlock()

		var asset *metadata.ArtworkAsset
		var err error
		select {
		case c.slots <- struct{}{}:
			asset, err = c.load(ctx, source)
			<-c.slots
		case <-ctx.Done():
			err = ctx.Err()
		}
		c.mu.Lock()
		if ctx.Err() == nil {
			c.storeLocked(key, asset, err)
		}
		delete(c.pending, key)
		close(done)
		c.mu.Unlock()
		return asset, err
	}
}

func (c *radioArtworkCache) storeLocked(key string, asset *metadata.ArtworkAsset, err error) {
	if _, exists := c.entries[key]; !exists {
		if len(c.order) >= maxRadioIconCacheEntries {
			delete(c.entries, c.order[0])
			c.order = c.order[1:]
		}
		c.order = append(c.order, key)
	}
	entry := radioArtworkEntry{asset: asset, err: err}
	if err != nil {
		entry.retryAt = c.now().Add(radioArtworkRetryDelay)
	}
	c.entries[key] = entry
}

// load serves a fresh disk copy before the network, and an expired one when
// its refresh fails.
func (c *radioArtworkCache) load(ctx context.Context, source radiobrowser.IconSource) (*metadata.ArtworkAsset, error) {
	if c.dir == "" {
		return radiobrowser.FetchIcon(ctx, source)
	}
	c.prune.Do(func() { go c.pruneIcons() })
	sum := sha256.Sum256([]byte(source.Key()))
	path := filepath.Join(c.dir, hex.EncodeToString(sum[:16])+".jpg")
	stored, fresh := c.readIcon(path, source)
	if fresh {
		return stored, nil
	}
	asset, err := radiobrowser.FetchIcon(ctx, source)
	switch {
	case err == nil:
		writeRadioIcon(path, asset.Data)
	case stored != nil && ctx.Err() == nil:
		return stored, nil
	}
	return asset, err
}

func (c *radioArtworkCache) readIcon(path string, source radiobrowser.IconSource) (*metadata.ArtworkAsset, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	asset, err := mediaartwork.AssetFromJPEG(data, cmp.Or(source.Favicon, source.Homepage))
	if err != nil {
		os.Remove(path)
		return nil, false
	}
	return asset, c.now().Sub(info.ModTime()) < radioIconTTL
}

// writeRadioIcon renames into place so readers never see a partial logo.
func writeRadioIcon(path string, data []byte) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".icon-*")
	if err != nil {
		return
	}
	_, err = file.Write(data)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(file.Name(), path)
	}
	if err != nil {
		os.Remove(file.Name())
	}
}

func (c *radioArtworkCache) pruneIcons() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if info, err := entry.Info(); err == nil && c.now().Sub(info.ModTime()) > radioIconPruneAge {
			os.Remove(filepath.Join(c.dir, entry.Name()))
		}
	}
}
