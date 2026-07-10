package gui

import (
	"strings"

	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/metadata"
)

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

func (s *FyneScreen) setCurrentArtwork(asset *metadata.ArtworkAsset) {
	s.mu.Lock()
	s.currentArtwork = asset
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
