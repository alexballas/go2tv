//go:build !(android || ios)

package gui

import (
	"path/filepath"

	"github.com/alexballas/refyne/v2"

	"go2tv.app/go2tv/v2/internal/mediasource"
)

func torrentCacheDir() (string, error) { return "", nil }

func syncTorrentBackgroundSession(_ *FyneScreen) {}

// Desktop Play may run on the UI goroutine; cancellation needs that goroutine
// for its final cleanup, so queue one continuation instead of blocking it.
func (s *FyneScreen) deferTorrentPlayback(target playbackTarget) bool {
	done := s.torrentCancellationDone()
	s.mu.Lock()
	if s.torrentPlayPending != nil {
		s.mu.Unlock()
		return true
	}
	if done == nil {
		s.mu.Unlock()
		return false
	}
	abort := make(chan struct{})
	s.torrentPlayPending = abort
	s.mu.Unlock()
	fyne.Do(func() { s.PlayPause.Disable() })
	go func() {
		select {
		case <-abort:
			return
		case <-done:
		}
		fyne.Do(func() {
			s.mu.Lock()
			current := s.torrentPlayPending == abort
			if current {
				s.torrentPlayPending = nil
			}
			s.mu.Unlock()
			if current {
				// Recheck cancellation in case another teardown was queued.
				playActionOnTarget(s, target)
			}
		})
	}()
	return true
}

func (s *FyneScreen) torrentPlaybackPending() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.torrentPlayPending != nil
}

func (s *FyneScreen) cancelPendingTorrentPlayback() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.torrentPlayPending != nil {
		close(s.torrentPlayPending)
		s.torrentPlayPending = nil
	}
}

func selectTorrentMedia(s *FyneScreen, path string) {
	s.replaceSessionQueue(nil, -1)
	if s.ExternalMediaURL != nil && s.ExternalMediaURL.Checked {
		fyne.DoAndWait(func() { s.ExternalMediaURL.SetChecked(false) })
	}
	s.mediafile = path
	s.subsfile = ""
	s.setCurrentArtwork(nil)
	fyne.Do(func() {
		if s.NextMediaCheck != nil {
			s.NextMediaCheck.SetChecked(false)
		}
		s.MediaText.SetText(filepath.Base(path))
		setInternalSubsDropdownNoSubs(s)
		s.refreshQueueStateUI()
		setPlayPauseView("", s)
	})
}

func torrentMediaSelected(s *FyneScreen) bool {
	_, ok := mediasource.Lookup(s.mediafile)
	return ok
}

func clearTorrentSelection(s *FyneScreen, path string) {
	if s.mediafile == path {
		clearCurrentMediaSelection(s)
	}
}
