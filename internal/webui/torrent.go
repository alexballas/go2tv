package webui

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"go2tv.app/go2tv/v2/internal/controller"
	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/internal/mediasource"
	"go2tv.app/go2tv/v2/internal/torrentstream"
)

const maxTorrentBytes = 4 << 20
const torrentMetadataTimeout = 2 * time.Minute

type torrentFileDTO struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
}

type torrentPendingDTO struct {
	ID     string           `json:"id"`
	Status string           `json:"status"`
	Files  []torrentFileDTO `json:"files"`
	Error  string           `json:"error,omitempty"`
}

type torrentActiveDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Completed int64  `json:"completed"`
	Total     int64  `json:"total"`
}

type torrentDTO struct {
	Pending *torrentPendingDTO `json:"pending,omitempty"`
	Active  *torrentActiveDTO  `json:"active,omitempty"`
}

type webTorrent struct {
	id      string
	cancel  context.CancelFunc
	session *torrentstream.Session
	status  string
	files   []torrentFileDTO
	err     string
	itemID  string
	name    string
}

// A pending metadata lookup leaves the current download/playback intact.
// opMu serializes replacement and cancellation; mu protects worker/status access.
type torrentState struct {
	opMu    sync.Mutex
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	workers sync.WaitGroup
	pending *webTorrent
	active  *webTorrent
}

func (h *Handler) torrentSnapshot() torrentDTO {
	h.torrents.mu.Lock()
	defer h.torrents.mu.Unlock()
	var result torrentDTO
	if pending := h.torrents.pending; pending != nil {
		result.Pending = &torrentPendingDTO{ID: pending.id, Status: pending.status, Files: slices.Clone(pending.files), Error: pending.err}
	}
	if active := h.torrents.active; active != nil {
		completed, total := active.session.Progress()
		result.Active = &torrentActiveDTO{ID: active.id, Name: active.name, Completed: completed, Total: total}
	}
	return result
}

func (h *Handler) torrentHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.torrentSnapshot())
	case http.MethodPost:
		// Unlike GETs, uploads must carry a same-origin, non-simple content type.
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || !strings.EqualFold(u.Host, r.Host) {
				apiError(w, http.StatusForbidden, "request_not_allowed")
				return
			}
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxTorrentBytes))
		if err != nil {
			apiError(w, http.StatusRequestEntityTooLarge, "torrent_too_large")
			return
		}
		var magnet string
		switch r.Header.Get("Content-Type") {
		case "application/json":
			var p struct {
				Magnet string `json:"magnet"`
			}
			if readStrict(body, &p) != nil || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(p.Magnet)), "magnet:") {
				apiError(w, http.StatusBadRequest, "invalid_magnet")
				return
			}
			magnet = strings.TrimSpace(p.Magnet)
		case "application/x-bittorrent", "application/octet-stream":
			if len(body) == 0 {
				apiError(w, http.StatusBadRequest, "invalid_torrent")
				return
			}
		default:
			apiError(w, http.StatusUnsupportedMediaType, "unsupported_media_type")
			return
		}
		if err := h.loadTorrent(r.Context(), magnet, body); err != nil {
			apiError(w, http.StatusServiceUnavailable, "torrent_unavailable")
			return
		}
		writeJSON(w, http.StatusAccepted, h.torrentSnapshot())
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) loadTorrent(ctx context.Context, magnet string, body []byte) error {
	h.torrents.opMu.Lock()
	defer h.torrents.opMu.Unlock()
	if err := h.torrents.ctx.Err(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return err
	}
	workerCTX, cancel := context.WithCancel(h.torrents.ctx)
	pending := &webTorrent{id: hex.EncodeToString(token), cancel: cancel, status: "loading"}
	h.torrents.mu.Lock()
	old := h.torrents.pending
	h.torrents.pending = pending
	h.torrents.mu.Unlock()
	closeWebTorrent(old)
	h.torrents.workers.Add(1)
	go func() {
		defer h.torrents.workers.Done()
		var session *torrentstream.Session
		var err error
		if magnet != "" {
			session, err = torrentstream.Open(workerCTX, magnet)
		} else {
			session, err = torrentstream.OpenReader(workerCTX, bytes.NewReader(body))
		}
		var files []torrentstream.File
		if err == nil {
			metadataCTX, stop := context.WithTimeout(workerCTX, torrentMetadataTimeout)
			files, err = session.Files(metadataCTX)
			stop()
		}
		h.torrents.mu.Lock()
		if workerCTX.Err() != nil || h.torrents.pending != pending {
			h.torrents.mu.Unlock()
			if session != nil {
				_ = session.Close()
			}
			return
		}
		if err != nil {
			pending.status, pending.err = "error", "Could not load torrent metadata. Check the torrent and available peers."
			if errors.Is(err, context.DeadlineExceeded) {
				pending.err = "Torrent metadata timed out. Try again when peers are available."
			}
		} else {
			pending.session, pending.status = session, "ready"
			for _, file := range files {
				pending.files = append(pending.files, torrentFileDTO{Index: file.Index, Name: file.Name, Size: file.Size})
			}
		}
		h.torrents.mu.Unlock()
		if err != nil {
			cancel()
			if session != nil {
				_ = session.Close()
			}
		}
	}()
	return nil
}

