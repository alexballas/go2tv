package gui

import (
	"path/filepath"
	"strings"

	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/metadata"
)

type artworkCacheEntry struct {
	asset *metadata.ArtworkAsset
}

func guiArtworkIdentity(mediaPath string) string {
	absPath, err := filepath.Abs(mediaPath)
	if err == nil {
		return filepath.Clean(absPath)
	}
	return filepath.Clean(mediaPath)
}

func resolveGUIArtwork(mediaPath, mediaType string, local bool) *metadata.ArtworkAsset {
	if !local || !strings.HasPrefix(strings.ToLower(mediaType), "audio/") {
		return nil
	}

	asset, err := metadata.ResolveArtwork(mediaPath)
	if err != nil {
		return nil
	}
	return asset
}

func (s *FyneScreen) cachedGUIArtwork(identity string, resolve func() *metadata.ArtworkAsset) *metadata.ArtworkAsset {
	if identity == "" {
		return nil
	}

	s.mu.RLock()
	entry, ok := s.artworkCache[identity]
	s.mu.RUnlock()
	if ok {
		return entry.asset
	}

	asset := resolve()
	s.mu.Lock()
	if entry, ok = s.artworkCache[identity]; ok {
		asset = entry.asset
	} else {
		if s.artworkCache == nil {
			s.artworkCache = make(map[string]artworkCacheEntry)
		}
		s.artworkCache[identity] = artworkCacheEntry{asset: asset}
	}
	s.mu.Unlock()
	return asset
}

func (s *FyneScreen) resolveCachedGUIArtwork(mediaPath, mediaType string, local bool) (string, *metadata.ArtworkAsset) {
	if !local {
		return "", nil
	}

	identity := guiArtworkIdentity(mediaPath)
	asset := s.cachedGUIArtwork(identity, func() *metadata.ArtworkAsset {
		return resolveGUIArtwork(mediaPath, mediaType, true)
	})
	return identity, asset
}

func (s *FyneScreen) setCurrentArtworkTarget(identity string) {
	s.mu.Lock()
	if s.currentArtworkIdentity != identity {
		s.currentArtwork = nil
	}
	s.currentArtworkIdentity = identity
	s.mu.Unlock()
}

func (s *FyneScreen) ensureCurrentArtworkTarget(identity string) {
	s.mu.Lock()
	if s.currentArtworkIdentity == "" {
		s.currentArtworkIdentity = identity
		s.currentArtwork = nil
	}
	s.mu.Unlock()
}

func (s *FyneScreen) setResolvedCurrentArtwork(identity string, asset *metadata.ArtworkAsset) {
	s.mu.Lock()
	if s.currentArtworkIdentity == identity {
		s.currentArtwork = asset
	}
	s.mu.Unlock()
}

func (s *FyneScreen) resolveCurrentGUIArtwork(mediaPath, mediaType string, local bool) *metadata.ArtworkAsset {
	if !local {
		s.setCurrentArtwork(nil)
		return nil
	}

	identity := guiArtworkIdentity(mediaPath)
	s.ensureCurrentArtworkTarget(identity)
	_, asset := s.resolveCachedGUIArtwork(mediaPath, mediaType, true)
	s.setResolvedCurrentArtwork(identity, asset)
	return asset
}

func (s *FyneScreen) setCurrentArtwork(asset *metadata.ArtworkAsset) {
	s.mu.Lock()
	s.currentArtwork = asset
	s.currentArtworkIdentity = ""
	s.mu.Unlock()
}

func (s *FyneScreen) getCurrentArtwork() *metadata.ArtworkAsset {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentArtwork
}

func registerGUIArtwork(server *httphandlers.HTTPserver, asset *metadata.ArtworkAsset) {
	if server == nil || asset == nil {
		return
	}
	server.AddStaticHandler(asset.HandlerPath(), asset.MIMEType, asset.Data)
}

func guiMediaMetadata(title, listenAddress string, asset *metadata.ArtworkAsset) metadata.Media {
	mediaMetadata := metadata.Media{Title: title}
	if asset == nil {
		return mediaMetadata
	}

	mediaMetadata.Artwork = &metadata.Artwork{
		URL:      "http://" + listenAddress + asset.HandlerPath(),
		MIMEType: asset.MIMEType,
		Width:    asset.Width,
		Height:   asset.Height,
	}
	return mediaMetadata
}
