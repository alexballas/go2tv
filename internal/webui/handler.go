package webui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"go2tv.app/go2tv/v2/internal/controller"
	"go2tv.app/go2tv/v2/internal/library"
	"go2tv.app/go2tv/v2/internal/mediaartwork"
	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/metadata"
	"go2tv.app/go2tv/v2/utils"
)

const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"

const maxArtworkRefs = controller.MaxQueueItems + 1

type Handler struct {
	cfg          Config
	hub          *hub
	artMu        sync.RWMutex
	artworkRefs  map[string]controller.ArtworkLoader
	artworkOrder []string
}

func New(cfg Config) (*Handler, error) {
	if cfg.Controller == nil || cfg.Library == nil {
		return nil, errors.New("webui dependencies required")
	}
	if cfg.Artwork == nil {
		cfg.Artwork = controller.NewArtworkCache(controller.ArtworkCacheBytes)
	}
	h := &Handler{cfg: cfg, artworkRefs: make(map[string]controller.ArtworkLoader)}
	h.hub = newHub(cfg.Controller, h.command)
	return h, nil
}

func (h *Handler) Close()           { h.hub.close() }
func (h *Handler) ServesBootstrap() {}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w.Header())
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case r.URL.Path == "/":
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		file, _ := fs.ReadFile(assets, "index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(file)
	case strings.HasPrefix(r.URL.Path, "/assets/"):
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			methodNotAllowed(w)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/assets/")
		if !validAssetName(name) {
			http.NotFound(w, r)
			return
		}
		data, err := fs.ReadFile(assets, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		if strings.HasSuffix(name, ".js") {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		} else {
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if r.Method == http.MethodGet {
			_, _ = w.Write(data)
		}
	case r.URL.Path == "/api/bootstrap":
		h.bootstrap(w, r)
	case r.URL.Path == "/api/library":
		h.browse(w, r)
	case r.URL.Path == "/api/thumbnail":
		h.libraryArtwork(w, r, true)
	case r.URL.Path == "/api/media-artwork":
		h.libraryArtwork(w, r, false)
	case strings.HasPrefix(r.URL.Path, "/api/artwork/"):
		h.artwork(w, r)
	case r.URL.Path == "/api/ws":
		h.hub.serve(w, r)
	default:
		http.NotFound(w, r)
	}
}

func securityHeaders(header http.Header) {
	header.Set("Content-Security-Policy", csp)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "same-origin")
	header.Set("Permissions-Policy", "accelerometer=(), autoplay=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()")
}

func validAssetName(name string) bool {
	parts := strings.Split(name, ".")
	if len(parts) != 3 || parts[0] != "app" || len(parts[1]) != 8 || (parts[2] != "js" && parts[2] != "css") {
		return false
	}
	_, err := hex.DecodeString(parts[1])
	return err == nil
}

