package webui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"go2tv.app/go2tv/v2/internal/controller"
	"go2tv.app/go2tv/v2/internal/library"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/metadata"
)

func testHandler(t *testing.T) (*Handler, *library.Library, *controller.Controller, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "movie.mp4"), []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	lib, err := library.Open(library.Config{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	control := controller.New(controller.Config{})
	handler, err := New(Config{Version: "test", Controller: control, Library: lib, TranscodeAvailable: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { handler.Close(); control.Close(); _ = lib.Close() })
	return handler, lib, control, lib.Roots()[0].ID
}

func TestMediaRefCarriesDetectedMIMEType(t *testing.T) {
	media := make([]byte, 261)
	copy(media, []byte{
		0x00, 0x00, 0x00, 0x20, 0x66, 0x74, 0x79, 0x70,
		0x6d, 0x70, 0x34, 0x32, 0x00, 0x00, 0x00, 0x00,
		0x6d, 0x70, 0x34, 0x32, 0x6d, 0x70, 0x34, 0x31,
		0x69, 0x73, 0x6f, 0x6d, 0x69, 0x73, 0x6f, 0x32,
	})
	root := t.TempDir()
	path := filepath.Join(root, "movie.mp4")
	if err := os.WriteFile(path, media, 0o600); err != nil {
		t.Fatal(err)
	}
	lib, err := library.Open(library.Config{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	control := controller.New(controller.Config{})
	h, err := New(Config{Version: "test", Controller: control, Library: lib, FFmpegPath: filepath.Join(root, "missing-ffmpeg")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { h.Close(); control.Close(); _ = lib.Close() }()
	rootID := lib.Roots()[0].ID
	page, err := lib.Browse(rootID, "", "", 1)
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("browse = %#v, err=%v", page, err)
	}
	ref, err := h.mediaRef(context.Background(), rootID, page.Entries[0].ID)
	if err != nil || ref.MIMEType != "video/mp4" {
		t.Fatalf("media MIME = %q, err=%v", ref.MIMEType, err)
	}
}

func TestShellEmbedCacheAndSecurityHeaders(t *testing.T) {
	h, _, _, _ := testHandler(t)
	server := httptest.NewServer(h)
	defer server.Close()
	response, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, response)
	if response.Header.Get("Cache-Control") != "no-cache" || response.Header.Get("Content-Security-Policy") != csp {
		t.Fatalf("headers = %#v", response.Header)
	}
	asset := regexp.MustCompile(`/assets/(app\.[0-9a-f]{8}\.js)`).FindStringSubmatch(body)
	if len(asset) != 2 {
		t.Fatalf("hashed JS absent: %s", body)
	}
	response, err = http.Get(server.URL + "/assets/" + asset[1])
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(readBody(t, response))
	sum := sha256.Sum256(data)
	if !strings.Contains(asset[1], hex.EncodeToString(sum[:4])) {
		t.Fatalf("asset name not content hash: %s", asset[1])
	}
	if response.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatal(response.Header.Get("Cache-Control"))
	}
}

func TestStaticRejectsUnhashedAssets(t *testing.T) {
	h, _, _, _ := testHandler(t)
	for _, path := range []string{"/assets/index.html", "/assets/app.js", "/assets/../index.html"} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s = %d %q", path, response.Code, response.Header().Get("Cache-Control"))
		}
	}
}

func TestBootstrapAndLibrarySanitizedNoStore(t *testing.T) {
	h, _, _, rootID := testHandler(t)
	request := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("bootstrap = %d %#v", response.Code, response.Header())
	}
	if strings.Contains(response.Body.String(), `"endpoint"`) || strings.Contains(response.Body.String(), `"source"`) {
		t.Fatal("private field leak")
	}
	if !strings.Contains(response.Body.String(), `"gapless":true`) {
		t.Fatal("gapless feature missing")
	}
	if !strings.Contains(response.Body.String(), `"transcode":true`) {
		t.Fatal("transcode feature missing")
	}
	if !strings.Contains(response.Body.String(), `"artwork_id":""`) {
		t.Fatal("empty artwork state omitted")
	}
	request = httptest.NewRequest(http.MethodGet, "/api/library?root_id="+rootID, nil)
	response = httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "movie.mp4") {
		t.Fatalf("library = %d %s", response.Code, response.Body.String())
	}
}

