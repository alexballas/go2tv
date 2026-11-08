//go:build android || ios

package gui

import "go2tv.app/go2tv/v2/httphandlers"

// Teardown runs off the UI goroutine while the event loop still processes
// the UI work queued by playback teardown.
func (s *FyneScreen) beginGUIShutdown() <-chan struct{} {
	s.shutdownOnce.Do(func() {
		s.shutdownDone = make(chan struct{})
		s.workers.stop()
		s.cancelMobilePlayback()
		go func() {
			defer close(s.shutdownDone)
			s.stopPlaybackForShutdown()
			s.shutdownTorrents()
		}()
	})
	return s.shutdownDone
}

// Mobile has no lock dedicated to httpserver; playback goroutines publish it.
func (s *FyneScreen) currentHTTPServer() *httphandlers.HTTPserver {
	return s.httpserver
}

// Mobile has no screencast.
func stopScreencastSession(*FyneScreen) {}