func closeWebTorrent(torrent *webTorrent) {
	if torrent == nil {
		return
	}
	torrent.cancel()
	if torrent.session != nil {
		_ = torrent.session.Close()
	}
}

func (h *Handler) torrentCommand(ctx context.Context, message envelope) controller.Result {
	var p struct {
		TorrentID        string  `json:"torrent_id"`
		Index            *int    `json:"index,omitempty"`
		ExpectedRevision *uint64 `json:"expected_revision"`
	}
	if readStrict(message.Payload, &p) != nil || p.TorrentID == "" || (message.Type == "torrent.select") != (p.Index != nil) {
		return invalid(message.ID)
	}
	h.torrents.opMu.Lock()
	defer h.torrents.opMu.Unlock()
	if h.torrents.ctx.Err() != nil {
		return invalid(message.ID)
	}
	snapshot, err := h.cfg.Controller.Snapshot(ctx)
	if err != nil {
		return invalid(message.ID)
	}
	if p.ExpectedRevision != nil && *p.ExpectedRevision != snapshot.Revision {
		return controller.Result{RequestID: message.ID, Code: controller.CodeConflict, Revision: snapshot.Revision, Message: "state changed"}
	}
	h.torrents.mu.Lock()
	pending, active := h.torrents.pending, h.torrents.active
	ready := pending != nil && pending.status == "ready"
	var files []torrentFileDTO
	if ready {
		files = slices.Clone(pending.files)
	}
	h.torrents.mu.Unlock()
	if message.Type == "torrent.cancel" {
		switch {
		case pending != nil && pending.id == p.TorrentID:
			h.torrents.mu.Lock()
			h.torrents.pending = nil
			h.torrents.mu.Unlock()
			closeWebTorrent(pending)
		case active != nil && active.id == p.TorrentID:
			if result := h.releaseActiveTorrent(ctx, active); !result.OK() {
				result.RequestID = message.ID
				return result
			}
		default:
			return invalid(message.ID)
		}
		return h.torrentResult(ctx, message.ID)
	}
	if pending == nil || pending.id != p.TorrentID || !ready {
		return invalid(message.ID)
	}
	index := slices.IndexFunc(files, func(file torrentFileDTO) bool { return file.Index == *p.Index })
	if index < 0 {
		return invalid(message.ID)
	}
	mutation := expectedMutation(message.ID, p.ExpectedRevision)
	if active != nil {
		result := h.releaseActiveTorrent(ctx, active)
		if !result.OK() {
			result.RequestID = message.ID
			return result
		}
		mutation.ExpectedRevision = &result.Revision
	}
	path, err := pending.session.Select(*p.Index)
	if err != nil {
		return invalid(message.ID)
	}
	queued := false
	defer func() {
		if !queued {
			pending.session.Deselect()
		}
	}()
	source, ok := mediasource.Lookup(path)
	if !ok {
		return invalid(message.ID)
	}
	open := func(ctx context.Context) (io.ReadSeekCloser, time.Time, error) {
		reader, err := source.Open(ctx)
		return reader, time.Time{}, err
	}
	name := filepath.Base(path)
	kind := mediamodel.MediaKindVideo
	if strings.HasPrefix(source.MIME(), "audio/") {
		kind = mediamodel.MediaKindAudio
	}
	ref := controller.MediaRef{RootID: "torrent", ID: pending.id, AbsolutePath: path, Name: name, Kind: kind, MIMEType: source.MIME(), OpenDirect: open, OpenTranscode: open}
	result := h.cfg.Controller.AddQueueItem(ctx, controller.QueueAddRequest{Mutation: mutation, Media: ref, Select: true})
	if !result.OK() {
		return result.Result
	}
	queued = true
	h.torrents.mu.Lock()
	pending.itemID, pending.name = result.ItemID, name
	h.torrents.active, h.torrents.pending = pending, nil
	h.torrents.mu.Unlock()
	return result.Result
}

