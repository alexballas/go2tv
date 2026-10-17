//go:build android || ios

package gui

import (
	"context"

	"github.com/alexballas/refyne/v2"
)

// Called on the UI goroutine, before launching playback work.
func claimMobilePlayback(s *FyneScreen) bool {
	s.mu.Lock()
	if s.playbackStarting || s.cancelEnablePlay != nil {
		s.mu.Unlock()
		return false
	}
	s.playbackStarting = true
	s.playbackStartCtx, s.playbackStartCancel = context.WithCancel(context.Background())
	s.mu.Unlock()
	s.PlayPause.Disable()
	s.refreshPlaybackStatus()
	// Android requires the first service start while the app is visible.
	beginBackgroundSession(s)
	return true
}

func startMobilePlayback(s *FyneScreen) {
	if claimMobilePlayback(s) {
		go playMobileAction(s, false)
	}
}

func (s *FyneScreen) mobilePlaybackStarting() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.playbackStarting
}

func (s *FyneScreen) mobilePlaybackContext() context.Context {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.playbackStartCtx
}

func (s *FyneScreen) cancelMobilePlayback() {
	s.mu.RLock()
	cancel := s.playbackStartCancel
	s.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
}

func finishMobilePlayback(s *FyneScreen) {
	fyne.DoAndWait(func() {
		s.mu.Lock()
		if s.playbackStartCancel != nil {
			s.playbackStartCancel()
		}
		s.playbackStartCtx, s.playbackStartCancel = nil, nil
		s.playbackStarting = false
		s.mu.Unlock()
		setPlayPauseView("", s)
		syncBackgroundSession(s, s.getScreenState())
	})
}
