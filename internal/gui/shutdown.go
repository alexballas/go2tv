package gui

import (
	"time"

	"go2tv.app/go2tv/v2/httphandlers"
)

const (
	// shutdownRendererGrace bounds how long closing the app waits for the
	// renderer to take Stop. An unreachable TV must not hold the window open.
	shutdownRendererGrace = 3 * time.Second
	// shutdownExitGrace bounds every wait on teardown: an operation that cannot
	// be interrupted must not keep the app from exiting. It exceeds the desktop
	// remote session's own grace.
	shutdownExitGrace = 25 * time.Second
)

// stopPlaybackForShutdown leaves the TV idle instead of on a dead stream when
// it answers promptly, then reaps local encoders and background work anyway.
func (s *FyneScreen) stopPlaybackForShutdown() {
	// Captured first so a slow renderer Stop cannot delay reaping encoders.
	server := s.currentHTTPServer()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		stopActionSync(s)
	}()
	select {
	case <-stopped:
	case <-time.After(shutdownRendererGrace):
	}
	// A request reading the live screencast only returns once its producer stops.
	stopScreencastSession(s)
	stopHTTPServer(server)
	s.workers.wait()
	// A startup that raced cancellation may have published another session or
	// server. The field keeps the stopped server so running code never sees nil.
	stopScreencastSession(s)
	stopHTTPServer(s.currentHTTPServer())
}

func stopHTTPServer(server *httphandlers.HTTPserver) {
	if server != nil {
		server.StopServer()
	}
}

// shutdownWait closes once teardown finishes or shutdownExitGrace elapses.
func (s *FyneScreen) shutdownWait() <-chan struct{} {
	done := s.beginGUIShutdown()
	s.shutdownWaitOnce.Do(func() {
		bounded := make(chan struct{})
		s.shutdownWaitCh = bounded
		go func() {
			defer close(bounded)
			select {
			case <-done:
			case <-time.After(shutdownExitGrace):
			}
		}()
	})
	return s.shutdownWaitCh
}
