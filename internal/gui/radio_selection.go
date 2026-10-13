package gui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/lang"

	"go2tv.app/go2tv/v2/internal/radiobrowser"
	"go2tv.app/go2tv/v2/metadata"
	"go2tv.app/go2tv/v2/utils"
)

// radioStationForURL prevents stale radio metadata affecting a pasted URL/file.
func (s *FyneScreen) radioStationForURL(mediaURL string) (radiobrowser.Station, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	station := s.radioSelected
	return station, station.UUID != "" && station.StreamURL() == strings.TrimSpace(mediaURL)
}

func (s *FyneScreen) radioMediaType(mediaURL, inferred string, transcode bool) string {
	station, ok := s.radioStationForURL(mediaURL)
	if !ok {
		return inferred
	}
	if !transcode && (station.HLS == 1 || utils.IsHLSStream(mediaURL, inferred)) {
		return "application/vnd.apple.mpegurl"
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(inferred)), "audio/") {
		return inferred
	}
	return station.MediaType()
}

func (s *FyneScreen) radioTitle(mediaURL string) string {
	station, ok := s.radioStationForURL(mediaURL)
	if !ok {
		return ""
	}
	return station.Name
}

func (s *FyneScreen) radioMediaMetadata(mediaURL, fallbackTitle, listenAddress string, asset *metadata.ArtworkAsset) metadata.Media {
	station, selected := s.radioStationForURL(mediaURL)
	if selected {
		fallbackTitle = station.Name
	}
	mediaMetadata := guiMediaMetadata(fallbackTitle, listenAddress, asset)
	mediaMetadata.AudioOnly = selected
	return mediaMetadata
}

func (s *FyneScreen) radioDirectHLSURL(mediaURL, mediaType string, transcode bool) string {
	station, selected := s.radioStationForURL(mediaURL)
	if !selected || transcode || !utils.IsHLSStream(mediaURL, mediaType) {
		return ""
	}
	// Preserve the origin for relative playlists and segment URLs.
	return station.StreamURL()
}

func (s *FyneScreen) reportRadioPlayback(mediaURL string) {
	s.startRadioSongMonitor(mediaURL)
	station, ok := s.radioStationForURL(mediaURL)
	if !ok || s.radio == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(s.radio.parentCtx, 8*time.Second)
		defer cancel()
		s.logRadioError("click", s.radio.client.Click(ctx, station.UUID))
	}()
}

func (r *radioView) useRadioStation() {
	if r.selected < 0 || r.selected >= len(r.stations) || r.selectPending {
		return
	}
	station := r.stations[r.selected]
	if station.StreamURL() == "" {
		r.status.SetText(lang.L("Invalid station URL"))
		return
	}
	if err := r.screen.validateRadioSelection(); err != nil {
		r.status.SetText(err.Error())
		return
	}
	r.screen.cancelQueuedRadioPlayback()
	r.selectPending = true
	r.refreshRadioSelection()
	r.status.SetText(lang.L("Selecting station…"))
	ctx := r.ctx
	refreshFavorite := r.isFavorite(station.UUID)
	asset, _, cached := r.artwork.lookup(station.Favicon)
	cached = cached && asset != nil
	previous, sameRadio := r.screen.radioStationForURL(r.screen.MediaText.Text)
	go func() {
		if refreshFavorite {
			refreshCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			refreshed, err := r.client.Station(refreshCtx, station.UUID)
			cancel()
			if err == nil {
				if refreshed.Favicon != station.Favicon {
					cached = false
					asset = nil
				}
				station = refreshed
			} else if ctx.Err() == nil {
				r.screen.logRadioError("refresh favorite", err)
			}
		}
		if ctx.Err() != nil {
			return
		}
		if !sameRadio || previous.UUID != station.UUID || previous.StreamURL() != station.StreamURL() {
			stopActionSync(r.screen)
		}
		fyne.Do(func() {
			if ctx.Err() != nil || r.ctx != ctx {
				return
			}
			r.selectPending = false
			if err := r.screen.validateRadioSelection(); err != nil {
				r.status.SetText(err.Error())
				r.refreshRadioSelection()
				return
			}
			r.screen.selectRadioStation(station)
			if cached {
				r.screen.setSelectedRadioArtwork(station, asset)
			} else {
				r.screen.previewRadioArtwork(nil)
			}
			if r.modal != nil {
				r.modal.Hide()
			}
			if !cached && station.Favicon != "" {
				r.screen.loadSelectedRadioArtwork(r.parentCtx, station)
			}
		})
	}()
}

func (s *FyneScreen) resetRadioSelection(station radiobrowser.Station) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.radioSelected = station
	s.radioArtworkResolved = station.Favicon == ""
	s.radioArtwork = nil
	s.currentArtworkIdentity = radioArtworkIdentity(station)
	s.currentArtwork = nil
}

func (s *FyneScreen) logRadioError(action string, err error) {
	if err == nil || errors.Is(err, context.Canceled) || s.Debug == nil {
		return
	}
	_, _ = fmt.Fprintf(s.Debug, "radio %s: %v\n", action, err)
}
