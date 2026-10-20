package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"go2tv.app/go2tv/v2/internal/controller"
	"go2tv.app/go2tv/v2/internal/library"
	"go2tv.app/go2tv/v2/internal/mediaserver"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/internal/radiobrowser"
)

const testStationID = "9617a958-0601-11e8-ae97-52543be04c81"

type fakeRadio struct {
	mu        sync.Mutex
	stations  []radiobrowser.Station
	refreshed map[string]radiobrowser.Station
	clicks    []string
}

func (f *fakeRadio) SearchText(context.Context, string, string) ([]radiobrowser.Station, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]radiobrowser.Station(nil), f.stations...), nil
}

func (f *fakeRadio) Countries(context.Context) ([]radiobrowser.Country, error) {
	return []radiobrowser.Country{{Name: "Greece", Code: "GR", StationCount: 12}}, nil
}

func (f *fakeRadio) Station(_ context.Context, id string) (radiobrowser.Station, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if station, ok := f.refreshed[id]; ok {
		return station, nil
	}
	return radiobrowser.Station{}, errors.New("station unavailable")
}

func (f *fakeRadio) Click(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clicks = append(f.clicks, id)
	return nil
}

func radioHandler(t *testing.T, dir RadioDirectory, storePath string, control *controller.Controller) *Handler {
	t.Helper()
	lib, err := library.Open(library.Config{Roots: []string{t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	if control == nil {
		control = controller.New(controller.Config{})
		t.Cleanup(control.Close)
	}
	h, err := New(Config{Version: "test", Controller: control, Library: lib, Radio: dir, RadioStorePath: storePath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close(); _ = lib.Close() })
	return h
}

func radioGet(t *testing.T, h *Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
	return response
}

func radioCommand(h *Handler, kind, id string, extra map[string]any) controller.Result {
	payload := map[string]any{"station_id": id}
	maps.Copy(payload, extra)
	data, _ := json.Marshal(payload)
	result, _ := h.command(context.Background(), envelope{ProtocolVersion: ProtocolVersion, Type: kind, ID: "request", Payload: data})
	return result
}

func TestRadioDirectoryAPIValidatesAndHidesStreamURLs(t *testing.T) {
	dir := &fakeRadio{stations: []radiobrowser.Station{
		{UUID: testStationID, Name: "Jazz FM", URL: "http://stream.test/jazz", Favicon: "http://logo.test/jazz.png", Country: "The Hellenic Republic", CountryCode: "gr", Codec: "MP3", Bitrate: 128, Tags: "jazz, Jazz,smooth,lounge,chill"},
		{UUID: "other", Name: "No Logo", URL: "http://stream.test/plain"},
	}}
	h := radioHandler(t, dir, "", nil)
	response := radioGet(t, h, "/api/radio/stations?q=jazz&country=gr")
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "stream.test") || strings.Contains(response.Body.String(), "logo.test") {
		t.Fatalf("station listing = %d %s", response.Code, response.Body)
	}
	var listing struct {
		Stations []stationDTO `json:"stations"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	jazz := listing.Stations[0]
	if jazz.CountryCode != "GR" || jazz.Country != "Greece" || jazz.IconURL != "/api/radio/icon?id="+testStationID || strings.Join(jazz.Tags, ",") != "jazz,smooth,lounge" || listing.Stations[1].IconURL != "" {
		t.Fatalf("station DTOs = %+v", listing.Stations)
	}
	for _, target := range []string{
		"/api/radio/stations?country=GRC",
		"/api/radio/stations?country=G1",
		"/api/radio/stations?q=" + strings.Repeat("x", maxRadioQuery+1),
		"/api/radio/stations?url=http://evil.test",
		"/api/radio/countries?all=1",
	} {
		if response := radioGet(t, h, target); response.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d", target, response.Code)
		}
	}
	if response := radioGet(t, h, "/api/radio/countries"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `{"code":"GR","name":"Greece","stations":12}`) {
		t.Fatalf("countries = %d %s", response.Code, response.Body)
	}
}

func TestRadioPlayRelaysDirectoryStationToRenderer(t *testing.T) {
	block := append([]byte{2}, []byte("StreamTitle='Live Song';")...)
	block = append(block, make([]byte, 33-len(block))...)
	station := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/aac")
		if r.Header.Get("Icy-MetaData") == "1" {
			w.Header().Set("icy-metaint", "8")
			_, _ = w.Write(append([]byte("audio..."), block...))
		}
		// Exactly one metadata interval follows the title block.
		_, _ = io.WriteString(w, "more aud")
	}))
	defer station.Close()
	offline := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer offline.Close()
	dir := &fakeRadio{stations: []radiobrowser.Station{
		{UUID: testStationID, Name: "Radio 1.FM", URL: station.URL, Codec: "MP3"},
		{UUID: "dead", Name: "Dead FM", URL: offline.URL},
	}}
	transport := &torrentTransport{}
	server := mediaserver.New(mediaserver.Config{ListenAddr: "127.0.0.1:0"})
	control := controller.New(controller.Config{Discovery: torrentDiscovery{playback.Device{ID: "renderer", Protocol: "DLNA", Endpoint: "http://127.0.0.1:1400"}}, TransportFactory: transport, MediaServer: server})
	defer control.Close()
	h := radioHandler(t, dir, "", control)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if result := radioCommand(h, "radio.play", testStationID, nil); result.Message != "station unavailable" {
		t.Fatalf("unlisted station result = %+v", result)
	}
	radioGet(t, h, "/api/radio/stations")
	for {
		if snapshot, _ := control.Snapshot(ctx); len(snapshot.Devices) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if result := radioCommand(h, "radio.play", "dead", nil); result.Message != "station offline or unreachable" {
		t.Fatalf("offline station result = %+v", result)
	}

	if result := radioCommand(h, "radio.play", testStationID, nil); !result.OK() {
		t.Fatal(result)
	}
	snapshot, _ := control.Snapshot(ctx)
	dto := safeSnapshot(snapshot)
	if dto.SelectedStation != testStationID || !dto.SelectedLive || dto.HasSession {
		t.Fatalf("station without renderer = %+v", dto)
	}
	if library := h.radio.library(); len(library.Recent) != 0 {
		t.Fatal("selected station recorded as played")
	}

	if result := control.SelectDevice(ctx, controller.Mutation{}, "renderer"); !result.OK() {
		t.Fatal(result)
	}
	if result := radioCommand(h, "radio.play", testStationID, nil); !result.OK() {
		t.Fatal(result)
	}
	if transport.load.MediaURL == station.URL || !transport.load.Live || transport.load.MediaType != "audio/aac" || transport.load.Metadata.Title != "Radio 1.FM" {
		t.Fatalf("DLNA radio load = %+v", transport.load)
	}
	response, err := http.Get(transport.load.MediaURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "audio...more aud" {
		t.Fatalf("relayed station audio = %q", body)
	}
	for {
		snapshot, _ = control.Snapshot(ctx)
		if snapshot.StreamTitle == "Live Song" {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("station title not published")
		}
		time.Sleep(time.Millisecond)
	}
	dto = safeSnapshot(snapshot)
	if dto.ActiveStation != testStationID || !dto.ActiveLive || dto.StreamTitle != "Live Song" || len(dto.Queue) != 0 {
		t.Fatalf("playing station snapshot = %+v", dto)
	}
	if library := h.radio.library(); len(library.Recent) != 1 || library.Recent[0].ID != testStationID {
		t.Fatalf("recent = %+v", library.Recent)
	}
	h.closeRadio()
	dir.mu.Lock()
	clicks := strings.Join(dir.clicks, ",")
	dir.mu.Unlock()
	if clicks != testStationID {
		t.Fatalf("directory clicks = %q", clicks)
	}
}

func TestRadioFavoritesPersistAndBroadcast(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "go2tv", "radio.json")
	dir := &fakeRadio{stations: []radiobrowser.Station{{UUID: testStationID, Name: "Jazz FM", URL: "http://stream.test/jazz"}}}
	h := radioHandler(t, dir, storePath, nil)
	server := httptest.NewServer(h)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	readType(t, conn, "state.snapshot")

	if result := radioCommand(h, "radio.favorite", testStationID, map[string]any{"favorite": true}); result.OK() {
		t.Fatal("favorited a station the directory never listed")
	}
	radioGet(t, h, "/api/radio/stations")
	if err := conn.WriteJSON(map[string]any{"protocol_version": 1, "type": "radio.favorite", "id": "fav", "payload": map[string]any{"station_id": testStationID, "favorite": true}}); err != nil {
		t.Fatal(err)
	}
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		var message struct {
			Type    string          `json:"type"`
			Payload radioLibraryDTO `json:"payload"`
		}
		_ = json.Unmarshal(data, &message)
		if message.Type == "state.radio" {
			if len(message.Payload.Favorites) != 1 || message.Payload.Favorites[0].Name != "Jazz FM" {
				t.Fatalf("broadcast favorites = %+v", message.Payload)
			}
			break
		}
	}

	restarted := radioHandler(t, &fakeRadio{}, storePath, nil)
	response := radioGet(t, restarted, "/api/bootstrap")
	var bootstrap bootstrapDTO
	if err := json.Unmarshal(response.Body.Bytes(), &bootstrap); err != nil {
		t.Fatal(err)
	}
	if !bootstrap.Features["radio"] || len(bootstrap.Radio.Favorites) != 1 || bootstrap.Radio.Favorites[0].ID != testStationID {
		t.Fatalf("restarted favorites = %+v", bootstrap.Radio)
	}
	if result := radioCommand(restarted, "radio.favorite", testStationID, map[string]any{"favorite": false}); !result.OK() {
		t.Fatal(result)
	}
	data, err := os.ReadFile(storePath)
	if err != nil || strings.Contains(string(data), testStationID) {
		t.Fatalf("store after removal = %s, %v", data, err)
	}
}

func TestRadioIconServesOnlyKnownStationsAndRemembersFailures(t *testing.T) {
	var logo bytes.Buffer
	if err := png.Encode(&logo, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/missing.png" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(logo.Bytes())
	}))
	defer upstream.Close()
	dir := &fakeRadio{stations: []radiobrowser.Station{
		{UUID: "aa", Name: "Logo", URL: "http://stream.test/a", Favicon: upstream.URL + "/logo.png"},
		{UUID: "bb", Name: "Broken", URL: "http://stream.test/b", Favicon: upstream.URL + "/missing.png"},
	}}
	h := radioHandler(t, dir, "", nil)
	if response := radioGet(t, h, "/api/radio/icon?id=aa"); response.Code != http.StatusNotFound || requests.Load() != 0 {
		t.Fatalf("unlisted station icon = %d after %d fetches", response.Code, requests.Load())
	}
	radioGet(t, h, "/api/radio/stations")
	if response := radioGet(t, h, "/api/radio/icon?id=aa"); response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("station icon = %d %s", response.Code, response.Header().Get("Content-Type"))
	}
	for range 2 {
		if response := radioGet(t, h, "/api/radio/icon?id=bb"); response.Code != http.StatusNotFound || !strings.Contains(response.Header().Get("Cache-Control"), "max-age") {
			t.Fatalf("missing icon = %d %q", response.Code, response.Header().Get("Cache-Control"))
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("icon fetches = %d, want one per favicon", requests.Load())
	}
}
