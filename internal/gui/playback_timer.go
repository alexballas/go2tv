package gui

import (
	"context"
	"time"

	"github.com/alexballas/refyne/v2"

	"go2tv.app/go2tv/v2/soapcalls"
)

const dlnaStartupPollInterval = time.Second

func (s *FyneScreen) dlnaPlaybackPending() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cancelEnablePlay != nil
}

// A successful Play command only acknowledges the request. Keep controls
// disabled until a callback or transport poll confirms playback.
func (s *FyneScreen) waitForDLNAPlayback(payload *soapcalls.TVPayload) {
	s.mu.Lock()
	if s.State == "Playing" || s.State == "Paused" {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelEnablePlay = cancel
	s.mu.Unlock()

	go func() {
		ticker := time.NewTicker(dlnaStartupPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			out, err := payload.GetTransportInfoContext(ctx)
			if err != nil || len(out) == 0 {
				continue
			}
			var state, button string
			switch out[0] {
			case "PLAYING":
				state, button = "Playing", "Pause"
			case "PAUSED_PLAYBACK":
				state, button = "Paused", "Play"
			default:
				continue
			}
			fyne.Do(func() {
				if ctx.Err() != nil {
					return
				}
				s.updateScreenState(state)
				setPlayPauseView(button, s)
			})
			return
		}
	}()
}

func (s *FyneScreen) cancelPlayTimer() {
	s.mu.Lock()
	cancel := s.cancelEnablePlay
	s.cancelEnablePlay = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
