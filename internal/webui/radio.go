package webui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"

	"go2tv.app/go2tv/v2/internal/controller"
	"go2tv.app/go2tv/v2/internal/mediaartwork"
	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/internal/radiobrowser"
	"go2tv.app/go2tv/v2/metadata"
	"go2tv.app/go2tv/v2/utils"
)

const (
	radioRootID         = "radio"
	maxRadioQuery       = 100
	maxRadioStations    = 2000
	maxRadioRecent      = 20
	maxRadioFavorites   = 500
	maxRadioRowTags     = 3
	radioIconRetry      = 5 * time.Minute
	maxRadioIconFails   = 512
	radioRefreshTimeout = 3 * time.Second
	radioProbeTimeout   = 8 * time.Second
	radioClickTimeout   = 8 * time.Second
)

// RadioDirectory is the Radio Browser surface the WebUI uses.
type RadioDirectory interface {
	SearchText(context.Context, string, string) ([]radiobrowser.Station, error)
	Countries(context.Context) ([]radiobrowser.Country, error)
	Station(context.Context, string) (radiobrowser.Station, error)
	Click(context.Context, string) error
}

type stationDTO struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Country     string   `json:"country,omitempty"`
	CountryCode string   `json:"country_code,omitempty"`
	Language    string   `json:"language,omitempty"`
	Codec       string   `json:"codec,omitempty"`
	Bitrate     int      `json:"bitrate,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	HLS         bool     `json:"hls,omitempty"`
	IconURL     string   `json:"icon_url,omitempty"`
}

type countryDTO struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Stations int    `json:"stations"`
}

type radioLibraryDTO struct {
	Favorites []stationDTO `json:"favorites"`
	Recent    []stationDTO `json:"recent"`
}

// radioStore is the persisted form; it matches the GUI preference encoding.
type radioStore struct {
	Favorites []radiobrowser.Station `json:"favorites"`
	Recent    []radiobrowser.Station `json:"recent"`
}

// radioState resolves client station IDs to directory records. Browsers only
// ever send station UUIDs, so playback and icon fetches are limited to URLs
// the Radio Browser directory returned.
type radioState struct {
	dir       RadioDirectory
	ctx       context.Context
	cancel    context.CancelFunc
	workers   sync.WaitGroup
	icons     chan struct{}
	mu        sync.Mutex
	seen      map[string]radiobrowser.Station
	order     []string
	favorites []radiobrowser.Station
	recent    []radiobrowser.Station
	iconFails map[string]time.Time
	saveMu    sync.Mutex
}

func (h *Handler) initRadio() {
	dir := h.cfg.Radio
	if dir == nil {
		dir = radiobrowser.New("Go2TV/" + h.cfg.Version)
	}
	r := &h.radio
	r.dir, r.icons = dir, make(chan struct{}, 4)
	r.seen, r.iconFails = make(map[string]radiobrowser.Station), make(map[string]time.Time)
	r.ctx, r.cancel = context.WithCancel(context.Background())
	if h.cfg.RadioStorePath == "" {
		return
	}
	data, err := os.ReadFile(h.cfg.RadioStorePath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			h.warn("Radio favorites unavailable: " + err.Error())
		}
		return
	}
	var stored radioStore
	if err := json.Unmarshal(data, &stored); err != nil {
		h.warn("Radio favorites unreadable: " + err.Error())
		return
	}
	r.favorites, r.recent = validStations(stored.Favorites, maxRadioFavorites), validStations(stored.Recent, maxRadioRecent)
}

func validStations(stations []radiobrowser.Station, limit int) []radiobrowser.Station {
	result := make([]radiobrowser.Station, 0, min(len(stations), limit))
	for _, station := range stations {
		if validStationID(station.UUID) && station.Name != "" && station.StreamURL() != "" && len(result) < limit {
			result = append(result, station)
		}
	}
	return result
}

func (h *Handler) closeRadio() {
	h.radio.cancel()
	h.radio.workers.Wait()
}

func (h *Handler) warn(message string) {
	if h.cfg.Logger != nil {
		h.cfg.Logger.Warning(message)
	}
}

func validStationID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F' || r == '-') {
			return false
		}
	}
	return true
}

func toStationDTO(station radiobrowser.Station) stationDTO {
	dto := stationDTO{ID: station.UUID, Name: station.Name, Country: strings.TrimSpace(station.Country), CountryCode: strings.ToUpper(strings.TrimSpace(station.CountryCode)), Language: strings.TrimSpace(station.Language), Codec: strings.TrimSpace(station.Codec), Bitrate: max(station.Bitrate, 0), HLS: station.HLS == 1}
	// Directory country fields hold official long forms such as "The United
	// Kingdom Of Great Britain And Northern Ireland"; prefer common names.
	if region, err := language.ParseRegion(dto.CountryCode); err == nil {
		if name := display.English.Regions().Name(region); name != "" {
			dto.Country = name
		}
	}
	seen := make(map[string]bool)
	for tag := range strings.SplitSeq(station.Tags, ",") {
		tag = strings.TrimSpace(tag)
		if key := strings.ToLower(tag); tag != "" && !seen[key] && len(dto.Tags) < maxRadioRowTags {
			seen[key] = true
			dto.Tags = append(dto.Tags, tag)
		}
	}
	if strings.TrimSpace(station.Favicon) != "" {
		dto.IconURL = "/api/radio/icon?" + url.Values{"id": {station.UUID}}.Encode()
	}
	return dto
}

func toStationDTOs(stations []radiobrowser.Station) []stationDTO {
	result := make([]stationDTO, 0, len(stations))
	for _, station := range stations {
		result = append(result, toStationDTO(station))
	}
	return result
}

func (r *radioState) remember(stations []radiobrowser.Station) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, station := range stations {
		if _, ok := r.seen[station.UUID]; !ok {
			r.order = append(r.order, station.UUID)
		}
		r.seen[station.UUID] = station
	}
	for len(r.order) > maxRadioStations {
		delete(r.seen, r.order[0])
		r.order = r.order[1:]
	}
}

// lookup reports whether the station came from favorites or recent history,
// whose stream URLs may have changed since they were saved.
func (r *radioState) lookup(id string) (radiobrowser.Station, bool, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, list := range [][]radiobrowser.Station{r.favorites, r.recent} {
		if index := slices.IndexFunc(list, func(station radiobrowser.Station) bool { return station.UUID == id }); index >= 0 {
			return list[index], true, true
		}
	}
	station, ok := r.seen[id]
	return station, false, ok
}

func (r *radioState) library() radioLibraryDTO {
	r.mu.Lock()
	defer r.mu.Unlock()
	return radioLibraryDTO{Favorites: toStationDTOs(r.favorites), Recent: toStationDTOs(r.recent)}
}

// update replaces saved copies of a refreshed station.
func (r *radioState) update(station radiobrowser.Station) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, list := range [][]radiobrowser.Station{r.favorites, r.recent} {
		if index := slices.IndexFunc(list, func(saved radiobrowser.Station) bool { return saved.UUID == station.UUID }); index >= 0 {
			list[index] = station
		}
	}
	if _, ok := r.seen[station.UUID]; ok {
		r.seen[station.UUID] = station
	}
}

func (h *Handler) saveRadio() {
	if h.cfg.RadioStorePath == "" {
		return
	}
	h.radio.saveMu.Lock()
	defer h.radio.saveMu.Unlock()
	h.radio.mu.Lock()
	data, err := json.Marshal(radioStore{Favorites: h.radio.favorites, Recent: h.radio.recent})
	h.radio.mu.Unlock()
	if err == nil {
		err = writeFileAtomic(h.cfg.RadioStorePath, data)
	}
	if err != nil {
		h.warn("Radio favorites not saved: " + err.Error())
	}
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".radio-*.json")
	if err != nil {
		return err
	}
	_, err = temp.Write(data)
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(temp.Name())
	}
	return err
}

func (h *Handler) publishRadioLibrary() {
	h.saveRadio()
	h.hub.broadcast("state.radio", h.radio.library())
}

func (h *Handler) radioHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && (r.Method != http.MethodHead || r.URL.Path != "/api/radio/icon") {
		methodNotAllowed(w)
		return
	}
	switch r.URL.Path {
	case "/api/radio/stations":
		h.radioStations(w, r)
	case "/api/radio/countries":
		h.radioCountries(w, r)
	case "/api/radio/icon":
		h.radioIcon(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) radioStations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for key, values := range q {
		if (key != "q" && key != "country") || len(values) != 1 {
			apiError(w, http.StatusBadRequest, "invalid_request")
			return
		}
	}
	query, country := strings.TrimSpace(q.Get("q")), strings.ToUpper(q.Get("country"))
	if utf8.RuneCountInString(query) > maxRadioQuery || (country != "" && (len(country) != 2 || strings.Trim(country, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "")) {
		apiError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	stations, err := h.radio.dir.SearchText(r.Context(), query, country)
	if err != nil {
		h.radioDirectoryError(w, r, err)
		return
	}
	h.radio.remember(stations)
	writeJSON(w, http.StatusOK, struct {
		Stations []stationDTO `json:"stations"`
	}{toStationDTOs(stations)})
}

func (h *Handler) radioCountries(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		apiError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	records, err := h.radio.dir.Countries(r.Context())
	if err != nil {
		h.radioDirectoryError(w, r, err)
		return
	}
	countries := make([]countryDTO, 0, len(records))
	for _, country := range records {
		countries = append(countries, countryDTO{Code: country.Code, Name: country.Name, Stations: country.StationCount})
	}
	writeJSON(w, http.StatusOK, struct {
		Countries []countryDTO `json:"countries"`
	}{countries})
}

// radioDirectoryError names the failure class; details stay in the debug log.
func (h *Handler) radioDirectoryError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		return
	}
	if h.cfg.Logger != nil {
		h.cfg.Logger.Debug("Radio directory request failed: " + err.Error())
	}
	var netErr net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		apiError(w, http.StatusGatewayTimeout, "radio_timeout")
	case errors.Is(err, radiobrowser.ErrHTTPStatus):
		apiError(w, http.StatusBadGateway, "radio_unavailable")
	default:
		apiError(w, http.StatusBadGateway, "radio_unreachable")
	}
}

func (h *Handler) radioIcon(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id := q.Get("id")
	if len(q) != 1 || len(q["id"]) != 1 || !validStationID(id) {
		apiError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	station, _, ok := h.radio.lookup(id)
	if !ok || strings.TrimSpace(station.Favicon) == "" {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	asset, err := h.stationIcon(r.Context(), station.Favicon)
	if err != nil {
		if r.Context().Err() == nil {
			// Missing logos are common; let the browser keep its fallback.
			w.Header().Set("Cache-Control", "private, max-age=300")
			w.WriteHeader(http.StatusNotFound)
		}
		return
	}
	serveJPEG(w, r, asset.Data, "private, max-age=86400")
}

// stationIcon shares one normalized logo per favicon between station rows and
// renderer artwork. Failures are remembered so missing logos are not
// refetched for every listing.
func (h *Handler) stationIcon(ctx context.Context, favicon string) (*metadata.ArtworkAsset, error) {
	r := &h.radio
	r.mu.Lock()
	retryAt, failed := r.iconFails[favicon]
	r.mu.Unlock()
	if failed && time.Now().Before(retryAt) {
		return nil, errors.New("radio icon unavailable")
	}
	sum := sha256.Sum256([]byte(favicon))
	value, err := h.cfg.Artwork.Get(ctx, "radio-icon:"+hex.EncodeToString(sum[:16]), func(ctx context.Context) ([]byte, string, error) {
		select {
		case r.icons <- struct{}{}:
			defer func() { <-r.icons }()
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
		asset, err := radiobrowser.FetchIcon(ctx, favicon)
		if err != nil {
			if ctx.Err() == nil {
				r.mu.Lock()
				if len(r.iconFails) >= maxRadioIconFails {
					clear(r.iconFails)
				}
				r.iconFails[favicon] = time.Now().Add(radioIconRetry)
				r.mu.Unlock()
			}
			return nil, "", err
		}
		return asset.Data, asset.MIMEType, nil
	})
	if err != nil {
		return nil, err
	}
	return mediaartwork.AssetFromJPEG(value.Data, favicon)
}

func (h *Handler) radioCommand(ctx context.Context, message envelope) controller.Result {
	var p struct {
		StationID        string  `json:"station_id"`
		Favorite         *bool   `json:"favorite,omitempty"`
		ExpectedRevision *uint64 `json:"expected_revision"`
	}
	if readStrict(message.Payload, &p) != nil || !validStationID(p.StationID) || (message.Type == "radio.favorite") != (p.Favorite != nil) {
		return invalid(message.ID)
	}
	snapshot, err := h.cfg.Controller.Snapshot(ctx)
	if err != nil {
		return controller.Result{RequestID: message.ID, Code: controller.CodeInternal, Message: "snapshot failed"}
	}
	// Directory refreshes and the station probe take seconds while playback
	// keeps committing revisions, so the precondition applies on receipt.
	if p.ExpectedRevision != nil && *p.ExpectedRevision != snapshot.Revision {
		return controller.Result{RequestID: message.ID, Code: controller.CodeConflict, Revision: snapshot.Revision, Message: "state changed"}
	}
	station, saved, ok := h.radio.lookup(p.StationID)
	if message.Type == "radio.favorite" {
		if !ok {
			return invalid(message.ID)
		}
		h.setRadioFavorite(station, *p.Favorite)
		return h.torrentResult(ctx, message.ID)
	}
	if !ok || saved {
		refreshCtx, cancel := context.WithTimeout(ctx, radioRefreshTimeout)
		refreshed, err := h.radio.dir.Station(refreshCtx, p.StationID)
		cancel()
		switch {
		case err == nil:
			station, ok = refreshed, true
			h.radio.update(station)
		case h.cfg.Logger != nil:
			h.cfg.Logger.Debug("Radio station refresh failed: " + err.Error())
		}
	}
	if !ok {
		return controller.Result{RequestID: message.ID, Code: controller.CodeNotFound, Revision: snapshot.Revision, Message: "station unavailable"}
	}
	ref, err := h.radioMediaRef(ctx, station)
	if err != nil {
		if h.cfg.Logger != nil {
			h.cfg.Logger.Debug("Radio station probe failed: " + err.Error())
		}
		return controller.Result{RequestID: message.ID, Code: controller.CodeNotFound, Revision: snapshot.Revision, Message: "station offline or unreachable"}
	}
	mutation := controller.Mutation{RequestID: message.ID}
	// Without a renderer the station becomes the selection, ready for Play.
	if snapshot.SelectedDeviceID == "" {
		return h.cfg.Controller.SelectMedia(ctx, mutation, ref)
	}
	result := h.cfg.Controller.PlayMedia(ctx, mutation, ref)
	if result.OK() {
		h.recordRadioPlay(station)
	}
	return result
}

func (h *Handler) radioMediaRef(ctx context.Context, station radiobrowser.Station) (controller.MediaRef, error) {
	streamURL := station.StreamURL()
	probeCtx, cancel := context.WithTimeout(ctx, radioProbeTimeout)
	body, inferred, err := utils.StreamURLWithMime(probeCtx, streamURL)
	if body != nil {
		_ = body.Close()
	}
	cancel()
	if err != nil {
		return controller.MediaRef{}, err
	}
	hls := station.HLS == 1 || utils.IsHLSStream(streamURL, inferred)
	mediaType := station.MediaType()
	switch {
	case hls:
		mediaType = "application/vnd.apple.mpegurl"
	case strings.HasPrefix(strings.ToLower(inferred), "audio/"):
		mediaType = inferred
	}
	ref := controller.MediaRef{RootID: radioRootID, ID: station.UUID, Name: station.Name, Kind: mediamodel.MediaKindAudio, MIMEType: mediaType, Live: &controller.LiveStream{URL: streamURL, HLS: hls}}
	if favicon := station.Favicon; strings.TrimSpace(favicon) != "" {
		ref.LoadArtwork = func(ctx context.Context) (*metadata.ArtworkAsset, error) {
			asset, err := h.stationIcon(ctx, favicon)
			if err == nil {
				h.rememberArtwork(asset.ID, func(ctx context.Context) ([]byte, string, error) {
					asset, err := h.stationIcon(ctx, favicon)
					if err != nil {
						return nil, "", err
					}
					return asset.Data, asset.MIMEType, nil
				})
			}
			return asset, err
		}
	}
	return ref, nil
}

func (h *Handler) setRadioFavorite(station radiobrowser.Station, favorite bool) {
	r := &h.radio
	r.mu.Lock()
	index := slices.IndexFunc(r.favorites, func(saved radiobrowser.Station) bool { return saved.UUID == station.UUID })
	changed := false
	switch {
	case favorite && index < 0 && len(r.favorites) < maxRadioFavorites:
		r.favorites, changed = append(r.favorites, station), true
	case !favorite && index >= 0:
		r.favorites, changed = slices.Delete(r.favorites, index, index+1), true
	}
	r.mu.Unlock()
	if changed {
		h.publishRadioLibrary()
	}
}

// recordRadioPlay keeps recent stations most recent first and reports the
// cast to Radio Browser, whose popularity ranking depends on it.
func (h *Handler) recordRadioPlay(station radiobrowser.Station) {
	r := &h.radio
	r.mu.Lock()
	recent := []radiobrowser.Station{station}
	for _, previous := range r.recent {
		if previous.UUID != station.UUID && len(recent) < maxRadioRecent {
			recent = append(recent, previous)
		}
	}
	r.recent = recent
	r.mu.Unlock()
	h.publishRadioLibrary()
	r.workers.Go(func() {
		ctx, cancel := context.WithTimeout(r.ctx, radioClickTimeout)
		defer cancel()
		if err := r.dir.Click(ctx, station.UUID); err != nil && ctx.Err() == nil && h.cfg.Logger != nil {
			h.cfg.Logger.Debug("Radio play report failed: " + err.Error())
		}
	})
}
