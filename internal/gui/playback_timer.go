package gui

import (
	"context"
	"errors"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/lang"

	"go2tv.app/go2tv/v2/soapcalls"
)

// Renderers buffering live streams report TRANSITIONING for a while. Give up
// when playback never starts, or the renderer settles back in STOPPED.
type dlnaStartupWait struct {
	interval     time.Duration
	timeout      time.Duration
	stoppedPolls int
}

var defaultDLNAStartupWait = dlnaStartupWait{interval: time.Second, timeout: 30 * time.Second, stoppedPolls: 10}

func (s *FyneScreen) dlnaPlaybackPending() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cancelEnablePlay != nil
}

// A successful Play command only acknowledges the request. Keep controls
// disabled until a callback or transport poll confirms playback.
func (s *FyneScreen) waitForDLNAPlayback(payload *soapcalls.TVPayload) {
	s.watchDLNAStartup(payload, defaultDLNAStartupWait)
}

func (s *FyneScreen) watchDLNAStartup(payload *soapcalls.TVPayload, wait dlnaStartupWait) {
	s.mu.Lock()
	if s.State == "Playing" || s.State == "Paused" {
		s.mu.Unlock()
		return
	}
	previous := s.cancelEnablePlay
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelEnablePlay = cancel
	s.mu.Unlock()
	if previous != nil {
		previous()
	}

	go func() {
		ticker := time.NewTicker(wait.interval)
		defer ticker.Stop()
		deadline := time.NewTimer(wait.timeout)
		defer deadline.Stop()
		stopped := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-deadline.C:
				s.failDLNAStartup(ctx)
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
			case "STOPPED", "NO_MEDIA_PRESENT":
				if stopped++; stopped >= wait.stoppedPolls {
					s.failDLNAStartup(ctx)
					return
				}
				continue
			default:
				stopped = 0
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

// failDLNAStartup ends a cast the renderer accepted but never played, so
// controls do not stay in "Loading media…".
func (s *FyneScreen) failDLNAStartup(ctx context.Context) {
	fyne.Do(func() {
		if ctx.Err() != nil {
			return
		}
		err := errors.New(lang.L("The device did not start playback"))
		if s.MediaText != nil {
			if _, radio := s.radioStationForURL(s.MediaText.Text); radio {
				err = errors.New(lang.L("Station offline or not supported by this device"))
			}
		}
		stopAction(s)
		s.showPlaybackError(err)
	})
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
