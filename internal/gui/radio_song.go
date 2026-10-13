package gui

import (
	"context"
	"strings"
	"sync"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/internal/radiometadata"
	"go2tv.app/go2tv/v2/utils"
)

type radioSongState struct {
	label      *widget.Label
	cancel     context.CancelFunc
	generation uint64
	mediaURL   string
	title      string
}

func newRadioSongLabel(screen *FyneScreen) *widget.Label {
	label := widget.NewLabel("")
	label.Truncation = fyne.TextTruncateEllipsis
	label.Importance = widget.LowImportance
	label.Hide()
	screen.radioSong.label = label
	return label
}

func (s *FyneScreen) stopRadioSongMonitor() uint64 {
	s.mu.Lock()
	cancel := s.radioSong.cancel
	s.radioSong.cancel = nil
	s.radioSong.generation++
	generation := s.radioSong.generation
	s.radioSong.mediaURL = ""
	s.radioSong.title = ""
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	fyne.Do(func() {
		s.mu.RLock()
		current := s.radioSong.generation == generation
		s.mu.RUnlock()
		if current {
			s.refreshRadioSongView()
		}
	})
	return generation
}

func (s *FyneScreen) startRadioSongMonitor(mediaURL string) {
	generation := s.stopRadioSongMonitor()
	s.mu.RLock()
	hasDisplay := s.radioSongDisplayAvailable()
	s.mu.RUnlock()
	if !hasDisplay {
		return
	}
	station, selected := s.radioStationForURL(mediaURL)
	if !selected || station.HLS == 1 || utils.IsHLSStream(mediaURL, "") {
		return
	}
	parentCtx := context.Background()
	if s.radio != nil {
		parentCtx = s.radio.parentCtx
	}
	ctx, cancel := context.WithCancel(parentCtx)
	s.mu.Lock()
	if ctx.Err() != nil || s.radioSong.generation != generation || s.radioSelected.UUID != station.UUID || s.radioSelected.StreamURL() != station.StreamURL() {
		s.mu.Unlock()
		cancel()
		return
	}
	s.radioSong.cancel = cancel
	s.radioSong.mediaURL = mediaURL
	s.mu.Unlock()
	go func() {
		// Keep at most one title update queued while the UI is busy.
		var pendingMu sync.Mutex
		pending := false
		latest := ""
		err := radiometadata.StreamTitles(ctx, mediaURL, func(title string) {
			if ctx.Err() != nil {
				return
			}
			pendingMu.Lock()
			latest = title
			if pending {
				pendingMu.Unlock()
				return
			}
			pending = true
			pendingMu.Unlock()
			fyne.Do(func() {
				pendingMu.Lock()
				title := latest
				pending = false
				pendingMu.Unlock()
				s.applyRadioSongTitle(generation, mediaURL, title)
			})
		})
		if ctx.Err() != nil {
			return
		}
		s.logRadioError("track metadata", err)
		cancel()
		fyne.Do(func() { s.applyRadioSongTitle(generation, mediaURL, "") })
	}()
}

func (s *FyneScreen) applyRadioSongTitle(generation uint64, mediaURL, title string) {
	s.mu.Lock()
	active := s.State == "Playing" || s.State == "Paused" || s.activeDevice.addr != ""
	if s.radioSong.generation != generation || s.radioSong.mediaURL != mediaURL || s.radioSelected.UUID == "" || s.radioSelected.StreamURL() != strings.TrimSpace(mediaURL) || !active {
		s.mu.Unlock()
		return
	}
	s.radioSong.title = title
	s.mu.Unlock()
	s.refreshRadioSongView()
}
