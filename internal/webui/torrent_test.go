package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"go2tv.app/go2tv/v2/internal/controller"
	"go2tv.app/go2tv/v2/internal/library"
	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/internal/mediaserver"
	"go2tv.app/go2tv/v2/internal/mediasource"
	"go2tv.app/go2tv/v2/internal/playback"
)

func webTorrentFixture(t *testing.T) []byte {
	t.Helper()
	info := metainfo.Info{Name: "Bundle", PieceLength: 16384, Pieces: make([]byte, 20), Files: []metainfo.FileInfo{
		{Length: 10, Path: []string{"notes.txt"}},
		{Length: 10, Path: []string{"movie.ts"}},
		{Length: 10, Path: []string{"song.opus"}},
	}}
	encoded, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	if err := (&metainfo.MetaInfo{InfoBytes: encoded}).Write(&body); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func loadWebTorrent(t *testing.T, h *Handler) *torrentPendingDTO {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/torrent", bytes.NewReader(webTorrentFixture(t)))
	r.Header.Set("Content-Type", "application/x-bittorrent")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("load = %d: %s", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state := h.torrentSnapshot()
		if state.Pending != nil && state.Pending.Status != "loading" {
			if state.Pending.Status != "ready" {
				t.Fatalf("metadata = %#v", state.Pending)
			}
			return state.Pending
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("metadata not ready")
	return nil
}

func webTorrentCommand(h *Handler, kind, id string, index *int) controller.Result {
	payload, _ := json.Marshal(struct {
		TorrentID string `json:"torrent_id"`
		Index     *int   `json:"index,omitempty"`
	}{id, index})
	result, _ := h.command(context.Background(), envelope{ID: "test", Type: kind, Payload: payload})
	return result
}

func TestTorrentUploadSelectionReplacementAndCleanup(t *testing.T) {
	h, _, control, _ := testHandler(t)
	pending := loadWebTorrent(t, h)
	if len(pending.Files) != 2 || pending.Files[0].Index != 1 || pending.Files[1].Index != 2 {
		t.Fatalf("media choices = %#v", pending.Files)
	}
	snapshot, _ := control.Snapshot(context.Background())
	stalePayload, _ := json.Marshal(map[string]any{"torrent_id": pending.ID, "index": 1, "expected_revision": snapshot.Revision + 1})
	conflict, _ := h.command(context.Background(), envelope{ID: "stale", Type: "torrent.select", Payload: stalePayload})
	if conflict.Code != controller.CodeConflict || h.torrentSnapshot().Active != nil {
		t.Fatalf("stale selection = %#v", conflict)
	}
	index := 0 // Non-media original index must be rejected.
	if result := webTorrentCommand(h, "torrent.select", pending.ID, &index); result.OK() {
		t.Fatal("accepted non-media file")
	}
	index = 2 // Opus is supported by torrents, outside the library extension list.
	if result := webTorrentCommand(h, "torrent.select", pending.ID, &index); !result.OK() {
		t.Fatal(result)
	}
	snapshot, _ = control.Snapshot(context.Background())
	if snapshot.SelectedMedia != "song.opus" || len(snapshot.Queue) != 1 {
		t.Fatalf("selected media = %#v", snapshot)
	}
	h.torrents.mu.Lock()
	old := h.torrents.active
	session := old.session
	h.torrents.mu.Unlock()
	path, err := session.Select(index)
	if err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Dir(filepath.Dir(filepath.Dir(path)))
	if _, ok := mediasource.Lookup(path); !ok {
		t.Fatal("progressive source unavailable")
	}
	// Loading and cancelling another metadata lookup preserve the active source.
	pending = loadWebTorrent(t, h)
	if h.torrentSnapshot().Active.ID != old.id {
		t.Fatal("metadata lookup replaced playback")
	}
	if result := webTorrentCommand(h, "torrent.cancel", pending.ID, nil); !result.OK() {
		t.Fatal(result)
	}
	if _, ok := mediasource.Lookup(path); !ok {
		t.Fatal("cancelling pending metadata removed active media")
	}
	pending = loadWebTorrent(t, h)
	index = 1
	if result := webTorrentCommand(h, "torrent.select", pending.ID, &index); !result.OK() {
		t.Fatal(result)
	}
	if _, ok := mediasource.Lookup(path); ok {
		t.Fatal("replaced source remains registered")
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("replaced cache remains: %v", err)
	}
	snapshot, _ = control.Snapshot(context.Background())
	if len(snapshot.Queue) != 1 || snapshot.SelectedMedia != "movie.ts" {
		t.Fatalf("replacement queue = %#v", snapshot.Queue)
	}
	if result := webTorrentCommand(h, "torrent.cancel", old.id, nil); result.OK() {
		t.Fatal("stale browser cancelled replacement")
	}
	if result := webTorrentCommand(h, "torrent.cancel", pending.ID, nil); !result.OK() {
		t.Fatal(result)
	}
	snapshot, _ = control.Snapshot(context.Background())
	if len(snapshot.Queue) != 0 || snapshot.SelectedMedia != "" || h.torrentSnapshot().Active != nil {
		t.Fatalf("cancel did not clear selection: %#v", snapshot)
	}
	pending = loadWebTorrent(t, h)
	if result := webTorrentCommand(h, "torrent.select", pending.ID, &index); !result.OK() {
		t.Fatal(result)
	}
	h.torrents.mu.Lock()
	session = h.torrents.active.session
	h.torrents.mu.Unlock()
	path, err = session.Select(index)
	if err != nil {
		t.Fatal(err)
	}
	h.Close()
	if _, ok := mediasource.Lookup(path); ok {
		t.Fatal("shutdown retained progressive source")
	}
	cacheDir = filepath.Dir(filepath.Dir(filepath.Dir(path)))
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("shutdown retained cache: %v", err)
	}
}

func TestTorrentMetadataCancellationAndHTTPValidation(t *testing.T) {
	h, _, _, _ := testHandler(t)
	tt := []struct {
		name, method, contentType, body, origin string
		want                                    int
	}{
		{name: "invalid magnet", method: "POST", contentType: "application/json", body: `{"magnet":"/etc/passwd"}`, want: 400},
		{name: "unknown fields", method: "POST", contentType: "application/json", body: `{"magnet":"magnet:x","path":"/etc/passwd"}`, want: 400},
		{name: "form rejected", method: "POST", contentType: "text/plain", body: "magnet:x", want: 415},
		{name: "cross origin", method: "POST", contentType: "application/json", origin: "http://evil.test", body: `{"magnet":"magnet:x"}`, want: 403},
		{name: "oversize", method: "POST", contentType: "application/x-bittorrent", body: strings.Repeat("x", maxTorrentBytes+1), want: 413},
		{name: "empty upload", method: "POST", contentType: "application/x-bittorrent", want: 400},
		{name: "unsupported method", method: "DELETE", want: 405},
	}
	for _, test := range tt {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(test.method, "/api/torrent", strings.NewReader(test.body))
			r.Header.Set("Content-Type", test.contentType)
			r.Header.Set("Origin", test.origin)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != test.want {
				t.Fatalf("response = %d, want %d: %s", w.Code, test.want, w.Body.String())
			}
		})
	}
	// No peers exist for this made-up hash: cancellation must interrupt metadata.
	r := httptest.NewRequest(http.MethodPost, "/api/torrent", strings.NewReader(`{"magnet":"magnet:?xt=urn:btih:0123456789012345678901234567890123456789"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 202 {
		t.Fatalf("magnet = %d", w.Code)
	}
	pending := h.torrentSnapshot().Pending
	if pending == nil || pending.Status != "loading" {
		t.Fatalf("pending = %#v", pending)
	}
	if result := webTorrentCommand(h, "torrent.cancel", pending.ID, nil); !result.OK() {
		t.Fatal(result)
	}
	done := make(chan struct{})
	go func() { h.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("metadata cancellation/shutdown blocked")
	}
}

type torrentDiscovery struct{ device playback.Device }

func (d torrentDiscovery) Start(context.Context)         {}
func (d torrentDiscovery) Refresh(context.Context) error { return nil }
func (d torrentDiscovery) Snapshot() []playback.Device   { return []playback.Device{d.device} }
func (d torrentDiscovery) Subscribe(int) (<-chan []playback.Device, func()) {
	updates := make(chan []playback.Device, 1)
	updates <- d.Snapshot()
	return updates, func() {}
}

type torrentTransport struct {
	load  playback.LoadRequest
	stops atomic.Int32
}

func (f *torrentTransport) Open(context.Context, playback.Device) (playback.Transport, error) {
	return f, nil
}
func (f *torrentTransport) Load(_ context.Context, request playback.LoadRequest) error {
	f.load = request
	return nil
}
func (f *torrentTransport) Play(context.Context) error           { return nil }
func (f *torrentTransport) Pause(context.Context) error          { return nil }
func (f *torrentTransport) Stop(context.Context) error           { f.stops.Add(1); return nil }
func (f *torrentTransport) Close(context.Context) error          { return nil }
func (f *torrentTransport) Volume(context.Context) (int, error)  { return 25, nil }
func (f *torrentTransport) SetVolume(context.Context, int) error { return nil }
func (f *torrentTransport) SetMute(context.Context, bool) error  { return nil }

func TestTorrentRendererHEADAndCancelBlockedRange(t *testing.T) {
	for _, protocol := range []string{"DLNA", "Chromecast"} {
		t.Run(protocol, func(t *testing.T) {
			lib, err := library.Open(library.Config{Roots: []string{t.TempDir()}})
			if err != nil {
				t.Fatal(err)
			}
			transport := &torrentTransport{}
			server := mediaserver.New(mediaserver.Config{ListenAddr: "127.0.0.1:0"})
			control := controller.New(controller.Config{Discovery: torrentDiscovery{playback.Device{ID: "renderer", Protocol: protocol, Endpoint: "http://127.0.0.1:8009"}}, TransportFactory: transport, MediaServer: server})
			h, err := New(Config{Controller: control, Library: lib})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { h.Close(); control.Close(); _ = lib.Close() }()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for {
				snapshot, err := control.Snapshot(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if len(snapshot.Devices) > 0 {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(time.Millisecond):
				}
			}
			if result := control.SelectDevice(ctx, controller.Mutation{}, "renderer"); !result.OK() {
				t.Fatal(result)
			}
			pending := loadWebTorrent(t, h)
			index := 1
			if result := webTorrentCommand(h, "torrent.select", pending.ID, &index); !result.OK() {
				t.Fatal(result)
			}
			if result := control.Play(ctx, controller.PlayRequest{}); !result.OK() {
				t.Fatal(result)
			}
			if transport.load.MediaType != "video/mp2t" || !transport.load.Seekable {
				t.Fatalf("load = %#v", transport.load)
			}
			r, _ := http.NewRequestWithContext(ctx, "HEAD", transport.load.MediaURL, nil)
			response, err := http.DefaultClient.Do(r)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 200 || response.ContentLength != 10 {
				t.Fatalf("HEAD = %d, %d", response.StatusCode, response.ContentLength)
			}
			readDone := make(chan error, 1)
			go func() {
				r, _ := http.NewRequestWithContext(ctx, "GET", transport.load.MediaURL, nil)
				r.Header.Set("Range", "bytes=5-9")
				response, err := http.DefaultClient.Do(r)
				if err == nil {
					defer response.Body.Close()
					_, err = io.ReadAll(response.Body)
					if err == nil {
						err = fmt.Errorf("range unexpectedly completed: %d", response.StatusCode)
					}
				}
				readDone <- err
			}()
			select {
			case err := <-readDone:
				t.Fatalf("missing pieces didn't buffer: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			if result := webTorrentCommand(h, "torrent.cancel", pending.ID, nil); !result.OK() {
				t.Fatal(result)
			}
			select {
			case <-readDone:
			case <-ctx.Done():
				t.Fatal("cancel didn't release renderer reader")
			}
			if transport.stops.Load() == 0 {
				t.Fatal("cancel didn't stop renderer")
			}
			snapshot, _ := control.Snapshot(ctx)
			if snapshot.HasSession || len(snapshot.Queue) != 0 || snapshot.SelectedMedia != "" {
				t.Fatalf("cancelled state = %#v", snapshot)
			}
		})
	}
}

func torrentTestLocalMedia(t *testing.T) controller.MediaRef {
	t.Helper()
	path := filepath.Join(t.TempDir(), "local.mp4")
	if err := os.WriteFile(path, []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	return controller.MediaRef{RootID: "local", ID: "local", Name: "local.mp4", Kind: mediamodel.MediaKindVideo, MIMEType: "video/mp4", OpenDirect: func(context.Context) (io.ReadSeekCloser, time.Time, error) {
		file, err := os.Open(path)
		return file, time.Time{}, err
	}}
}

func TestTorrentQueueLimitRollsBackDownloadAndAllowsRetry(t *testing.T) {
	h, _, control, _ := testHandler(t)
	ctx := context.Background()
	media := torrentTestLocalMedia(t)
	items := make([]controller.MediaRef, controller.MaxQueueItems)
	for i := range items {
		items[i] = media
		items[i].ID = fmt.Sprint(i)
	}
	if result := control.AddQueueItems(ctx, controller.QueueAddManyRequest{Items: items}); !result.OK() {
		t.Fatal(result)
	}
	pending := loadWebTorrent(t, h)
	index := 1
	if result := webTorrentCommand(h, "torrent.select", pending.ID, &index); result.Code != controller.CodeQueueLimit {
		t.Fatalf("selection = %#v", result)
	}
	state := h.torrentSnapshot()
	if state.Active != nil || state.Pending == nil || state.Pending.ID != pending.ID || state.Pending.Status != "ready" {
		t.Fatalf("rejected selection = %#v", state)
	}
	if completed, total := h.torrents.pending.session.Progress(); completed != 0 || total != 0 {
		t.Fatalf("rejected selection still downloading: %d/%d", completed, total)
	}
	if result := control.ClearQueue(ctx, controller.Mutation{}); !result.OK() {
		t.Fatal(result)
	}
	if result := webTorrentCommand(h, "torrent.select", pending.ID, &index); !result.OK() {
		t.Fatal(result)
	}
	snapshot, err := control.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Queue) != 1 || snapshot.SelectedMedia != "movie.ts" || h.torrentSnapshot().Active == nil {
		t.Fatalf("retry did not queue torrent: %#v", snapshot)
	}
}

func TestTorrentCancellationPreservesUnrelatedPlayback(t *testing.T) {
	tt := []struct {
		name, protocol string
		paused         bool
	}{
		{name: "playing DLNA", protocol: "DLNA"},
		{name: "paused DLNA", protocol: "DLNA", paused: true},
		{name: "playing Chromecast", protocol: "Chromecast"},
		{name: "paused Chromecast", protocol: "Chromecast", paused: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			lib, err := library.Open(library.Config{Roots: []string{t.TempDir()}})
			if err != nil {
				t.Fatal(err)
			}
			transport := &torrentTransport{}
			server := mediaserver.New(mediaserver.Config{ListenAddr: "127.0.0.1:0"})
			control := controller.New(controller.Config{Discovery: torrentDiscovery{playback.Device{ID: "renderer", Protocol: tc.protocol, Endpoint: "http://127.0.0.1:8009"}}, TransportFactory: transport, MediaServer: server})
			h, err := New(Config{Controller: control, Library: lib})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { h.Close(); control.Close(); _ = lib.Close() }()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for {
				snapshot, err := control.Snapshot(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if len(snapshot.Devices) > 0 {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if result := control.SelectDevice(ctx, controller.Mutation{}, "renderer"); !result.OK() {
				t.Fatal(result)
			}
			local := control.AddQueueItem(ctx, controller.QueueAddRequest{Media: torrentTestLocalMedia(t), Select: true})
			if !local.OK() {
				t.Fatal(local)
			}
			if result := control.Play(ctx, controller.PlayRequest{}); !result.OK() {
				t.Fatal(result)
			}
			if tc.paused {
				if result := control.Pause(ctx, controller.Mutation{}); !result.OK() {
					t.Fatal(result)
				}
			}
			pending := loadWebTorrent(t, h)
			index := 1
			if result := webTorrentCommand(h, "torrent.select", pending.ID, &index); !result.OK() {
				t.Fatal(result)
			}
			before, err := control.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if result := webTorrentCommand(h, "torrent.cancel", pending.ID, nil); !result.OK() {
				t.Fatal(result)
			}
			after, err := control.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !after.HasSession || after.PlaybackState != before.PlaybackState || after.ActiveMediaName != "local.mp4" || after.Generation != before.Generation || transport.stops.Load() != 0 {
				t.Fatalf("torrent cancellation interrupted playback: %#v", after)
			}
			if len(after.Queue) != 1 || after.Queue[0].ID != local.ItemID || !after.Queue[0].IsSelected || !after.Queue[0].IsActive || h.torrentSnapshot().Active != nil {
				t.Fatalf("torrent cancellation did not restore local selection: %#v", after)
			}
		})
	}
}
