package gui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alexballas/refyne/v2"
	_ "golang.org/x/image/webp"

	"go2tv.app/go2tv/v2/internal/radiobrowser"
	"go2tv.app/go2tv/v2/metadata"
)

const (
	maxRadioIconBytes        = 2 << 20
	maxRadioIconPixels       = 4_000_000
	maxRadioIconCacheEntries = 64
	radioArtworkTimeout      = 5 * time.Second
)

var radioIconHTTPClient = &http.Client{
	Timeout: radioArtworkTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("radio icon: too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("radio icon: invalid redirect")
		}
		return nil
	},
}

func fetchRadioArtwork(ctx context.Context, favicon string) (*metadata.ArtworkAsset, error) {
	parsed, err := url.Parse(strings.TrimSpace(favicon))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("radio icon: invalid URL")
	}
	ctx, cancel := context.WithTimeout(ctx, radioArtworkTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("radio icon request: %w", err)
	}
	request.Header = http.Header{"User-Agent": {"Go2TV"}}
	response, err := radioIconHTTPClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("radio icon fetch: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("radio icon status: %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxRadioIconBytes+1))
	if err != nil {
		return nil, fmt.Errorf("radio icon body: %w", err)
	}
	if len(data) > maxRadioIconBytes {
		return nil, fmt.Errorf("radio icon too large")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("radio icon decode: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || uint64(config.Width)*uint64(config.Height) > maxRadioIconPixels {
		return nil, fmt.Errorf("radio icon dimensions too large")
	}
	switch format {
	case "jpeg", "png":
	case "gif", "webp":
		// Artwork normalization accepts JPEG/PNG; many station logos are not.
		decoded, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("radio icon decode: %w", err)
		}
		var converted bytes.Buffer
		if err := png.Encode(&converted, decoded); err != nil {
			return nil, fmt.Errorf("radio icon convert: %w", err)
		}
		data = converted.Bytes()
	default:
		return nil, fmt.Errorf("radio icon: unsupported image")
	}
	asset, err := metadata.LoadArtwork(data, parsed.String())
	if err != nil {
		return nil, fmt.Errorf("radio icon normalize: %w", err)
	}
	return asset, nil
}

// Only visible list rows start fetches; four concurrent requests share a cache.
func (r *radioView) loadRadioIcon(station radiobrowser.Station) {
	favicon := station.Favicon
	if favicon == "" || r.iconsPending[favicon] {
		return
	}
	if _, _, cached := r.artwork.lookup(favicon); cached {
		return
	}
	r.iconsPending[favicon] = true
	ctx := r.ctx
	go func() {
		_, err := r.artwork.fetch(ctx, favicon)
		fyne.Do(func() {
			if ctx.Err() != nil || r.ctx != ctx {
				return
			}
			delete(r.iconsPending, favicon)
			r.screen.logRadioError("artwork", err)
			for id, station := range r.stations {
				if station.Favicon == favicon {
					r.list.RefreshItem(id)
				}
			}
		})
	}()
}

func radioArtworkIdentity(station radiobrowser.Station) string {
	return "radio:" + station.UUID + ":" + station.StreamURL() + ":" + station.Favicon
}

// Selection binds artwork to both station UUID and URL before UI/network work.
func (s *FyneScreen) setSelectedRadioArtwork(station radiobrowser.Station, asset *metadata.ArtworkAsset) {
	temporary := s.radioTemporaryMode()
	s.mu.Lock()
	if s.radioSelected.UUID != station.UUID || s.radioSelected.StreamURL() != station.StreamURL() || s.radioSelected.Favicon != station.Favicon {
		s.mu.Unlock()
		return
	}
	s.radioArtworkResolved = asset != nil || station.Favicon == ""
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
	if resolved || station.Favicon == "" {
		return asset
	}
	asset, err := s.getRadioArtworkCache().fetch(ctx, station.Favicon)
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
		asset, err := s.getRadioArtworkCache().fetch(ctx, station.Favicon)
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
	s.mu.Lock()
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
	s.radioSelected = radiobrowser.Station{}
	s.radioArtworkResolved = false
	s.radioArtwork = nil
	if strings.HasPrefix(s.currentArtworkIdentity, "radio:") {
		s.currentArtworkIdentity = ""
		s.currentArtwork = nil
	}
	s.mu.Unlock()
	s.stopRadioSongMonitor()
	s.previewRadioArtwork(nil)
}
