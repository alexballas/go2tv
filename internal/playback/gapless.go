package playback

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var ErrGaplessUnavailable = errors.New("gapless requires DLNA autoplay")

type GaplessItem struct {
	ID        string
	Open      SourceOpener
	Extension string
	MediaURL  string
	Metadata  string
	MediaType string
}

type GaplessQueue interface {
	Next(string) (GaplessItem, bool)
}
type GaplessSession interface {
	ActiveID() string
	Promote(GaplessItem)
}

type GaplessPolicy interface {
	Autoplay() bool
	Protocol() string
}

type GaplessEngine struct {
	mu        sync.Mutex
	queue     GaplessQueue
	policy    GaplessPolicy
	session   GaplessSession
	transport DLNATransport
	server    MediaServer
	next      *GaplessItem
	enabled   bool
}

func NewGaplessEngine(queue GaplessQueue, policy GaplessPolicy, session GaplessSession, transport DLNATransport, server MediaServer) *GaplessEngine {
	return &GaplessEngine{queue: queue, policy: policy, session: session, transport: transport, server: server}
}

func (e *GaplessEngine) Enable(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.policy == nil || e.policy.Protocol() != "DLNA" || !e.policy.Autoplay() {
		return ErrGaplessUnavailable
	}
	if e.queue == nil || e.session == nil || e.transport == nil {
		return errors.New("gapless dependencies unavailable")
	}
	next, ok := e.queue.Next(e.session.ActiveID())
	if !ok {
		return errors.New("gapless next item unavailable")
	}
	if e.server != nil && next.MediaURL == "" {
		route, err := e.server.Add(ctx, RouteRequest{Open: next.Open, Extension: next.Extension, MediaType: next.MediaType})
		if err != nil {
			return fmt.Errorf("gapless media route: %w", err)
		}
		next.MediaURL = route.URL
	}
	if err := e.transport.SetNextURI(ctx, next.MediaURL, next.Metadata); err != nil {
		return fmt.Errorf("set next URI: %w", err)
	}
	e.next, e.enabled = &next, true
	return nil
}

func (e *GaplessEngine) Disable(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.enabled {
		return nil
	}
	e.enabled, e.next = false, nil
	if err := e.transport.ClearNextURI(ctx); err != nil {
		if stopErr := e.transport.Stop(ctx); stopErr != nil {
			return fmt.Errorf("clear next URI: %v; stop: %w", err, stopErr)
		}
		return fmt.Errorf("clear next URI: %w", err)
	}
	return nil
}

// Promote preserves the exact queued target during a renderer transition.
func (e *GaplessEngine) Promote() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.enabled || e.next == nil {
		return false
	}
	e.session.Promote(*e.next)
	e.next = nil
	return true
}