// Release renderer readers/queue capabilities before deleting the cache.
func (h *Handler) releaseActiveTorrent(ctx context.Context, active *webTorrent) controller.Result {
	snapshot, err := h.cfg.Controller.Snapshot(ctx)
	if err != nil {
		return controller.Result{Code: controller.CodeInternal, Message: "snapshot failed"}
	}
	for _, item := range snapshot.Queue {
		if item.ID != active.itemID {
			continue
		}
		revision := snapshot.Revision
		selected := item.IsSelected
		if selected && !item.IsActive {
			// Selecting a torrent does not replace playback. Restore selection
			// to the playing item so removing this torrent leaves it running.
			for _, playing := range snapshot.Queue {
				if !playing.IsActive {
					continue
				}
				result := h.cfg.Controller.SelectQueueItem(ctx, controller.Mutation{ExpectedRevision: &revision}, playing.ID)
				if !result.OK() {
					return result
				}
				revision, selected = result.Revision, false
				break
			}
		}
		if item.IsActive || (selected && snapshot.PlaybackState != "STOPPED") {
			result := h.cfg.Controller.Stop(ctx, controller.Mutation{ExpectedRevision: &revision})
			if !result.OK() {
				return result
			}
			revision = result.Revision
		}
		if result := h.cfg.Controller.RemoveQueueItem(ctx, controller.Mutation{ExpectedRevision: &revision}, item.ID); !result.OK() {
			return result
		}
		break
	}
	h.torrents.mu.Lock()
	h.torrents.active = nil
	h.torrents.mu.Unlock()
	closeWebTorrent(active)
	return h.torrentResult(ctx, "")
}

func (h *Handler) torrentResult(ctx context.Context, id string) controller.Result {
	snapshot, err := h.cfg.Controller.Snapshot(ctx)
	if err != nil {
		return controller.Result{RequestID: id, Code: controller.CodeInternal, Message: "snapshot failed"}
	}
	return controller.Result{RequestID: id, Revision: snapshot.Revision}
}

func (h *Handler) closeTorrents() {
	h.torrents.opMu.Lock()
	h.torrents.mu.Lock()
	pending, active := h.torrents.pending, h.torrents.active
	h.torrents.pending = nil
	h.torrents.mu.Unlock()
	closeWebTorrent(pending)
	if active != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		result := h.releaseActiveTorrent(ctx, active)
		cancel()
		if !result.OK() {
			closeWebTorrent(active)
		}
	}
	h.torrents.cancel()
	h.torrents.opMu.Unlock()
	h.torrents.workers.Wait()
}
