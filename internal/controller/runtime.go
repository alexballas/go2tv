package controller

import (
	"context"
	"io"
	"time"

	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/internal/playbackadapter"
)

// RuntimeConfig composes production protocol adapters without server/UI wiring.
type RuntimeConfig struct {
	ParentContext     context.Context
	MediaServer       playback.MediaServer
	Callbacks         *playbackadapter.CallbackBridge
	LogOutput         io.Writer
	DLNADelay         int
	DiscoveryInterval time.Duration
	OperationTimeout  time.Duration
	Logger            EventLogger
	Artwork           *ArtworkCache
	DurationProbe     func(context.Context, playback.SourceOpener) (float64, error)
	// Discovery overrides the default scanner-backed discovery service when
	// non-nil. GUI-managed children inject a pipe-fed discovery here; nil
	// keeps the standalone SSDP/mDNS construction.
	Discovery playback.Discovery
}

// NewRuntimeConfig builds a Config backed by production discovery, transport,
// and monitor adapters. It does not start them; New does.
func NewRuntimeConfig(cfg RuntimeConfig) Config {
	discovery := cfg.Discovery
	if discovery == nil {
		discovery = playback.NewDiscoveryService(playbackadapter.Scanner{DLNADelay: cfg.DLNADelay}, nil, nil, cfg.DiscoveryInterval)
	}
	factory := &playbackadapter.Factory{LogOutput: cfg.LogOutput, CallbackURL: callbackURLProvider(cfg.MediaServer), Callbacks: cfg.Callbacks}
	return Config{ParentContext: cfg.ParentContext, Discovery: discovery, TransportFactory: factory, MediaServer: cfg.MediaServer, Artwork: cfg.Artwork, RunMonitor: playbackadapter.RunMonitor, DurationProbe: cfg.DurationProbe, OperationTimeout: cfg.OperationTimeout, Logger: cfg.Logger}
}

func callbackURLProvider(server playback.MediaServer) playbackadapter.CallbackURLProvider {
	provider, _ := server.(playbackadapter.CallbackURLProvider)
	return provider
}
