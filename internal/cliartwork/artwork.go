package cliartwork

import (
	"io"
	"os"

	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/metadata"
)

const maxArtworkInputSize = 20 << 20

// Prepare resolves local media artwork and registers it before server start.
func Prepare(server *httphandlers.HTTPserver, mediaPath, listenAddress string, local bool) *metadata.Artwork {
	return PrepareWithOverride(server, mediaPath, "", listenAddress, local)
}

// PrepareWithOverride prefers explicit artwork, then automatic local discovery.
func PrepareWithOverride(server *httphandlers.HTTPserver, mediaPath, overridePath, listenAddress string, local bool) *metadata.Artwork {
	return Register(server, Resolve(mediaPath, overridePath, local), listenAddress)
}

// Resolve prefers a valid explicit artwork file, then automatic local discovery.
func Resolve(mediaPath, overridePath string, local bool) *metadata.ArtworkAsset {
	if overridePath != "" {
		if asset := loadExplicit(overridePath); asset != nil {
			return asset
		}
	}

	if !local {
		return nil
	}

	asset, err := metadata.ResolveArtwork(mediaPath)
	if err != nil {
		return nil
	}
	return asset
}

func loadExplicit(path string) *metadata.ArtworkAsset {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, maxArtworkInputSize+1))
	if err != nil {
		return nil
	}
	asset, err := metadata.LoadArtwork(data, path)
	if err != nil {
		return nil
	}
	return asset
}

// Register adds artwork to the server and returns receiver-facing metadata.
func Register(server *httphandlers.HTTPserver, asset *metadata.ArtworkAsset, listenAddress string) *metadata.Artwork {
	if asset == nil {
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
