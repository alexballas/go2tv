package gui

import (
	"context"

	"github.com/alexballas/refyne/v2"

	"go2tv.app/go2tv/v2/internal/torrentstream"
)

type torrentPlaybackStartup struct {
	session *torrentstream.Session
	cancel  context.CancelFunc
	done    chan struct{}
}

// Register before launching playback, so cancellation can abort and join work
// that has not published a renderer or HTTP server yet.
func (s *FyneScreen) beginTorrentPlayback(path string) (context.Context, func(), bool) {
	if !s.torrent.operationMu.TryLock() {
		return nil, nil, false
	}
	defer s.torrent.operationMu.Unlock()
	s.torrent.mu.Lock()
	defer s.torrent.mu.Unlock()
	if s.torrent.cancelDone != nil || s.torrent.startup != nil {
		return nil, nil, false
	}
	s.torrent.playback = nil
	if s.torrent.session == nil || path != s.torrent.path {
		return context.Background(), func() {}, true
	}
	ctx, cancel := context.WithCancel(context.Background())
	startup := &torrentPlaybackStartup{session: s.torrent.session, cancel: cancel, done: make(chan struct{})}
	s.torrent.startup, s.torrent.playback = startup, startup.session
	return ctx, func() {
		cancel()
		s.torrent.mu.Lock()
		s.torrent.startup = nil
		close(startup.done)
		s.torrent.mu.Unlock()
	}, true
}

// Stop interrupts startup but keeps downloading. Publish the same teardown
// barrier as Cancel download so a subsequent Play waits for Stop to finish.
func (s *FyneScreen) stopTorrentStartup() bool {
	s.torrent.mu.Lock()
	startup := s.torrent.startup
	if startup == nil {
		s.torrent.mu.Unlock()
		return false
	}
	previous := s.torrent.cancelDone
	done := make(chan struct{})
	s.torrent.cancelDone = done
	startup.cancel()
	s.torrent.mu.Unlock()
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

func (s *FyneScreen) cancelTorrentStartup() <-chan struct{} {
	s.torrent.mu.Lock()
	defer s.torrent.mu.Unlock()
	if startup := s.torrent.startup; startup != nil {
		startup.cancel()
		return startup.done
	}
	return nil
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
