package gui

import (
	"context"
	"strings"

	"github.com/alexballas/refyne/v2"

	"go2tv.app/go2tv/v2/internal/radiobrowser"
	"go2tv.app/go2tv/v2/metadata"
)

// Bounds the station-row logo cache shared with playback.
const maxRadioIconCacheEntries = 64

// Only visible list rows start fetches; four concurrent requests share a cache.
func (r *radioView) loadRadioIcon(station radiobrowser.Station) {
	icon := station.Icon()
	key := icon.Key()
	if icon.Empty() || r.iconsPending[key] {
		return
	}
	if _, _, cached := r.artwork.lookup(icon); cached {
		return
	}
	r.iconsPending[key] = true
	ctx := r.ctx
	go func() {
		_, err := r.artwork.fetch(ctx, icon)
		fyne.Do(func() {
			if ctx.Err() != nil || r.ctx != ctx {
				return
			}
			delete(r.iconsPending, key)
			r.screen.logRadioError("artwork", err)
			for id, station := range r.stations {
				if station.Icon() == icon {
					r.list.RefreshItem(id)
				}
			}
		})
	}()
}

func radioArtworkIdentity(station radiobrowser.Station) string {
	return "radio:" + station.UUID + ":" + station.StreamURL() + ":" + station.Icon().Key()
}

// Selection binds artwork to both station UUID and URL before UI/network work.
func (s *FyneScreen) setSelectedRadioArtwork(station radiobrowser.Station, asset *metadata.ArtworkAsset) {
	temporary := s.radioTemporaryMode()
	s.mu.Lock()
	if s.radioSelected.UUID != station.UUID || s.radioSelected.StreamURL() != station.StreamURL() || s.radioSelected.Icon() != station.Icon() {
		s.mu.Unlock()
		return
	}
	s.radioArtworkResolved = asset != nil || station.Icon().Empty()
	s.radioArtwork = asset
	preview := !temporary && s.currentArtworkIdentity == radioArtworkIdentity(station)
	if preview {
		s.currentArtwork = asset
	}
	s.mu.Unlock()
	if preview {
		s.previewRadioArtwork(asset)
	}
	s.refreshRadioSongView()
}

func (s *FyneScreen) resolveCurrentRadioArtwork(ctx context.Context, mediaURL string) *metadata.ArtworkAsset {
	s.mu.Lock()
	station := s.radioSelected
	if station.UUID == "" || station.StreamURL() != strings.TrimSpace(mediaURL) {
		s.currentArtworkIdentity = ""
		s.currentArtwork = nil
		s.mu.Unlock()
		return nil
	}
	asset, resolved := s.radioArtwork, s.radioArtworkResolved
	s.currentArtworkIdentity = radioArtworkIdentity(station)
	s.currentArtwork = asset
	s.mu.Unlock()
	if resolved || station.Icon().Empty() {
		return asset
	}
	asset, err := s.getRadioArtworkCache().fetch(ctx, station.Icon())
	if ctx.Err() != nil {
		return nil
	}
	s.logRadioError("artwork", err)
	if err != nil {
		return nil
	}
	s.mu.Lock()
	if s.radioSelected.UUID != station.UUID || s.radioSelected.StreamURL() != station.StreamURL() || s.currentArtworkIdentity != radioArtworkIdentity(station) {
		s.mu.Unlock()
		return nil
	}
	s.radioArtwork, s.currentArtwork = asset, asset
	s.radioArtworkResolved = true
	s.mu.Unlock()
	fyne.Do(func() { s.setSelectedRadioArtwork(station, asset) })
	return asset
}

func (s *FyneScreen) loadSelectedRadioArtwork(ctx context.Context, station radiobrowser.Station) {
	go func() {
		asset, err := s.getRadioArtworkCache().fetch(ctx, station.Icon())
		fyne.Do(func() {
			if ctx.Err() == nil {
				s.logRadioError("artwork", err)
				if err != nil {
					return
				}
				s.setSelectedRadioArtwork(station, asset)
			}
		})
	}()
}

func chromecastRadioArtworkAddress(mediaAddress, artworkAddress string, asset *metadata.ArtworkAsset) string {
	if asset != nil && artworkAddress != "" {
		return artworkAddress
	}
	return mediaAddress
}

func (s *FyneScreen) invalidateRadioURL(mediaURL string) {
	if s.radioTemporaryMode() {
		s.stopRadioSongMonitor()
		return
	}
	// Local selections reuse the entry; only URL edits may replace the station.
	if s.ExternalMediaURL != nil && !s.ExternalMediaURL.Checked {
		return
	}
	s.mu.Lock()
	s.urlInput = mediaURL
	station := s.radioSelected
	if station.UUID == "" {
		s.mu.Unlock()
		return
	}
	if station.StreamURL() == strings.TrimSpace(mediaURL) {
		asset := s.radioArtwork
		s.currentArtworkIdentity = radioArtworkIdentity(station)
		s.currentArtwork = asset
		s.mu.Unlock()
		s.previewRadioArtwork(asset)
		return
	}
	// Keep the station: radioStationForURL ignores it while the URL differs,
	// and a typo fixed back restores artwork and song details. The song
	// monitor keeps following the station still on the receiver.
	if strings.HasPrefix(s.currentArtworkIdentity, "radio:") {
		s.currentArtworkIdentity = ""
		s.currentArtwork = nil
	}
	s.mu.Unlock()
	s.previewRadioArtwork(nil)
}
