package gui

import (
	"context"

	"github.com/alexballas/refyne/v2"

	"go2tv.app/go2tv/v2/internal/torrentstream"
)

type playbackStartup struct {
	session *torrentstream.Session
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
}

// Register before launching playback, so cancellation can abort and join work
// that has not published a renderer or HTTP server yet.
func (s *FyneScreen) beginPlaybackStartup(path string) (context.Context, func(), bool) {
	if !s.torrent.operationMu.TryLock() {
		return nil, nil, false
	}
	defer s.torrent.operationMu.Unlock()
	s.torrent.mu.Lock()
	defer s.torrent.mu.Unlock()
	if s.torrent.cancelDone != nil || s.torrent.startup != nil {
		return nil, nil, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	startup := &playbackStartup{ctx: ctx, cancel: cancel, done: make(chan struct{})}
	if path == s.torrent.path {
		startup.session = s.torrent.session
	}
	s.torrent.startup, s.torrent.playback = startup, startup.session
	return ctx, func() {
		s.torrent.mu.Lock()
		cancel()
		s.torrent.startup = nil
		s.torrent.mu.Unlock()
		refreshPlaybackControls("", s)
		fyne.DoAndWait(func() {})
		close(startup.done)
	}, true
}

// Stop interrupts startup and keeps any download. Publish the same teardown
// barrier as Cancel download so a subsequent Play waits for Stop to finish.
func (s *FyneScreen) stopPlaybackStartup() bool {
	s.torrent.mu.Lock()
	startup := s.torrent.startup
	if startup == nil {
		s.torrent.mu.Unlock()
		return false
	}
	if startup.ctx.Err() != nil && s.torrent.cancelDone != nil {
		s.torrent.mu.Unlock()
		return true
	}
	previous := s.torrent.cancelDone
	done := make(chan struct{})
	s.torrent.cancelDone = done
	startup.cancel()
	s.torrent.mu.Unlock()
	s.nextChromecastActionID()
	setPlayPauseView("", s)
	go func() {
		defer s.finishTorrentCancellation(done)
		if previous != nil {
			<-previous
		}
		<-startup.done
		s.torrent.operationMu.Lock()
		defer s.torrent.operationMu.Unlock()
		stopActionInternal(s, true)
		fyne.DoAndWait(func() { setPlayPauseView("", s) })
	}()
	return true
}

func (s *FyneScreen) cancelPlaybackStartup() <-chan struct{} {
	s.torrent.mu.Lock()
	if startup := s.torrent.startup; startup != nil {
		startup.cancel()
		s.torrent.mu.Unlock()
		s.nextChromecastActionID()
		return startup.done
	}
	s.torrent.mu.Unlock()
	return nil
}

func (s *FyneScreen) cancelTorrentStartup() <-chan struct{} {
	s.torrent.mu.Lock()
	defer s.torrent.mu.Unlock()
	if startup := s.torrent.startup; startup != nil && startup.session != nil && startup.session == s.torrent.session {
		startup.cancel()
		return startup.done
	}
	return nil
}

func (s *FyneScreen) playbackStartupPending() bool {
	s.torrent.mu.Lock()
	defer s.torrent.mu.Unlock()
	return s.torrent.startup != nil || s.torrent.cancelDone != nil
}

func (s *FyneScreen) playbackStartupContext() context.Context {
	s.torrent.mu.Lock()
	defer s.torrent.mu.Unlock()
	if startup := s.torrent.startup; startup != nil {
		return startup.ctx
	}
	return context.Background()
}

// URL bodies outlive preparation. Detach their cancellation when the worker
// hands them to the media server, before finishing the startup context.
func playbackStreamContext(startupCtx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	detach := context.AfterFunc(startupCtx, cancel)
	return ctx, func() {
		detach()
		if startupCtx.Err() != nil {
			cancel()
		}
	}
}

func (s *FyneScreen) torrentOwnsPlayback(session *torrentstream.Session, path string) bool {
	s.torrent.mu.Lock()
	owned := s.torrent.playback == session
	s.torrent.mu.Unlock()
	if owned {
		return true
	}
	// An established DLNA payload identifies the media independently of the
	// current selection, which may already point at a replacement file.
	return s.tvdata != nil && s.tvdata.MediaPath == path
}
