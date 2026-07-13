package controller

import (
	"io"
	"time"

	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/internal/playbackadapter"
)

// RuntimeConfig composes production protocol adapters without server/UI wiring.
type RuntimeConfig struct {
	MediaServer       playback.MediaServer
	Callbacks         *playbackadapter.CallbackBridge
	LogOutput         io.Writer
	DLNADelay         int
	DiscoveryInterval time.Duration
	OperationTimeout  time.Duration
	Artwork           *ArtworkCache
}

func NewRuntimeConfig(cfg RuntimeConfig) Config {
	discovery := playback.NewDiscoveryService(playbackadapter.Scanner{DLNADelay: cfg.DLNADelay}, nil, nil, cfg.DiscoveryInterval)
	factory := &playbackadapter.Factory{LogOutput: cfg.LogOutput, CallbackURL: callbackURLProvider(cfg.MediaServer), Callbacks: cfg.Callbacks}
	return Config{Discovery: discovery, TransportFactory: factory, MediaServer: cfg.MediaServer, Artwork: cfg.Artwork, RunMonitor: playbackadapter.RunMonitor, OperationTimeout: cfg.OperationTimeout}
}

func callbackURLProvider(server playback.MediaServer) playbackadapter.CallbackURLProvider {
	provider, _ := server.(playbackadapter.CallbackURLProvider)
	return provider
}