func TestTranscodeUnavailableRejected(t *testing.T) {
	root := t.TempDir()
	lib, err := library.Open(library.Config{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	control := controller.New(controller.Config{})
	h, err := New(Config{Version: "test", Controller: control, Library: lib})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { h.Close(); control.Close(); _ = lib.Close() }()

	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil))
	if !strings.Contains(response.Body.String(), `"transcode":false`) {
		t.Fatalf("bootstrap = %s", response.Body.String())
	}
	result, _ := h.command(context.Background(), envelope{Type: "player.transcode", ID: "enable", Payload: []byte(`{"enabled":true}`)})
	if result.Code != controller.CodeInvalid {
		t.Fatalf("enable result = %#v", result)
	}
}

func TestBootstrapManagedByGUIFollowsConfig(t *testing.T) {
	for _, managed := range []bool{false, true} {
		root := t.TempDir()
		lib, err := library.Open(library.Config{Roots: []string{root}})
		if err != nil {
			t.Fatal(err)
		}
		control := controller.New(controller.Config{})
		h, err := New(Config{Version: "test", Controller: control, Library: lib, ManagedByGUI: managed})
		if err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil))
		var payload struct {
			ManagedByGUI bool `json:"managed_by_gui"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.ManagedByGUI != managed {
			t.Fatalf("managed_by_gui = %v, want %v", payload.ManagedByGUI, managed)
		}
		h.Close()
		control.Close()
		_ = lib.Close()
	}
}

func TestSafeSnapshotDeviceMetadata(t *testing.T) {
	tt := []struct {
		name      string
		device    playback.Device
		protocol  string
		audioOnly bool
	}{
		{name: "DLNA", device: playback.Device{ID: "dlna", Name: "TV", Protocol: "DLNA"}, protocol: "DLNA"},
		{name: "audio Chromecast", device: playback.Device{ID: "cast", Name: "Speaker", Protocol: "Chromecast", AudioOnly: true}, protocol: "Chromecast", audioOnly: true},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			devices := safeSnapshot(controller.Snapshot{Devices: []playback.Device{tc.device}}).Devices
			if len(devices) != 1 || devices[0].Protocol != tc.protocol {
				t.Fatalf("devices = %#v", devices)
			}
			gotAudioOnly := len(devices[0].Capabilities) == 1 && devices[0].Capabilities[0] == "audio_only"
			if gotAudioOnly != tc.audioOnly {
				t.Fatalf("audio only = %t, want %t", gotAudioOnly, tc.audioOnly)
			}
		})
	}
}

func TestStateUpdatesOnlyChangedDomains(t *testing.T) {
	base := func() snapshotDTO {
		return snapshotDTO{
			Revision:      1,
			Devices:       []deviceDTO{{ID: "tv", Label: "TV", Protocol: "DLNA"}},
			Queue:         []queueDTO{{ID: "one", Name: "One", Kind: "video", Selected: true}},
			PlaybackState: controller.PlaybackStatePlaying,
			Duration:      60,
			Volume:        25,
			HasSession:    true,
			Policy:        controller.DefaultPolicy(),
		}
	}
	tt := []struct {
		name   string
		change func(*snapshotDTO)
		want   []string
	}{
		{name: "playback", change: func(s *snapshotDTO) { s.Position = 1 }, want: []string{"state.playback"}},
		{name: "devices", change: func(s *snapshotDTO) { s.Devices[0].Label = "Living room" }, want: []string{"state.devices"}},
		{name: "queue", change: func(s *snapshotDTO) { s.Queue[0].Active = true }, want: []string{"state.queue"}},
		{name: "selection", change: func(s *snapshotDTO) { s.SelectedMediaName = "One" }, want: []string{"state.selection"}},
		{name: "policy", change: func(s *snapshotDTO) { s.Policy.AutoPlayNext = true }, want: []string{"state.policy"}},
		{name: "multiple", change: func(s *snapshotDTO) { s.Position = 1; s.Queue[0].Active = true }, want: []string{"state.queue", "state.playback"}},
		{name: "snapshot fallback", change: func(s *snapshotDTO) { s.ActiveMediaName = "One" }, want: []string{"state.snapshot"}},
		{name: "unmapped change", change: func(*snapshotDTO) {}, want: []string{"state.snapshot"}},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			previous, current := base(), base()
			current.Revision = 2
			tc.change(&current)
			updates := stateUpdates(previous, current)
			got := make([]string, 0, len(updates))
			for _, update := range updates {
				got = append(got, update.kind)
				if !bytes.Contains(update.data, []byte(`"revision":2`)) {
					t.Fatalf("missing revision: %s", update.data)
				}
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("updates = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLibraryImageThumbnailModalAndPlayerArtwork(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	var source bytes.Buffer
	if err := jpeg.Encode(&source, solidWebUIArtwork(80, 40), nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "picture.jpg"), source.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	lib, err := library.Open(library.Config{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	control := controller.New(controller.Config{})
	h, err := New(Config{Version: "test", Controller: control, Library: lib})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { h.Close(); control.Close(); _ = lib.Close() }()
	rootID := lib.Roots()[0].ID

	browse := httptest.NewRecorder()
	h.ServeHTTP(browse, httptest.NewRequest(http.MethodGet, "/api/library?root_id="+rootID, nil))
	var page struct {
		Entries []entryDTO `json:"entries"`
	}
	if err = json.Unmarshal(browse.Body.Bytes(), &page); err != nil || len(page.Entries) != 1 {
		t.Fatalf("browse = %s, err=%v", browse.Body.String(), err)
	}
	entry := page.Entries[0]
	if entry.MediaKind != "image" || entry.ThumbnailURL == "" || entry.ArtworkURL == "" {
		t.Fatalf("entry = %#v", entry)
	}

	thumbnail := httptest.NewRecorder()
	h.ServeHTTP(thumbnail, httptest.NewRequest(http.MethodGet, entry.ThumbnailURL, nil))
	assertJPEGDimensions(t, thumbnail, 128, 128)
	if thumbnail.Header().Get("Cache-Control") != "private, no-cache" || thumbnail.Header().Get("ETag") == "" {
		t.Fatalf("thumbnail headers = %#v", thumbnail.Header())
	}
	artwork := httptest.NewRecorder()
	h.ServeHTTP(artwork, httptest.NewRequest(http.MethodGet, entry.ArtworkURL, nil))
	assertJPEGDimensions(t, artwork, 80, 40)

	result, _ := h.command(context.Background(), envelope{Type: "library.select_media", ID: "select-image", Payload: json.RawMessage(`{"root_id":"` + rootID + `","entry_id":"` + entry.ID + `"}`)})
	if !result.OK() {
		t.Fatal(result)
	}
	snapshot, err := control.Snapshot(context.Background())
	if err != nil || snapshot.ArtworkID != "" {
		t.Fatalf("snapshot artwork = %q, err=%v", snapshot.ArtworkID, err)
	}
	ref, err := h.mediaRef(context.Background(), rootID, entry.ID)
	if err != nil || ref.LoadArtwork == nil {
		t.Fatalf("media ref artwork loader: %v", err)
	}
	resolved, err := ref.LoadArtwork(context.Background())
	if err != nil || resolved == nil {
		t.Fatalf("resolve artwork: %#v, %v", resolved, err)
	}
	player := httptest.NewRecorder()
	h.ServeHTTP(player, httptest.NewRequest(http.MethodGet, "/api/artwork/"+resolved.ID+".jpg", nil))
	assertJPEGDimensions(t, player, 80, 40)
}

func TestLibraryVideoArtworkUsesFFmpegCache(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake skipped on windows")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	mediaPath := filepath.Join(root, "movie.mp4")
	if err := os.WriteFile(mediaPath, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	framePath := filepath.Join(t.TempDir(), "frame.jpg")
	frame, err := os.Create(framePath)
	if err != nil {
		t.Fatal(err)
	}
	if err = jpeg.Encode(frame, solidWebUIArtwork(96, 54), nil); err != nil {
		t.Fatal(err)
	}
	if err = frame.Close(); err != nil {
		t.Fatal(err)
	}
	counterPath := filepath.Join(t.TempDir(), "count")
	ffmpegPath := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\ncase \"$*\" in\n  *-protocols*) printf 'Input:\\n  fd\\nOutput:\\n'; exit 0 ;;\n  *-frames:v*) printf x >> \"" + counterPath + "\"; cat \"" + framePath + "\"; exit 0 ;;\nesac\nprintf 'Duration: 00:00:10.00\\n' >&2\nexit 1\n"
	if err = os.WriteFile(ffmpegPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	lib, err := library.Open(library.Config{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	control := controller.New(controller.Config{})
	h, err := New(Config{Version: "test", Controller: control, Library: lib, FFmpegPath: ffmpegPath})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { h.Close(); control.Close(); _ = lib.Close() }()
	rootID := lib.Roots()[0].ID
	page, err := lib.Browse(rootID, "", "", 10)
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("browse = %#v, err=%v", page, err)
	}
	entryID := page.Entries[0].ID
	result, _ := h.command(context.Background(), envelope{Type: "library.select_media", ID: "select-video", Payload: json.RawMessage(`{"root_id":"` + rootID + `","entry_id":"` + entryID + `"}`)})
	if !result.OK() {
		t.Fatal(result)
	}
	if count, readErr := os.ReadFile(counterPath); !os.IsNotExist(readErr) {
		t.Fatalf("selection resolved artwork: %q, err=%v", count, readErr)
	}
	for _, path := range []string{
		libraryArtworkURL("/api/thumbnail", rootID, entryID),
		libraryArtworkURL("/api/media-artwork", rootID, entryID),
	} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", path, response.Code, response.Body.String())
		}
	}
	count, err := os.ReadFile(counterPath)
	if err != nil || string(count) != "x" {
		t.Fatalf("ffmpeg frame runs = %q, err=%v", count, err)
	}
}

func solidWebUIArtwork(width, height int) image.Image {
	result := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			result.Set(x, y, color.RGBA{R: 70, G: 130, B: 200, A: 255})
		}
	}
	return result
}

func assertJPEGDimensions(t *testing.T, response *httptest.ResponseRecorder, width, height int) {
	t.Helper()
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("response = %d %#v %q", response.Code, response.Header(), response.Body.Bytes())
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(response.Body.Bytes()))
	if err != nil || format != "jpeg" || config.Width != width || config.Height != height {
		t.Fatalf("image = %s %#v, err=%v", format, config, err)
	}
}

func TestArtworkContentAddressedCache(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "song.mp3"), []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	var artwork bytes.Buffer
	picture := image.NewRGBA(image.Rect(0, 0, 20, 20))
	for y := range 20 {
		for x := range 20 {
			picture.Set(x, y, color.RGBA{R: 180, A: 255})
		}
	}
	if err := jpeg.Encode(&artwork, picture, nil); err != nil {
		t.Fatal(err)
	}
	asset, err := metadata.LoadArtwork(artwork.Bytes(), "song.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "song.jpg"), artwork.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	lib, err := library.Open(library.Config{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	control := controller.New(controller.Config{})
	h, err := New(Config{Version: "test", Controller: control, Library: lib})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { h.Close(); control.Close(); _ = lib.Close() }()
	rootID := lib.Roots()[0].ID
	page, err := lib.Browse(rootID, "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	var mediaID string
	for _, entry := range page.Entries {
		if entry.Name == "song.mp3" {
			mediaID = entry.ID
		}
	}
	result, _ := h.command(context.Background(), envelope{Type: "library.select_media", ID: "select", Payload: json.RawMessage(`{"root_id":"` + rootID + `","entry_id":"` + mediaID + `"}`)})
	if !result.OK() {
		t.Fatal(result)
	}
	snapshot, _ := control.Snapshot(context.Background())
	if snapshot.ArtworkID != "" {
		t.Fatalf("artwork ID = %q", snapshot.ArtworkID)
	}
	ref, err := h.mediaRef(context.Background(), rootID, mediaID)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ref.LoadArtwork(context.Background())
	if err != nil || resolved == nil || resolved.ID != asset.ID || strings.Contains(resolved.ID, "song") {
		t.Fatalf("resolved artwork = %#v, err=%v", resolved, err)
	}
	path := "/api/artwork/" + resolved.ID + ".jpg"
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), asset.Data) || response.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" || response.Header().Get("ETag") == "" {
		t.Fatalf("artwork = %d %q %#v", response.Code, response.Body.Bytes(), response.Header())
	}
	conditional := httptest.NewRequest(http.MethodGet, path, nil)
	conditional.Header.Set("If-None-Match", response.Header().Get("ETag"))
	notModified := httptest.NewRecorder()
	h.ServeHTTP(notModified, conditional)
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
		t.Fatalf("conditional = %d %q", notModified.Code, notModified.Body.String())
	}
}

func TestArtworkReloadReferencesAreBounded(t *testing.T) {
	h, _, _, _ := testHandler(t)
	for i := range maxArtworkRefs + 1 {
		id := strconv.Itoa(i)
		h.rememberArtwork(id, func(context.Context) ([]byte, string, error) {
			return []byte(id), "image/jpeg", nil
		})
	}
	if _, err := h.loadArtwork(context.Background(), "0"); err == nil {
		t.Fatal("old artwork reference retained")
	}
	latest := strconv.Itoa(maxArtworkRefs)
	data, err := h.loadArtwork(context.Background(), latest)
	if err != nil || string(data) != latest {
		t.Fatalf("latest artwork = %q, err=%v", data, err)
	}
}

func TestStrictEnvelopeVersionUnknownAndNesting(t *testing.T) {
	valid := []byte(`{"protocol_version":1,"type":"devices.refresh","id":"1","payload":{}}`)
	if _, err := decodeEnvelope(valid); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{[]byte(`{"protocol_version":1,"type":"x","extra":1}`), []byte(`{"protocol_version":1,"type":"x"}{}`), []byte(strings.Repeat("[", 17) + strings.Repeat("]", 17))} {
		if _, err := decodeEnvelope(data); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
}

func TestCommandStableQueueIDsAndStrictPayload(t *testing.T) {
	h, lib, control, rootID := testHandler(t)
	page, err := lib.Browse(rootID, "", "", 10)
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("browse: %#v %v", page, err)
	}
	entryID := page.Entries[0].ID
	command := func(kind, id, payload string) controller.Result {
		result, _ := h.command(context.Background(), envelope{ProtocolVersion: ProtocolVersion, Type: kind, ID: id, Payload: json.RawMessage(payload)})
		return result
	}
	if result := command("library.select_media", "select", `{"root_id":"`+rootID+`","entry_id":"`+entryID+`"}`); !result.OK() {
		t.Fatal(result)
	}
	selected, _ := control.Snapshot(context.Background())
	if len(selected.Queue) != 1 || !selected.Queue[0].IsSelected || selected.SelectedMedia != "movie.mp4" {
		t.Fatalf("selected media not queued: %#v", selected)
	}
	if result := command("library.select_media", "select-next", `{"root_id":"`+rootID+`","entry_id":"`+entryID+`"}`); !result.OK() {
		t.Fatal(result)
	}
	selectedNext, _ := control.Snapshot(context.Background())
	if len(selectedNext.Queue) != 1 || selectedNext.Queue[0].ID != selected.Queue[0].ID || !selectedNext.Queue[0].IsSelected || selectedNext.SelectedMedia != "movie.mp4" {
		t.Fatalf("duplicate media appended: %#v", selectedNext)
	}
	if result := command("queue.clear", "clear-selected", `{}`); !result.OK() {
		t.Fatal(result)
	}
	if result := command("queue.add", "add1", `{"root_id":"`+rootID+`","entry_id":"`+entryID+`"}`); !result.OK() {
		t.Fatal(result)
	}
	first, _ := control.Snapshot(context.Background())
	if result := command("queue.add", "add2", `{"root_id":"`+rootID+`","entry_id":"`+entryID+`"}`); !result.OK() {
		t.Fatal(result)
	}
	second, _ := control.Snapshot(context.Background())
	if len(second.Queue) != 1 || second.Queue[0].ID != first.Queue[0].ID || second.Revision != first.Revision {
		t.Fatalf("duplicate queue add mutated playlist: %#v %#v", first.Queue, second.Queue)
	}
	if result := command("library.play", "play", `{"root_id":"`+rootID+`","entry_id":"`+entryID+`"}`); result.Code != controller.CodeNoDevice {
		t.Fatalf("library play = %#v", result)
	}
	if result := command("queue.clear", "clear", `{}`); !result.OK() {
		t.Fatal(result)
	}
	cleared, _ := control.Snapshot(context.Background())
	if len(cleared.Queue) != 0 {
		t.Fatalf("queue not cleared: %#v", cleared.Queue)
	}
	if result := command("player.volume", "bad", `{"volume":101}`); result.Code != controller.CodeInvalid {
		t.Fatal(result)
	}
	if result := command("player.volume", "bad-delta", `{"delta":5}`); result.Code != controller.CodeInvalid {
		t.Fatal(result)
	}
	if result := command("player.volume", "ambiguous", `{"volume":10,"delta":1}`); result.Code != controller.CodeInvalid {
		t.Fatal(result)
	}
	if result := command("player.volume", "adjust", `{"delta":1}`); result.Code != controller.CodeNoDevice {
		t.Fatalf("relative volume command rejected: %#v", result)
	}
	if result := command("queue.move", "zero-move", `{"item_id":"missing","delta":0}`); result.Code != controller.CodeInvalid {
		t.Fatalf("zero queue move = %#v", result)
	}
	if result := command("queue.move", "multi-move", `{"item_id":"missing","delta":2}`); result.Code != controller.CodeNotFound {
		t.Fatalf("multi-position queue move rejected: %#v", result)
	}
	if result := command("player.mute", "unknown", `{"muted":true,"extra":1}`); result.Code != controller.CodeInvalid {
		t.Fatal(result)
	}
}

func TestCommandQueueAddManyKeepsRequestOrder(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"b.mp4", "a.mp4", "c.mp4", "captions.srt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("media"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	lib, err := library.Open(library.Config{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	control := controller.New(controller.Config{})
	h, err := New(Config{Version: "test", Controller: control, Library: lib})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close(); control.Close(); _ = lib.Close() })
	rootID := lib.Roots()[0].ID
	page, err := lib.Browse(rootID, "", "", 10)
	if err != nil || len(page.Entries) != 4 {
		t.Fatalf("browse: %#v %v", page, err)
	}
	ids := make(map[string]string, len(page.Entries))
	for _, entry := range page.Entries {
		ids[entry.Name] = entry.ID
	}
	payload := `{"root_id":"` + rootID + `","entry_ids":["` + ids["a.mp4"] + `","` + ids["b.mp4"] + `","` + ids["c.mp4"] + `","` + ids["captions.srt"] + `","bogus"]}`
	result, extra := h.command(context.Background(), envelope{Type: "queue.add_many", ID: "bulk", Payload: json.RawMessage(payload)})
	if !result.OK() {
		t.Fatal(result)
	}
	if extra["added"] != 3 || extra["duplicates"] != 0 || extra["dropped"] != 0 || extra["failed"] != 2 {
		t.Fatalf("extra = %#v", extra)
	}
	snapshot, _ := control.Snapshot(context.Background())
	names := make([]string, 0, len(snapshot.Queue))
	for _, item := range snapshot.Queue {
		names = append(names, item.Name)
	}
	if !slices.Equal(names, []string{"a.mp4", "b.mp4", "c.mp4"}) {
		t.Fatalf("queue order = %v", names)
	}
	again, extra := h.command(context.Background(), envelope{Type: "queue.add_many", ID: "bulk-again", Payload: json.RawMessage(payload)})
	repeat, _ := control.Snapshot(context.Background())
	if !again.OK() || extra["added"] != 0 || extra["duplicates"] != 3 || repeat.Revision != snapshot.Revision {
		t.Fatalf("repeat = %#v extra = %#v revisions = %d/%d", again, extra, snapshot.Revision, repeat.Revision)
	}
	if result, _ := h.command(context.Background(), envelope{Type: "queue.add_many", ID: "empty", Payload: json.RawMessage(`{"root_id":"` + rootID + `","entry_ids":[]}`)}); result.Code != controller.CodeInvalid {
		t.Fatalf("empty = %#v", result)
	}
	if result, _ := h.command(context.Background(), envelope{Type: "queue.add_many", ID: "unresolvable", Payload: json.RawMessage(`{"root_id":"` + rootID + `","entry_ids":["bogus"]}`)}); result.Code != controller.CodeInvalid {
		t.Fatalf("unresolvable = %#v", result)
	}
}

func TestQueuedPlaybackActive(t *testing.T) {
	playing := controller.Snapshot{PlaybackState: "PLAYING", Queue: []controller.QueueItem{{IsActive: true}}}
	if !queuedPlaybackActive(playing) {
		t.Fatal("playing queue not active")
	}
	playing.PlaybackState = "PAUSED"
	if queuedPlaybackActive(playing) || queuedPlaybackActive(controller.Snapshot{PlaybackState: "PLAYING"}) {
		t.Fatal("inactive queue reported active")
	}
}

func TestCommandClearsSubtitle(t *testing.T) {
	h, _, control, _ := testHandler(t)
	subtitlePath := filepath.Join(t.TempDir(), "captions.srt")
	if err := os.WriteFile(subtitlePath, []byte("subtitle"), 0o600); err != nil {
		t.Fatal(err)
	}
	ref := controller.SubtitleRef{
		RootID: "root", ID: "subtitle", Name: "captions.srt",
		Open: func(context.Context) (io.ReadSeekCloser, time.Time, error) {
			file, err := os.Open(subtitlePath)
			return file, time.Time{}, err
		},
	}
	if result := control.SelectSubtitle(context.Background(), controller.Mutation{}, ref); !result.OK() {
		t.Fatal(result)
	}
	result, _ := h.command(context.Background(), envelope{Type: "library.clear_subtitle", ID: "clear", Payload: json.RawMessage(`{}`)})
	if !result.OK() {
		t.Fatal(result)
	}
	snapshot, err := control.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SelectedSubtitle != "" {
		t.Fatalf("selected subtitle = %q", snapshot.SelectedSubtitle)
	}
	if result, _ := h.command(context.Background(), envelope{Type: "library.clear_subtitle", ID: "bad", Payload: json.RawMessage(`{"extra":true}`)}); result.Code != controller.CodeInvalid {
		t.Fatal(result)
	}
}

func TestWebSocketProtocolDedupeAndShutdown(t *testing.T) {
	h, _, _, _ := testHandler(t)
	server := httptest.NewServer(h)
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/ws"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	readType(t, conn, "state.snapshot")
	if err = conn.WriteJSON(map[string]any{"protocol_version": 99, "type": "devices.refresh", "id": "bad", "payload": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	readType(t, conn, "error")
	message := map[string]any{"protocol_version": 1, "type": "devices.refresh", "id": "one", "payload": map[string]any{}}
	if err = conn.WriteJSON(message); err != nil {
		t.Fatal(err)
	}
	readType(t, conn, "pending")
	readType(t, conn, "error")
	if err = conn.WriteJSON(message); err != nil {
		t.Fatal(err)
	}
	readType(t, conn, "error")
	h.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	shutdown := false
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if closeErr, ok := err.(*websocket.CloseError); !ok || closeErr.Code != websocket.CloseGoingAway {
				t.Fatalf("shutdown close = %v", err)
			}
			break
		}
		var message struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(data, &message)
		if message.Type == "server.shutdown" {
			shutdown = true
		}
	}
	if !shutdown {
		t.Fatal("server.shutdown absent")
	}
}

func TestWebSocketPerIPLimit(t *testing.T) {
	h, _, _, _ := testHandler(t)
	server := httptest.NewServer(h)
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/ws"
	connections := make([]*websocket.Conn, 0, maxClientsPerIP)
	for range maxClientsPerIP {
		conn, _, err := websocket.DefaultDialer.Dial(url, nil)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, conn)
	}
	defer func() {
		for i := range connections {
			_ = connections[i].Close()
		}
	}()
	conn, response, err := websocket.DefaultDialer.Dial(url, nil)
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("limit err=%v status=%v", err, response)
	}
}

func TestWebSocketGlobalLimit(t *testing.T) {
	h, _, _, _ := testHandler(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.URL.Query().Get("ip")
		r.RemoteAddr = "192.0.2." + ip + ":1234"
		h.ServeHTTP(w, r)
	}))
	defer server.Close()
	baseURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/ws?ip="
	connections := make([]*websocket.Conn, 0, maxClients)
	for i := range maxClients {
		conn, _, err := websocket.DefaultDialer.Dial(baseURL+strconv.Itoa(i/maxClientsPerIP+1), nil)
		if err != nil {
			t.Fatalf("client %d: %v", i, err)
		}
		connections = append(connections, conn)
	}
	defer func() {
		for _, conn := range connections {
			_ = conn.Close()
		}
	}()
	conn, response, err := websocket.DefaultDialer.Dial(baseURL+"5", nil)
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("global limit err=%v status=%v", err, response)
	}
}

func TestWebSocketSlowClientDisconnected(t *testing.T) {
	h, _, _, _ := testHandler(t)
	h.hub.close()
	gate := make(chan struct{})
	h.hub = newHubWithConfig(h.cfg.Controller, h.command, hubConfig{
		writeWait: writeWait, pongWait: pongWait, pingEvery: pingEvery, writerGate: gate,
	})
	server := httptest.NewServer(h)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/ws", nil)
	if err != nil {
		close(gate)
		t.Fatal(err)
	}
	defer conn.Close()

	var slow *client
	deadline := time.Now().Add(2 * time.Second)
	for slow == nil && time.Now().Before(deadline) {
		h.hub.mu.Lock()
		for c := range h.hub.clients {
			slow = c
		}
		h.hub.mu.Unlock()
		if slow == nil {
			time.Sleep(time.Millisecond)
		}
	}
	if slow == nil {
		close(gate)
		t.Fatal("client not registered")
	}
	for i := range outboundSize {
		if !slow.enqueue("toast", mustEnvelope("toast", strconv.Itoa(i), map[string]any{})) {
			break
		}
	}
	close(gate)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, _, err = conn.ReadMessage()
		if err == nil {
			continue
		}
		closeErr, ok := err.(*websocket.CloseError)
		if !ok || closeErr.Code != websocket.ClosePolicyViolation || closeErr.Text != "slow client" {
			t.Fatalf("slow close = %v", err)
		}
		break
	}
}

func TestWebSocketPingTimeoutDisconnects(t *testing.T) {
	h, _, _, _ := testHandler(t)
	h.hub.close()
	h.hub = newHubWithConfig(h.cfg.Controller, h.command, hubConfig{
		writeWait: 200 * time.Millisecond,
		pongWait:  120 * time.Millisecond,
		pingEvery: 20 * time.Millisecond,
	})
	server := httptest.NewServer(h)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	pingCount := 0
	conn.SetPingHandler(func(string) error {
		pingCount++
		return nil
	})
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if _, _, err = conn.ReadMessage(); err != nil {
			break
		}
	}
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		t.Fatalf("client deadline expired before server disconnect: %v", err)
	}
	if pingCount == 0 {
		t.Fatal("server sent no ping before timeout")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		h.hub.mu.Lock()
		clients := len(h.hub.clients)
		h.hub.mu.Unlock()
		if clients == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed-out client still registered")
}

func TestWebSocketOriginBinaryAndReadLimit(t *testing.T) {
	h, _, _, _ := testHandler(t)
	server := httptest.NewServer(h)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/ws"
	header := http.Header{"Origin": []string{"http://evil.invalid"}}
	if conn, response, err := websocket.DefaultDialer.Dial(wsURL, header); err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatalf("cross-origin upgrade: response=%v err=%v", response, err)
	}
	for _, test := range []struct {
		name string
		kind int
		data []byte
		code int
	}{
		{name: "binary", kind: websocket.BinaryMessage, data: []byte("x"), code: websocket.CloseUnsupportedData},
		{name: "too large", kind: websocket.TextMessage, data: bytes.Repeat([]byte("x"), maxMessageBytes+1), code: websocket.CloseMessageTooBig},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			readType(t, conn, "state.snapshot")
			if err = conn.WriteMessage(test.kind, test.data); err != nil {
				t.Fatal(err)
			}
			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			_, _, err = conn.ReadMessage()
			closeErr, ok := err.(*websocket.CloseError)
			if !ok || closeErr.Code != test.code {
				t.Fatalf("close = %v, want %d", err, test.code)
			}
		})
	}
}

func TestOutboundStateCoalescingPreservesControl(t *testing.T) {
	c := &client{send: make(chan outbound, outboundSize)}
	c.send <- outbound{kind: "ack", data: []byte("control")}
	for range outboundSize - 1 {
		c.send <- outbound{kind: "state.snapshot", data: []byte("old")}
	}
	if !c.enqueue("state.snapshot", []byte("new")) {
		t.Fatal("state update not coalesced")
	}
	control, latest := 0, 0
	for range len(c.send) {
		message := <-c.send
		if message.kind == "ack" {
			control++
		}
		if message.kind == "state.snapshot" && string(message.data) == "new" {
			latest++
		}
	}
	if control != 1 || latest != 1 {
		t.Fatalf("control=%d latest=%d", control, latest)
	}
}

func TestCommandFailureEmitsOneTerminalMessage(t *testing.T) {
	c := &client{send: make(chan outbound, outboundSize)}
	c.enqueueResult(controller.Result{RequestID: "request", Revision: 7, Code: controller.CodeConflict, Message: "state changed"}, nil)
	if len(c.send) != 1 {
		t.Fatalf("terminal messages = %d", len(c.send))
	}
	message := <-c.send
	if message.kind != "error" || !bytes.Contains(message.data, []byte(`"id":"request"`)) || bytes.Contains(message.data, []byte(`"type":"toast"`)) {
		t.Fatalf("terminal message = %s %s", message.kind, message.data)
	}
}

func readType(t *testing.T, conn *websocket.Conn, want string) {
	t.Helper()
	for range 5 {
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		var message struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &message) != nil {
			t.Fatal(string(data))
		}
		if message.Type == want {
			return
		}
	}
	t.Fatalf("missing %s", want)
}
func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
