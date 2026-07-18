//go:build !(android || ios)

package gui

import (
	"errors"
	"sync"

	"github.com/alexballas/refyne/v2/lang"
)

var (
	errRemoteLeaseHeld    = errors.New("remote web session is active")
	errRendererBusy       = errors.New("renderer action in flight")
	errRemoteLeaseGranted = errors.New("remote lease already granted")
)

// rendererControlGate makes GUI renderer mutations and the remote web session
// mutually exclusive. Discovery, device selection, media/subtitle/queue
// preparation and settings never take permits and stay usable.
type rendererControlGate struct {
	mu      sync.Mutex
	remote  bool
	permits int
}

// acquireMutationPermit is taken by every GUI renderer-mutation entry point
// before any async work and released after the network work finishes. It
// fails while the remote lease is held.
func (g *rendererControlGate) acquireMutationPermit() (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.remote {
		return nil, errRemoteLeaseHeld
	}
	g.permits++
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			g.permits--
			g.mu.Unlock()
		})
	}, nil
}

// acquireRemoteLease atomically claims renderer exclusivity for a managed
// session. It fails when another lease is held or a GUI mutation is in
// flight. The returned release is idempotent.
func (g *rendererControlGate) acquireRemoteLease() (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.remote {
		return nil, errRemoteLeaseGranted
	}
	if g.permits > 0 {
		return nil, errRendererBusy
	}
	g.remote = true
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			g.remote = false
			g.mu.Unlock()
		})
	}, nil
}

// remoteLeaseHeld reports whether renderer mutations are currently blocked.
func (g *rendererControlGate) remoteLeaseHeld() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.remote
}

// rendererPermit is the single guard every GUI renderer-mutation entry point
// takes before async work. When the remote session owns the renderer it
// optionally surfaces a friendly error and reports failure.
func (s *FyneScreen) rendererPermit(notify bool) (func(), bool) {
	release, err := s.renderGate.acquireMutationPermit()
	if err != nil {
		if notify {
			check(s, errors.New(lang.L("stop the remote web session to control devices from this app")))
		}
		return func() {}, false
	}
	return release, true
}
