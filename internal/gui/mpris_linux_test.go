//go:build linux && !android

package gui

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/data/binding"
	"github.com/alexballas/tunetag/id3v1"
	"github.com/godbus/dbus/v5"
	"go2tv.app/go2tv/v2/internal/mediaartwork"
	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/internal/mpris"
	"go2tv.app/go2tv/v2/metadata"
	"go2tv.app/go2tv/v2/soapcalls"
)

func TestMPRISVolumeHonorsRemoteLease(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("truncated response"))
	}))
	defer server.Close()
	screen := &FyneScreen{tvdata: &soapcalls.TVPayload{RenderingControlURL: server.URL}}
	bridge := &guiMPRIS{screen: screen}
	release, err := screen.renderGate.acquireRemoteLease()
	if err != nil {
		t.Fatal(err)
	}
	bridge.setVolume(0.5)
	if requests.Load() != 0 {
		t.Fatal("volume changed while remote session owned the renderer")
	}
	release()
	bridge.setVolume(0.5)
	if requests.Load() != 1 {
		t.Fatal("volume request missing after remote lease released")
	}
}

func TestMPRISVolumeHoldsPermitUntilRequestCompletes(t *testing.T) {
	screen := &FyneScreen{}
	leaseError := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		release, err := screen.renderGate.acquireRemoteLease()
		if err == nil {
			release()
		}
		leaseError <- err
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("truncated response"))
	}))
	defer server.Close()
	screen.tvdata = &soapcalls.TVPayload{RenderingControlURL: server.URL}
	(&guiMPRIS{screen: screen}).setVolume(0.5)
	if err := <-leaseError; !errors.Is(err, errRendererBusy) {
		t.Fatalf("remote lease during volume request = %v, want renderer busy", err)
	}
	release, err := screen.renderGate.acquireRemoteLease()
	if err != nil {
		t.Fatalf("volume failure leaked permit: %v", err)
	}
	release()
}

func TestMPRISSliderSeekSignal(t *testing.T) {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		t.Skip("session bus unavailable")
	}
	tt := []struct {
		name       string
		status     int
		wantSignal bool
	}{
		{"success", http.StatusOK, true},
		{"renderer rejects seek", http.StatusInternalServerError, false},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			var seeks atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.Header.Get("SOAPAction"), "GetPositionInfo") {
					_, _ = w.Write([]byte(`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:GetPositionInfoResponse xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"><TrackDuration>00:02:00</TrackDuration><RelTime>00:00:10</RelTime></u:GetPositionInfoResponse></s:Body></s:Envelope>`))
					return
				}
				seeks.Add(1)
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			screen, _ := newMediaCardTestScreen(t)
			screen.SlideBar = newTappableSlider(screen)
			screen.CurrentPos = binding.NewString()
			screen.EndPos = binding.NewString()
			screen.tvdata = &soapcalls.TVPayload{ControlURL: server.URL}
			service, err := mpris.Start(mpris.Controls{})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			base := mpris.Snapshot{Status: "Playing", Path: "/song.mp3", PositionUS: 10_000_000, LengthUS: 120_000_000}
			service.Update(base)
			bridge := &guiMPRIS{screen: screen, service: service, last: base}
			screen.mpris = bridge
			client, err := dbus.ConnectSessionBus()
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if err := client.AddMatchSignal(dbus.WithMatchObjectPath(mpris.Path), dbus.WithMatchInterface(mpris.PlayerIface), dbus.WithMatchMember("Seeked")); err != nil {
				t.Fatal(err)
			}
			signals := make(chan *dbus.Signal, 10)
			client.Signal(signals)
			defer client.RemoveSignal(signals)
			screen.SlideBar.SetValue(50)
			screen.SlideBar.seekDLNAAsync()
			select {
			case signal := <-signals:
				if !tc.wantSignal {
					t.Fatalf("failed seek emitted %v", signal)
				}
				if len(signal.Body) != 1 || signal.Body[0] != int64(60_000_000) {
					t.Fatalf("Seeked body = %v", signal.Body)
				}
				position, err := client.Object(service.Name(), mpris.Path).GetProperty(mpris.PlayerIface + ".Position")
				if err != nil || position.Value() != int64(60_000_000) {
					t.Fatalf("position = %v, %v", position, err)
				}
			case <-time.After(time.Second):
				if tc.wantSignal {
					t.Fatal("successful GUI seek did not emit Seeked")
				}
			}
			if seeks.Load() != 1 {
				t.Fatalf("seek requests = %d, want 1", seeks.Load())
			}
		})
	}
}

func TestFlatpakMPRISArtworkUsesHostVisibleCache(t *testing.T) {
	t.Setenv("FLATPAK_ID", mpris.AppID)
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	dir, err := mprisArtworkDir()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	asset := &metadata.ArtworkAsset{ID: "cover", Extension: ".png", Data: []byte("artwork")}
	artURL := (&guiMPRIS{dir: dir}).artURL(asset)
	parsed, err := url.Parse(artURL)
	if err != nil || parsed.Scheme != "file" {
		t.Fatalf("art URL = %q, %v", artURL, err)
	}
	if filepath.Dir(filepath.Dir(parsed.Path)) != filepath.Join(cache, "go2tv") {
		t.Fatalf("artwork path outside shared app cache: %q", parsed.Path)
	}
	if data, err := os.ReadFile(parsed.Path); err != nil || !bytes.Equal(data, asset.Data) {
		t.Fatalf("artwork file = %q, %v", data, err)
	}
}