func (h *Handler) bootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	snapshot, err := h.cfg.Controller.Snapshot(r.Context())
	if err != nil {
		apiError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	roots := h.cfg.Library.Roots()
	result := bootstrapDTO{ServerVersion: h.cfg.Version, ProtocolVersion: ProtocolVersion, AssetsHash: assetsHash, Snapshot: safeSnapshot(snapshot), Limits: map[string]int{"ws_message_bytes": maxMessageBytes, "ws_clients": maxClients, "ws_clients_per_ip": maxClientsPerIP, "queue_items": controller.MaxQueueItems, "library_page": library.MaxLimit}, Features: map[string]bool{"websocket": true, "artwork": true, "transcode": true, "gapless": true}}
	for _, root := range roots {
		result.Roots = append(result.Roots, rootDTO{ID: root.ID, Name: root.Name})
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) browse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	q := r.URL.Query()
	for key, values := range q {
		if (key != "root_id" && key != "parent_id" && key != "cursor" && key != "limit") || len(values) != 1 {
			apiError(w, http.StatusBadRequest, "invalid_request")
			return
		}
	}
	if len(q) > 4 {
		apiError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	limit := 0
	if q.Get("limit") != "" {
		var err error
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil {
			apiError(w, 400, "invalid_request")
			return
		}
	}
	page, err := h.cfg.Library.Browse(q.Get("root_id"), q.Get("parent_id"), q.Get("cursor"), limit)
	if err != nil {
		apiError(w, http.StatusBadRequest, libraryCode(err))
		return
	}
	entries := make([]entryDTO, 0, len(page.Entries))
	for _, entry := range page.Entries {
		dto := entryDTO{ID: entry.ID, Name: entry.Name, Kind: entry.Kind}
		if entry.Kind == "file" {
			kind := mediamodel.KindForPath(entry.Name)
			if kind != mediamodel.MediaKindUnknown {
				dto.MediaKind = string(kind)
				dto.ThumbnailURL = libraryArtworkURL("/api/thumbnail", q.Get("root_id"), entry.ID)
				dto.ArtworkURL = libraryArtworkURL("/api/media-artwork", q.Get("root_id"), entry.ID)
			}
		}
		entries = append(entries, dto)
	}
	writeJSON(w, http.StatusOK, struct {
		Entries []entryDTO `json:"entries"`
		Cursor  string     `json:"cursor,omitempty"`
	}{entries, page.Cursor})
}

func libraryArtworkURL(path, rootID, entryID string) string {
	query := url.Values{"root_id": []string{rootID}, "entry_id": []string{entryID}}
	return path + "?" + query.Encode()
}

func (h *Handler) libraryArtwork(w http.ResponseWriter, r *http.Request, thumbnail bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	rootID, entryID, ok := mediaQuery(r.URL.Query())
	if !ok {
		apiError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	file, meta, err := h.cfg.Library.OpenMedia(rootID, entryID)
	if err != nil {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	defer file.Close()
	kind := mediamodel.KindForPath(meta.Name)
	if kind == mediamodel.MediaKindUnknown {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	request := h.mediaArtworkRequest(rootID, entryID, meta.Name, kind, file)
	var data []byte
	if thumbnail {
		data, err = mediaartwork.ThumbnailWithResolver(r.Context(), request, func(ctx context.Context) (*metadata.ArtworkAsset, error) {
			return h.resolveArtworkRequest(ctx, request)
		})
	} else {
		var asset *metadata.ArtworkAsset
		asset, err = h.resolveArtworkRequest(r.Context(), request)
		if asset != nil {
			data = asset.Data
		}
	}
	if err != nil || len(data) == 0 {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	serveJPEG(w, r, data, "private, no-cache")
}

func mediaQuery(query url.Values) (string, string, bool) {
	if len(query) != 2 {
		return "", "", false
	}
	root, rootOK := query["root_id"]
	entry, entryOK := query["entry_id"]
	if !rootOK || !entryOK || len(root) != 1 || len(entry) != 1 || root[0] == "" || entry[0] == "" {
		return "", "", false
	}
	return root[0], entry[0], true
}

func (h *Handler) artwork(w http.ResponseWriter, r *http.Request) {
	// Artwork IDs are controller cache IDs, never filesystem identifiers.
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	if !strings.HasSuffix(r.URL.Path, ".jpg") {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/artwork/"), ".jpg")
	if id == "" || strings.Contains(id, "/") {
		apiError(w, 404, "not_found")
		return
	}
	value, err := h.loadArtwork(r.Context(), id)
	if err != nil {
		apiError(w, 404, "not_found")
		return
	}
	serveJPEG(w, r, value, "private, max-age=31536000, immutable")
}

func serveJPEG(w http.ResponseWriter, r *http.Request, data []byte, cacheControl string) {
	sum := sha256.Sum256(data)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if r.Method == http.MethodGet {
		_, _ = w.Write(data)
	}
}

func (h *Handler) loadArtwork(ctx context.Context, id string) ([]byte, error) {
	if value, ok := h.cfg.Artwork.Lookup(id); ok {
		return value.Data, nil
	}
	h.artMu.RLock()
	loader, ok := h.artworkRefs[id]
	h.artMu.RUnlock()
	if !ok {
		return nil, errors.New("not found")
	}
	value, err := h.cfg.Artwork.Get(ctx, id, loader)
	return value.Data, err
}

func safeSnapshot(s controller.Snapshot) snapshotDTO {
	result := snapshotDTO{Revision: s.Revision, SelectedDeviceID: s.SelectedDeviceID, ActiveDeviceID: s.ActiveDeviceID, SelectedMedia: s.SelectedMedia != "", SelectedMediaName: s.SelectedMedia, ActiveMediaName: s.ActiveMediaName, SelectedSubtitle: s.SelectedSubtitle != "", SelectedSubtitleName: s.SelectedSubtitle, Transcode: s.Transcode, HasSession: s.HasSession, PlaybackState: s.PlaybackState, Position: s.Position, Duration: s.Duration, Volume: s.Volume, Muted: s.Muted, MediaType: string(s.MediaType), ArtworkID: s.ArtworkID, Policy: s.Policy}
	result.Devices = make([]deviceDTO, 0, len(s.Devices))
	for _, d := range s.Devices {
		caps := []string{}
		if d.AudioOnly {
			caps = append(caps, "audio_only")
		}
		result.Devices = append(result.Devices, deviceDTO{ID: d.ID, Label: d.Name, Protocol: d.Protocol, Capabilities: caps})
	}
	result.Queue = make([]queueDTO, 0, len(s.Queue))
	for _, q := range s.Queue {
		result.Queue = append(result.Queue, queueDTO{ID: q.ID, Name: q.Name, Parent: q.Parent, Kind: string(q.MediaKind), Selected: q.IsSelected, Active: q.IsActive})
	}
	return result
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func apiError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{code})
}
func methodNotAllowed(w http.ResponseWriter) {
	apiError(w, http.StatusMethodNotAllowed, "method_not_allowed")
}
func libraryCode(err error) string {
	switch {
	case errors.Is(err, library.ErrCursorExpired):
		return "cursor_expired"
	case errors.Is(err, library.ErrCursorLimit):
		return "cursor_limit"
	case errors.Is(err, library.ErrInvalidLimit):
		return "invalid_limit"
	default:
		return "invalid_request"
	}
}

func readStrict(raw []byte, dst any) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing json")
	}
	return nil
}
func expectedMutation(id string, revision *uint64) controller.Mutation {
	return controller.Mutation{RequestID: id, ExpectedRevision: revision}
}
func (h *Handler) command(ctx context.Context, message envelope) (controller.Result, map[string]any) {
	var before controller.Snapshot
	if h.cfg.Logger != nil && knownAction(message.Type) {
		before, _ = h.cfg.Controller.Snapshot(ctx)
	}
	var result controller.Result
	var extra map[string]any
	// queue.add_many is dispatched here, not in executeCommand, because it is
	// the only command that acknowledges with extra payload fields.
	if message.Type == "queue.add_many" {
		result, extra = h.queueAddMany(ctx, message)
	} else {
		result = h.executeCommand(ctx, message)
	}
	if h.cfg.Logger == nil || !knownAction(message.Type) {
		return result, extra
	}
	if !result.OK() {
		if result.Code == controller.CodeConflict {
			return result, extra
		}
		h.cfg.Logger.Warning("WebUI action failed: " + actionName(message.Type) + " (" + result.Message + ")")
		return result, extra
	}
	snapshot, err := h.cfg.Controller.Snapshot(ctx)
	if err == nil {
		h.logAction(message.Type, before, snapshot)
	}
	return result, extra
}

func (h *Handler) queueAddMany(ctx context.Context, message envelope) (controller.Result, map[string]any) {
	var p struct {
		RootID           string   `json:"root_id"`
		EntryIDs         []string `json:"entry_ids"`
		ExpectedRevision *uint64  `json:"expected_revision"`
	}
	if readStrict(message.Payload, &p) != nil || len(p.EntryIDs) == 0 || len(p.EntryIDs) > controller.MaxQueueItems {
		return invalid(message.ID), nil
	}
	refs := make([]controller.MediaRef, 0, len(p.EntryIDs))
	failed := 0
	for _, entryID := range p.EntryIDs {
		ref, err := h.queuedMediaRef(p.RootID, entryID)
		if err != nil || ref.Kind == mediamodel.MediaKindUnknown {
			// Entries listed by the client can vanish or change before the
			// bulk add arrives; skip them instead of failing the batch.
			failed++
			continue
		}
		refs = append(refs, ref)
	}
	if len(refs) == 0 {
		return invalid(message.ID), nil
	}
	result := h.cfg.Controller.AddQueueItems(ctx, controller.QueueAddManyRequest{Mutation: expectedMutation(message.ID, p.ExpectedRevision), Items: refs})
	if !result.OK() {
		return result.Result, nil
	}
	return result.Result, map[string]any{"added": result.Added, "duplicates": result.Duplicates, "dropped": result.Dropped, "failed": failed}
}

func (h *Handler) executeCommand(ctx context.Context, message envelope) controller.Result {
	var common struct {
		ExpectedRevision *uint64 `json:"expected_revision"`
	}
	if len(message.Payload) > 0 && readStrict(message.Payload, &common) != nil { /* command-specific decoder follows */
	}
	switch message.Type {
	case "devices.refresh":
		var p struct {
			ExpectedRevision *uint64 `json:"expected_revision"`
		}
		if readStrict(message.Payload, &p) != nil {
			return invalid(message.ID)
		}
		return h.cfg.Controller.Refresh(ctx, expectedMutation(message.ID, p.ExpectedRevision))
	case "devices.select":
		var p struct {
			DeviceID         string  `json:"device_id"`
			ExpectedRevision *uint64 `json:"expected_revision"`
		}
		if readStrict(message.Payload, &p) != nil {
			return invalid(message.ID)
		}
		return h.cfg.Controller.SelectDevice(ctx, expectedMutation(message.ID, p.ExpectedRevision), p.DeviceID)
	case "library.play", "library.select_media", "library.select_subtitle":
		var p struct {
			RootID           string  `json:"root_id"`
			EntryID          string  `json:"entry_id"`
			ExpectedRevision *uint64 `json:"expected_revision"`
		}
		if readStrict(message.Payload, &p) != nil {
			return invalid(message.ID)
		}
		if message.Type == "library.play" || message.Type == "library.select_media" {
			ref, err := h.mediaRef(ctx, p.RootID, p.EntryID)
			if err != nil {
				return invalid(message.ID)
			}
			if message.Type == "library.play" {
				return h.cfg.Controller.QueueAndPlay(ctx, expectedMutation(message.ID, p.ExpectedRevision), ref)
			}
			return h.selectMedia(ctx, expectedMutation(message.ID, p.ExpectedRevision), ref)
		}
		ref, err := h.subtitleRef(p.RootID, p.EntryID)
		if err != nil {
			return invalid(message.ID)
		}
		return h.cfg.Controller.SelectSubtitle(ctx, expectedMutation(message.ID, p.ExpectedRevision), ref)
	case "library.clear_subtitle":
		var p struct {
			ExpectedRevision *uint64 `json:"expected_revision"`
		}
		if readStrict(message.Payload, &p) != nil {
			return invalid(message.ID)
		}
		return h.cfg.Controller.SelectSubtitle(ctx, expectedMutation(message.ID, p.ExpectedRevision), controller.SubtitleRef{})
	case "queue.add":
		var p struct {
			RootID           string  `json:"root_id"`
			EntryID          string  `json:"entry_id"`
			ExpectedRevision *uint64 `json:"expected_revision"`
		}
		if readStrict(message.Payload, &p) != nil {
			return invalid(message.ID)
		}
		ref, err := h.mediaRef(ctx, p.RootID, p.EntryID)
		if err != nil {
			return invalid(message.ID)
		}
		return h.cfg.Controller.AddQueueItem(ctx, controller.QueueAddRequest{Mutation: expectedMutation(message.ID, p.ExpectedRevision), Media: ref}).Result
	case "queue.select":
		var p struct {
			ItemID           string  `json:"item_id"`
			ExpectedRevision *uint64 `json:"expected_revision"`
		}
		if readStrict(message.Payload, &p) != nil {
			return invalid(message.ID)
		}
		return h.cfg.Controller.SelectQueueItem(ctx, expectedMutation(message.ID, p.ExpectedRevision), p.ItemID)
	case "queue.remove":
		var p struct {
			ItemID           string  `json:"item_id"`
			ExpectedRevision *uint64 `json:"expected_revision"`
		}
		if readStrict(message.Payload, &p) != nil {
			return invalid(message.ID)
		}
		return h.cfg.Controller.RemoveQueueItem(ctx, expectedMutation(message.ID, p.ExpectedRevision), p.ItemID)
	case "queue.move":
		var p struct {
			ItemID           string  `json:"item_id"`
			Delta            *int    `json:"delta"`
			ExpectedRevision *uint64 `json:"expected_revision"`
		}
		if readStrict(message.Payload, &p) != nil || p.Delta == nil || *p.Delta == 0 {
			return invalid(message.ID)
		}
		return h.cfg.Controller.MoveQueueItem(ctx, expectedMutation(message.ID, p.ExpectedRevision), p.ItemID, *p.Delta)
	case "queue.clear":
		return h.simplePayload(ctx, message, h.cfg.Controller.ClearQueue)
	case "player.play":
		var p struct {
			ItemID           string  `json:"item_id"`
			ExpectedRevision *uint64 `json:"expected_revision"`
		}
		if len(message.Payload) == 0 {
			message.Payload = []byte("{}")
		}
		if readStrict(message.Payload, &p) != nil {
			return invalid(message.ID)
		}
		if snapshot, err := h.cfg.Controller.Snapshot(ctx); err == nil && snapshot.HasSession && snapshot.PlaybackState == "PAUSED" {
			if p.ItemID == "" || slices.ContainsFunc(snapshot.Queue, func(item controller.QueueItem) bool {
				return item.ID == p.ItemID && item.IsActive
			}) {
				return h.cfg.Controller.Resume(ctx, expectedMutation(message.ID, p.ExpectedRevision))
			}
		}
		return h.cfg.Controller.Play(ctx, controller.PlayRequest{Mutation: expectedMutation(message.ID, p.ExpectedRevision), QueueItemID: p.ItemID})
	case "player.resume":
		return h.simplePayload(ctx, message, h.cfg.Controller.Resume)
	case "player.pause":
		return h.simplePayload(ctx, message, h.cfg.Controller.Pause)
	case "player.stop":
		return h.simplePayload(ctx, message, h.cfg.Controller.Stop)
	case "player.volume":
		var p struct {
			Volume           *int    `json:"volume"`
			Delta            *int    `json:"delta"`
			ExpectedRevision *uint64 `json:"expected_revision"`
		}
		if readStrict(message.Payload, &p) != nil || (p.Volume == nil) == (p.Delta == nil) {
			return invalid(message.ID)
		}
		if p.Delta != nil {
			if *p.Delta != -1 && *p.Delta != 1 {
				return invalid(message.ID)
			}
			return h.cfg.Controller.AdjustVolume(ctx, expectedMutation(message.ID, p.ExpectedRevision), *p.Delta)
		}
		if *p.Volume < 0 || *p.Volume > 100 {
			return invalid(message.ID)
		}
		return h.cfg.Controller.SetVolume(ctx, expectedMutation(message.ID, p.ExpectedRevision), *p.Volume)
	case "player.mute":
		var p struct {
			Muted            *bool   `json:"muted"`
			ExpectedRevision *uint64 `json:"expected_revision"`
		}
		if readStrict(message.Payload, &p) != nil || p.Muted == nil {
			return invalid(message.ID)
		}
		return h.cfg.Controller.SetMute(ctx, expectedMutation(message.ID, p.ExpectedRevision), *p.Muted)
	case "player.transcode":
		var p struct {
			Enabled          *bool   `json:"enabled"`
			ExpectedRevision *uint64 `json:"expected_revision"`
		}
		if readStrict(message.Payload, &p) != nil || p.Enabled == nil {
			return invalid(message.ID)
		}
		return h.cfg.Controller.SetTranscode(ctx, expectedMutation(message.ID, p.ExpectedRevision), *p.Enabled)
	case "playback.policy":
		var p struct {
			Policy           *controller.Policy `json:"policy"`
			ExpectedRevision *uint64            `json:"expected_revision"`
		}
		if readStrict(message.Payload, &p) != nil || p.Policy == nil {
			return invalid(message.ID)
		}
		return h.cfg.Controller.SetPolicy(ctx, controller.PolicyRequest{Mutation: expectedMutation(message.ID, p.ExpectedRevision), Policy: *p.Policy})
	case "player.seek":
		var p struct {
			Seconds          *int    `json:"seconds"`
			ExpectedRevision *uint64 `json:"expected_revision"`
		}
		if readStrict(message.Payload, &p) != nil || p.Seconds == nil || *p.Seconds < 0 {
			return invalid(message.ID)
		}
		return h.cfg.Controller.Seek(ctx, controller.SeekRequest{Mutation: expectedMutation(message.ID, p.ExpectedRevision), Seconds: *p.Seconds})
	default:
		return invalid(message.ID)
	}
}

func knownAction(kind string) bool {
	switch kind {
	case "devices.refresh", "devices.select", "library.play", "library.select_media", "library.select_subtitle", "library.clear_subtitle",
		"queue.add", "queue.add_many", "queue.select", "queue.remove", "queue.move", "queue.clear", "player.play", "player.resume",
		"player.pause", "player.stop", "player.volume", "player.mute", "player.transcode", "playback.policy", "player.seek":
		return true
	default:
		return false
	}
}

func actionName(kind string) string {
	return strings.ReplaceAll(kind, ".", " ")
}

func (h *Handler) logAction(kind string, before, snapshot controller.Snapshot) {
	message := ""
	switch kind {
	case "devices.refresh":
		message = "Device discovery refreshed"
	case "devices.select":
		for _, device := range snapshot.Devices {
			if device.ID == snapshot.SelectedDeviceID {
				message = "Device selected: " + device.Name
				break
			}
		}
	case "library.select_media":
		message = "Media selected: " + snapshot.SelectedMedia
	case "library.play":
		message = "Media played: " + snapshot.SelectedMedia
	case "library.select_subtitle":
		message = "Subtitles selected: " + snapshot.SelectedSubtitle
	case "library.clear_subtitle":
		message = "Subtitles cleared"
	case "queue.add":
		for _, item := range snapshot.Queue {
			if !slices.ContainsFunc(before.Queue, func(previous controller.QueueItem) bool { return previous.ID == item.ID }) {
				message = "Media added to queue: " + item.Name
				break
			}
		}
	case "queue.add_many":
		if added := len(snapshot.Queue) - len(before.Queue); added > 0 {
			message = fmt.Sprintf("Media added to queue: %d files", added)
		}
	case "queue.select":
		message = "Queue item selected: " + snapshot.SelectedMedia
	case "queue.remove":
		for _, item := range before.Queue {
			if !slices.ContainsFunc(snapshot.Queue, func(current controller.QueueItem) bool { return current.ID == item.ID }) {
				message = "Queue item removed: " + item.Name
				break
			}
		}
	case "queue.move":
		message = "Queue reordered"
	case "queue.clear":
		message = "Queue cleared"
	case "player.volume":
		message = fmt.Sprintf("Volume set to %d%%", snapshot.Volume)
	case "player.mute":
		if snapshot.Muted {
			message = "Audio muted"
		} else {
			message = "Audio unmuted"
		}
	case "player.transcode":
		if snapshot.Transcode {
			message = "Transcoding enabled"
		} else {
			message = "Transcoding disabled"
		}
	case "playback.policy":
		message = "Playback options updated"
	}
	if message != "" {
		h.cfg.Logger.Info(message)
	}
}

func (h *Handler) selectMedia(ctx context.Context, mutation controller.Mutation, ref controller.MediaRef) controller.Result {
	snapshot, err := h.cfg.Controller.Snapshot(ctx)
	if err != nil {
		return controller.Result{RequestID: mutation.RequestID, Code: controller.CodeInternal, Message: "snapshot failed"}
	}
	if queuedPlaybackActive(snapshot) {
		return h.cfg.Controller.QueueAndPlay(ctx, mutation, ref)
	}
	if len(snapshot.Queue) == 0 {
		return h.cfg.Controller.AddQueueItem(ctx, controller.QueueAddRequest{Mutation: mutation, Media: ref, Select: true}).Result
	}
	return h.cfg.Controller.AddQueueItem(ctx, controller.QueueAddRequest{Mutation: mutation, Media: ref, Select: true}).Result
}

func queuedPlaybackActive(snapshot controller.Snapshot) bool {
	return snapshot.PlaybackState == "PLAYING" && slices.ContainsFunc(snapshot.Queue, func(item controller.QueueItem) bool { return item.IsActive })
}

func (h *Handler) simplePayload(ctx context.Context, m envelope, fn func(context.Context, controller.Mutation) controller.Result) controller.Result {
	var p struct {
		ExpectedRevision *uint64 `json:"expected_revision"`
	}
	if len(m.Payload) == 0 {
		m.Payload = []byte("{}")
	}
	if readStrict(m.Payload, &p) != nil {
		return invalid(m.ID)
	}
	return fn(ctx, expectedMutation(m.ID, p.ExpectedRevision))
}
func invalid(id string) controller.Result {
	return controller.Result{RequestID: id, Code: controller.CodeInvalid, Message: "invalid request"}
}

func (h *Handler) mediaRef(_ context.Context, rootID, entryID string) (controller.MediaRef, error) {
	return h.buildMediaRef(rootID, entryID)
}

func (h *Handler) queuedMediaRef(rootID, entryID string) (controller.MediaRef, error) {
	return h.buildMediaRef(rootID, entryID)
}

func (h *Handler) buildMediaRef(rootID, entryID string) (controller.MediaRef, error) {
	file, meta, err := h.cfg.Library.OpenMedia(rootID, entryID)
	if err != nil {
		return controller.MediaRef{}, err
	}
	kind := mediamodel.KindForPath(meta.Name)
	mediaType := detectMediaType(file)
	_ = file.Close()
	open := h.opener(rootID, entryID)
	return controller.MediaRef{RootID: rootID, ID: entryID, AbsolutePath: meta.AbsolutePath(), Name: meta.Name, Kind: kind, MIMEType: mediaType, OpenDirect: open, OpenTranscode: open, LoadArtwork: h.mediaArtworkLoader(rootID, entryID, meta.Name, kind)}, nil
}

func detectMediaType(file *os.File) string {
	head := make([]byte, 261)
	n, err := file.ReadAt(head, 0)
	if (err != nil && !errors.Is(err, io.EOF)) || n == 0 {
		return ""
	}
	mediaType, err := utils.GetMimeDetailsFromBytes(head[:n])
	if err != nil || mediaType == "/" {
		return ""
	}
	return mediaType
}

func (h *Handler) subtitleRef(rootID, entryID string) (controller.SubtitleRef, error) {
	file, meta, err := h.cfg.Library.OpenMedia(rootID, entryID)
	if err != nil {
		return controller.SubtitleRef{}, err
	}
	_ = file.Close()
	return controller.SubtitleRef{RootID: rootID, ID: entryID, Name: meta.Name, Open: h.opener(rootID, entryID)}, nil
}
func (h *Handler) opener(rootID, entryID string) playback.SourceOpener {
	return func(context.Context) (io.ReadSeekCloser, time.Time, error) {
		file, _, err := h.cfg.Library.OpenMedia(rootID, entryID)
		if err != nil {
			return nil, time.Time{}, err
		}
		info, err := file.Stat()
		if err != nil {
			_ = file.Close()
			return nil, time.Time{}, err
		}
		return file, info.ModTime(), nil
	}
}
func (h *Handler) resolveArtworkRequest(ctx context.Context, request mediaartwork.Request) (*metadata.ArtworkAsset, error) {
	id, err := mediaartwork.CacheID(request)
	if err != nil {
		return mediaartwork.Resolve(ctx, request)
	}
	value, err := h.cfg.Artwork.Get(ctx, id, func(ctx context.Context) ([]byte, string, error) {
		asset, resolveErr := mediaartwork.Resolve(ctx, request)
		if resolveErr != nil {
			return nil, "", resolveErr
		}
		if asset == nil {
			return nil, "", errors.New("artwork not found")
		}
		return asset.Data, asset.MIMEType, nil
	})
	if err != nil {
		return nil, err
	}
	return mediaartwork.AssetFromJPEG(value.Data, request.Path)
}

func (h *Handler) mediaArtworkRequest(rootID, mediaID, mediaName string, kind mediamodel.MediaKind, media *os.File) mediaartwork.Request {
	return mediaartwork.Request{
		Path:       media.Name(),
		Kind:       kind,
		FFmpegPath: h.cfg.FFmpegPath,
		File:       media,
		ResolveAudio: func() (*metadata.ArtworkAsset, error) {
			return h.resolveAudioArtwork(rootID, mediaID, mediaName, media), nil
		},
	}
}

func (h *Handler) resolveAudioArtwork(rootID, mediaID, mediaName string, media *os.File) *metadata.ArtworkAsset {
	for _, ext := range []string{".jpg", ".jpeg", ".png"} {
		if asset := h.loadArtworkFile(func() (*os.File, error) {
			file, _, err := h.cfg.Library.OpenArtwork(rootID, mediaID, ext)
			return file, err
		}, mediaName+ext); asset != nil {
			return asset
		}
	}
	if asset, _ := metadata.ResolveEmbeddedArtwork(media, mediaName); asset != nil {
		return asset
	}
	for _, base := range []string{"cover", "folder", "front", "albumart", "album", "artwork", "albumartlarge", "albumartsmall", "thumb"} {
		for _, ext := range []string{".jpg", ".jpeg", ".png"} {
			name := base + ext
			if asset := h.loadArtworkFile(func() (*os.File, error) {
				file, _, err := h.cfg.Library.OpenNamedArtwork(rootID, mediaID, name)
				return file, err
			}, name); asset != nil {
				return asset
			}
		}
	}
	return nil
}

func (h *Handler) loadArtworkFile(open func() (*os.File, error), source string) *metadata.ArtworkAsset {
	file, err := open()
	if err != nil {
		return nil
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (20<<20)+1))
	if err != nil {
		return nil
	}
	asset, err := metadata.LoadArtwork(data, source)
	if err != nil {
		return nil
	}
	return asset
}

func (h *Handler) mediaArtworkLoader(rootID, mediaID, mediaName string, kind mediamodel.MediaKind) controller.MediaArtworkLoader {
	return func(ctx context.Context) (*metadata.ArtworkAsset, error) {
		media, _, err := h.cfg.Library.OpenMedia(rootID, mediaID)
		if err != nil {
			return nil, err
		}
		defer media.Close()
		asset, err := h.resolveArtworkRequest(ctx, h.mediaArtworkRequest(rootID, mediaID, mediaName, kind, media))
		if err == nil && asset != nil {
			h.rememberArtwork(asset.ID, h.artworkLoader(rootID, mediaID, mediaName, kind))
		}
		return asset, err
	}
}

func (h *Handler) artworkLoader(rootID, mediaID, mediaName string, kind mediamodel.MediaKind) controller.ArtworkLoader {
	return func(ctx context.Context) ([]byte, string, error) {
		media, _, err := h.cfg.Library.OpenMedia(rootID, mediaID)
		if err != nil {
			return nil, "", err
		}
		defer media.Close()
		asset, err := mediaartwork.Resolve(ctx, h.mediaArtworkRequest(rootID, mediaID, mediaName, kind, media))
		if err != nil {
			return nil, "", err
		}
		if asset == nil {
			return nil, "", errors.New("artwork not found")
		}
		return asset.Data, asset.MIMEType, nil
	}
}

func (h *Handler) rememberArtwork(id string, loader controller.ArtworkLoader) {
	if id == "" || loader == nil {
		return
	}
	h.artMu.Lock()
	defer h.artMu.Unlock()
	if _, ok := h.artworkRefs[id]; ok {
		h.artworkRefs[id] = loader
		return
	}
	h.artworkRefs[id] = loader
	h.artworkOrder = append(h.artworkOrder, id)
	if len(h.artworkOrder) > maxArtworkRefs {
		delete(h.artworkRefs, h.artworkOrder[0])
		h.artworkOrder = h.artworkOrder[1:]
	}
}
