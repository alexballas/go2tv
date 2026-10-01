//go:build android || ios

package gui

import "github.com/alexballas/refyne/v2"

// Called on the UI goroutine, before launching playback work.
func claimMobilePlayback(s *FyneScreen) bool {
	s.mu.Lock()
	if s.playbackStarting {
		s.mu.Unlock()
		return false
	}
	s.playbackStarting = true
	s.mu.Unlock()
	s.PlayPause.Disable()
	// Android requires the first service start while the app is visible.
	beginBackgroundSession(s)
	return true
}

func startMobilePlayback(s *FyneScreen) {
	if claimMobilePlayback(s) {
		go playMobileAction(s)
	}
}

func (s *FyneScreen) mobilePlaybackStarting() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.playbackStarting
}

func finishMobilePlayback(s *FyneScreen) {
	fyne.DoAndWait(func() {
		s.mu.Lock()
		s.playbackStarting = false
		s.mu.Unlock()
		setPlayPauseView("", s)
		syncBackgroundSession(s, s.getScreenState())
	})
}
