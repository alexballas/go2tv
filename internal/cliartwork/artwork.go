package cliartwork

import (
	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/metadata"
)

// Prepare resolves local media artwork and registers it before server start.
func Prepare(server *httphandlers.HTTPserver, mediaPath, listenAddress string, local bool) *metadata.Artwork {
	if !local {
		return nil
	}

	asset, err := metadata.ResolveArtwork(mediaPath)
	if err != nil || asset == nil {
		return nil
	}

	server.AddStaticHandler(asset.HandlerPath(), asset.MIMEType, asset.Data)
	return &metadata.Artwork{
		URL:      "http://" + listenAddress + asset.HandlerPath(),
		MIMEType: asset.MIMEType,
		Width:    asset.Width,
		Height:   asset.Height,
	}
}
