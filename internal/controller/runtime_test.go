package controller

import (
	"context"
	"testing"

	"go2tv.app/go2tv/v2/internal/playback"
)

type stubDiscovery struct{}

func (stubDiscovery) Start(context.Context)         {}
func (stubDiscovery) Refresh(context.Context) error { return nil }
func (stubDiscovery) Snapshot() []playback.Device   { return nil }
func (stubDiscovery) Subscribe(int) (<-chan []playback.Device, func()) {
	ch := make(chan []playback.Device)
	return ch, func() { close(ch) }
}

func TestNewRuntimeConfigDiscoveryInjection(t *testing.T) {
	t.Parallel()
	injected := stubDiscovery{}
	cfg := NewRuntimeConfig(RuntimeConfig{Discovery: injected})
	if cfg.Discovery != injected {
		t.Fatalf("injected discovery not used: %T", cfg.Discovery)
	}

	// Nil keeps the standalone scanner-backed construction.
	cfg = NewRuntimeConfig(RuntimeConfig{})
	if _, ok := cfg.Discovery.(*playback.DiscoveryService); !ok {
		t.Fatalf("default discovery = %T, want *playback.DiscoveryService", cfg.Discovery)
	}
}
