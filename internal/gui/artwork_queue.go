//go:build !(android || ios)

package gui

import (
	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/metadata"
)

func sameGUIArtwork(a, b *metadata.ArtworkAsset) bool {
	return a != nil && b != nil && a.HandlerPath() == b.HandlerPath()
}

func removeGUIArtworkHandler(server *httphandlers.HTTPserver, asset *metadata.ArtworkAsset, keep ...*metadata.ArtworkAsset) {
	if server == nil || asset == nil {
		return
	}
	for _, candidate := range keep {
		if sameGUIArtwork(asset, candidate) {
			return
		}
	}
	server.RemoveHandler(asset.HandlerPath())
}

func (s *FyneScreen) queuedArtworkSnapshot() (string, *metadata.ArtworkAsset) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.queuedArtworkIdentity, s.queuedArtwork
}

func (s *FyneScreen) commitQueuedArtwork(identity string, asset *metadata.ArtworkAsset) (*metadata.ArtworkAsset, *metadata.ArtworkAsset) {
	s.mu.Lock()
	oldQueued := s.queuedArtwork
	current := s.currentArtwork
	s.queuedArtworkIdentity = identity
	s.queuedArtwork = asset
	s.mu.Unlock()
	return oldQueued, current
}

func (s *FyneScreen) clearQueuedArtwork(server *httphandlers.HTTPserver) {
	s.mu.Lock()
	queued := s.queuedArtwork
	current := s.currentArtwork
	s.queuedArtworkIdentity = ""
	s.queuedArtwork = nil
	s.mu.Unlock()
	removeGUIArtworkHandler(server, queued, current)
}

func (s *FyneScreen) resetQueuedArtworkState() {
	s.mu.Lock()
	s.queuedArtworkIdentity = ""
	s.queuedArtwork = nil
	s.mu.Unlock()
}

func (s *FyneScreen) promoteQueuedArtwork(identity string, server *httphandlers.HTTPserver) {
	s.mu.Lock()
	oldCurrent := s.currentArtwork
	next := s.queuedArtwork
	if s.queuedArtworkIdentity != identity {
		if entry, ok := s.artworkCache[identity]; ok {
			next = entry.asset
		} else {
			next = nil
		}
	}
	s.currentArtworkIdentity = identity
	s.currentArtwork = next
	s.queuedArtworkIdentity = ""
	s.queuedArtwork = nil
	s.mu.Unlock()
	removeGUIArtworkHandler(server, oldCurrent, next)
}