func TestMPRISSelectedTrackBeforeCast(t *testing.T) {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		t.Skip("session bus unavailable")
	}
	path := writeQueueArtworkTrack(t, t.TempDir(), "song.mp3", color.RGBA{R: 130, G: 40, B: 80, A: 255})
	var file bytes.Buffer
	file.Write(make([]byte, 50))
	if err := (&id3v1.Tag{Title: "Selected song", Artist: "Selected artist", Genre: id3v1.GenreNone}).Encode(&file); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, file.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	screen, _ := newMediaCardTestScreen(t)
	service, err := mpris.Start(mpris.Controls{})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	bridge := &guiMPRIS{screen: screen, service: service, dir: t.TempDir(), ctx: context.Background(), last: mpris.Snapshot{Volume: 1}}
	screen.mpris = bridge
	if err := setCurrentMediaPath(screen, path); err != nil {
		t.Fatal(err)
	}
	fyne.DoAndWait(func() {})
	client, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	obj := client.Object(service.Name(), mpris.Path)
	deadline := time.Now().Add(3 * time.Second)
	for {
		status, statusErr := obj.GetProperty(mpris.PlayerIface + ".PlaybackStatus")
		property, metadataErr := obj.GetProperty(mpris.PlayerIface + ".Metadata")
		if statusErr != nil || metadataErr != nil {
			t.Fatalf("MPRIS properties: status %v, metadata %v", statusErr, metadataErr)
		}
		fields := property.Value().(map[string]dbus.Variant)
		if title, ok := fields["xesam:title"]; ok && title.Value() == "Selected song" {
			if status.Value() != "Stopped" || fields["xesam:artist"].Value().([]string)[0] != "Selected artist" {
				t.Fatalf("pre-cast status/metadata = %v, %v", status.Value(), fields)
			}
			if _, ok := fields["mpris:artUrl"]; !ok {
				t.Fatalf("pre-cast artwork missing: %v", fields)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("selected track not published: %v", fields)
		}
		time.Sleep(10 * time.Millisecond)
	}
	clearCurrentMediaSelection(screen)
	fyne.DoAndWait(func() {})
	property, err := obj.GetProperty(mpris.PlayerIface + ".Metadata")
	if err != nil {
		t.Fatal(err)
	}
	if fields := property.Value().(map[string]dbus.Variant); len(fields) != 0 {
		t.Fatalf("cleared metadata = %v", fields)
	}
}

func TestMPRISUsesGUIArtwork(t *testing.T) {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		t.Skip("session bus unavailable")
	}
	path := writeQueueArtworkTrack(t, t.TempDir(), "song.mp3", color.RGBA{R: 130, G: 40, B: 80, A: 255})
	screen := &FyneScreen{}
	_, guiAsset := screen.resolveCachedGUIArtwork(path, "audio/mpeg", true)
	if guiAsset == nil {
		t.Fatal("GUI artwork missing")
	}
	service, err := mpris.Start(mpris.Controls{})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	service.Update(mpris.Snapshot{Status: "Playing", Path: path, Name: "song.mp3", LoopStatus: "None", Volume: 1})
	bridge := &guiMPRIS{screen: screen, service: service, dir: t.TempDir(), last: mpris.Snapshot{Status: "Playing", Path: path, Name: "song.mp3", LoopStatus: "None", Volume: 1}}
	bridge.enrich(path, "")
	client, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	property, err := client.Object(service.Name(), mpris.Path).GetProperty(mpris.PlayerIface + ".Metadata")
	if err != nil {
		t.Fatal(err)
	}
	fields := property.Value().(map[string]dbus.Variant)
	artURL, ok := fields["mpris:artUrl"].Value().(string)
	if !ok || artURL == "" {
		t.Fatalf("art URL = %v", fields["mpris:artUrl"])
	}
	parsed, err := url.Parse(artURL)
	if err != nil || parsed.Scheme != "file" {
		t.Fatalf("art URL = %q, %v", artURL, err)
	}
	data, err := os.ReadFile(parsed.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, guiAsset.Data) {
		t.Fatal("MPRIS artwork differs from GUI artwork")
	}
}

func TestMPRISImageUsesPlaybackArtwork(t *testing.T) {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		t.Skip("session bus unavailable")
	}
	path := filepath.Join(t.TempDir(), "picture.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 32, 24))
	for y := range 24 {
		for x := range 32 {
			img.SetRGBA(x, y, color.RGBA{R: 80, G: 120, B: 160, A: 255})
		}
	}
	if err := png.Encode(file, img); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	guiAsset, err := mediaartwork.Resolve(context.Background(), mediaartwork.Request{Path: path, Kind: mediamodel.MediaKindImage})
	if err != nil || guiAsset == nil {
		t.Fatalf("playback artwork = %v, %v", guiAsset, err)
	}
	service, err := mpris.Start(mpris.Controls{})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	base := mpris.Snapshot{Status: "Playing", Path: path, Name: "picture.png", LoopStatus: "None", Volume: 1}
	service.Update(base)
	bridge := &guiMPRIS{screen: &FyneScreen{}, service: service, dir: t.TempDir(), last: base}
	bridge.enrich(path, "")
	client, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	property, err := client.Object(service.Name(), mpris.Path).GetProperty(mpris.PlayerIface + ".Metadata")
	if err != nil {
		t.Fatal(err)
	}
	artURL := property.Value().(map[string]dbus.Variant)["mpris:artUrl"].Value().(string)
	parsed, err := url.Parse(artURL)
	if err != nil || parsed.Scheme != "file" {
		t.Fatalf("art URL = %q, %v", artURL, err)
	}
	data, err := os.ReadFile(parsed.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, guiAsset.Data) {
		t.Fatal("image artwork differs from playback card")
	}
}
