//go:build !(android || ios)

package gui

import (
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/soapcalls"
)

func TestGUIArtworkCacheUsesMediaIdentityAndContentHash(t *testing.T) {
	dir := t.TempDir()
	trackOne := writeQueueArtworkTrack(t, dir, "one.mp3", color.RGBA{R: 200, A: 255})
	trackTwo := writeQueueArtworkTrack(t, dir, "two.mp3", color.RGBA{R: 200, A: 255})
	screen := &FyneScreen{}

	identityOne, artworkOne := screen.resolveCachedGUIArtwork(trackOne, "audio/mpeg", true)
	identityTwo, artworkTwo := screen.resolveCachedGUIArtwork(trackTwo, "audio/mpeg", true)
	if identityOne == identityTwo {
		t.Fatal("distinct media share identity")
	}
	if artworkOne == nil || artworkTwo == nil {
		t.Fatal("artwork missing")
	}
	if artworkOne.HandlerPath() != artworkTwo.HandlerPath() {
		t.Fatalf("same cover paths differ: %q, %q", artworkOne.HandlerPath(), artworkTwo.HandlerPath())
	}

	if err := os.Remove(strings.TrimSuffix(trackOne, filepath.Ext(trackOne)) + ".png"); err != nil {
		t.Fatal(err)
	}
	_, cached := screen.resolveCachedGUIArtwork(trackOne, "audio/mpeg", true)
	if cached != artworkOne {
		t.Fatal("media identity did not reuse cached artwork")
	}
}

func TestGUIArtworkSelectionRejectsStaleResolution(t *testing.T) {
	screen := &FyneScreen{}
	asset := resolveGUIArtwork(
		writeQueueArtworkTrack(t, t.TempDir(), "one.mp3", color.RGBA{G: 180, A: 255}),
		"audio/mpeg",
		true,
	)
	if asset == nil {
		t.Fatal("artwork missing")
	}

	screen.setCurrentArtworkTarget("one")
	screen.setCurrentArtworkTarget("two")
	screen.setResolvedCurrentArtwork("one", asset)
	if got := screen.getCurrentArtwork(); got != nil {
		t.Fatal("stale resolution replaced selected artwork")
	}
	screen.setResolvedCurrentArtwork("two", asset)
	if got := screen.getCurrentArtwork(); got != asset {
		t.Fatal("selected artwork not stored")
	}
}

func TestQueueNextArtworkLifecycleDistinctCovers(t *testing.T) {
	dir := t.TempDir()
	trackOne := writeQueueArtworkTrack(t, dir, "one.mp3", color.RGBA{R: 220, A: 255})
	trackTwo := writeQueueArtworkTrack(t, dir, "two.mp3", color.RGBA{G: 220, A: 255})
	trackThree := writeQueueArtworkTrack(t, dir, "three.mp3", color.RGBA{B: 220, A: 255})

	screen, soapBodies := newQueueArtworkTestScreen(t, []string{trackOne, trackTwo, trackThree})
	_, artworkOne := screen.resolveCachedGUIArtwork(trackOne, "audio/mpeg", true)
	_, artworkTwo := screen.resolveCachedGUIArtwork(trackTwo, "audio/mpeg", true)
	_, artworkThree := screen.resolveCachedGUIArtwork(trackThree, "audio/mpeg", true)
	if artworkOne == nil || artworkTwo == nil || artworkThree == nil {
		t.Fatal("artwork missing")
	}
	screen.setCurrentArtworkTarget(guiArtworkIdentity(trackOne))
	screen.setResolvedCurrentArtwork(guiArtworkIdentity(trackOne), artworkOne)
	registerGUIArtwork(screen.httpserver, artworkOne)

	soapBodies.expectArtwork(artworkTwo.HandlerPath())
	next, err := queueNext(screen, false)
	if err != nil {
		t.Fatal(err)
	}
	if next.MediaPath != trackTwo || next.Metadata.Artwork == nil || !strings.HasSuffix(next.Metadata.Artwork.URL, artworkTwo.HandlerPath()) {
		t.Fatalf("next metadata = %+v, path %q", next.Metadata, next.MediaPath)
	}
	assertGUIArtworkRoute(t, screen.httpserver, artworkOne, http.StatusOK)
	assertGUIArtworkRoute(t, screen.httpserver, artworkTwo, http.StatusOK)

	screen.promoteQueuedArtwork(guiArtworkIdentity(trackTwo), screen.httpserver)
	screen.mediafile = trackTwo
	assertGUIArtworkRoute(t, screen.httpserver, artworkOne, http.StatusNotFound)
	assertGUIArtworkRoute(t, screen.httpserver, artworkTwo, http.StatusOK)

	soapBodies.expectArtwork(artworkThree.HandlerPath())
	if _, err := queueNext(screen, false); err != nil {
		t.Fatal(err)
	}
	assertGUIArtworkRoute(t, screen.httpserver, artworkTwo, http.StatusOK)
	assertGUIArtworkRoute(t, screen.httpserver, artworkThree, http.StatusOK)

	soapBodies.expectArtwork("")
	if _, err := queueNext(screen, true); err != nil {
		t.Fatal(err)
	}
	assertGUIArtworkRoute(t, screen.httpserver, artworkTwo, http.StatusOK)
	assertGUIArtworkRoute(t, screen.httpserver, artworkThree, http.StatusNotFound)

	soapBodies.mu.Lock()
	bodies := append([]string(nil), soapBodies.values...)
	soapBodies.mu.Unlock()
	if len(bodies) != 3 {
		t.Fatalf("SOAP request count = %d, want 3", len(bodies))
	}
	if !strings.Contains(bodies[0], artworkTwo.HandlerPath()) || !strings.Contains(bodies[1], artworkThree.HandlerPath()) {
		t.Fatalf("queued SOAP artwork mismatch: %q, %q", bodies[0], bodies[1])
	}
	if strings.Contains(bodies[2], "/artwork/") {
		t.Fatalf("clear queue retained artwork: %q", bodies[2])
	}
}

func TestQueueArtworkClearsOnNoArtTransition(t *testing.T) {
	dir := t.TempDir()
	artTrack := writeQueueArtworkTrack(t, dir, "art.mp3", color.RGBA{R: 150, B: 80, A: 255})
	plainTrack := filepath.Join(dir, "plain.mp3")
	if err := os.WriteFile(plainTrack, queueMP3Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	screen, soapBodies := newQueueArtworkTestScreen(t, []string{artTrack, plainTrack})
	_, current := screen.resolveCachedGUIArtwork(artTrack, "audio/mpeg", true)
	screen.setCurrentArtworkTarget(guiArtworkIdentity(artTrack))
	screen.setResolvedCurrentArtwork(guiArtworkIdentity(artTrack), current)
	registerGUIArtwork(screen.httpserver, current)

	soapBodies.expectArtwork("")
	next, err := queueNext(screen, false)
	if err != nil {
		t.Fatal(err)
	}
	if next.Metadata.Artwork != nil {
		t.Fatalf("no-art next metadata = %+v", next.Metadata.Artwork)
	}
	assertGUIArtworkRoute(t, screen.httpserver, current, http.StatusOK)

	screen.promoteQueuedArtwork(guiArtworkIdentity(plainTrack), screen.httpserver)
	assertGUIArtworkRoute(t, screen.httpserver, current, http.StatusNotFound)
	if got := screen.getCurrentArtwork(); got != nil {
		t.Fatalf("stale current artwork = %q", got.HandlerPath())
	}
}

func TestQueueNextKeepsActiveDLNAEndpoints(t *testing.T) {
	dir := t.TempDir()
	trackOne := writeQueueArtworkTrack(t, dir, "one.mp3", color.RGBA{R: 120, A: 255})
	trackTwo := writeQueueArtworkTrack(t, dir, "two.mp3", color.RGBA{G: 120, A: 255})
	screen, _ := newQueueArtworkTestScreen(t, []string{trackOne, trackTwo})

	screen.tvdata.EventURL = "http://active/event"
	screen.tvdata.RenderingControlURL = "http://active/rendering"
	screen.tvdata.ConnectionManagerURL = "http://active/connection"
	screen.controlURL = "http://selected.invalid/control"
	screen.eventURL = "http://selected.invalid/event"
	screen.renderingControlURL = "http://selected.invalid/rendering"
	screen.connectionManagerURL = "http://selected.invalid/connection"

	next, err := queueNext(screen, false)
	if err != nil {
		t.Fatal(err)
	}
	if next.ControlURL != screen.tvdata.ControlURL ||
		next.EventURL != screen.tvdata.EventURL ||
		next.RenderingControlURL != screen.tvdata.RenderingControlURL ||
		next.ConnectionManagerURL != screen.tvdata.ConnectionManagerURL {
		t.Fatalf("queued endpoints = %+v, active = %+v", next, screen.tvdata)
	}
}

type queueSOAPBodies struct {
	mu              sync.Mutex
	values          []string
	expectedArtwork string
}

func (b *queueSOAPBodies) expectArtwork(path string) {
	b.mu.Lock()
	b.expectedArtwork = path
	b.mu.Unlock()
}

func newQueueArtworkTestScreen(t *testing.T, tracks []string) (*FyneScreen, *queueSOAPBodies) {
	t.Helper()
	bodies := &queueSOAPBodies{}
	mediaServer := httphandlers.NewServer("127.0.0.1:39000")
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		bodies.mu.Lock()
		bodies.values = append(bodies.values, string(body))
		expectedArtwork := bodies.expectedArtwork
		bodies.mu.Unlock()
		if expectedArtwork != "" {
			req := httptest.NewRequest(http.MethodGet, expectedArtwork, nil)
			recorder := httptest.NewRecorder()
			mediaServer.ServeMediaHandler().ServeHTTP(recorder, req)
			if recorder.Code != http.StatusOK {
				t.Errorf("artwork route status during SOAP = %d", recorder.Code)
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(controlServer.Close)

	items := make([]QueueItem, 0, len(tracks))
	for _, track := range tracks {
		items = append(items, QueueItem{
			Path:      track,
			BaseName:  filepath.Base(track),
			MediaType: "audio",
		})
	}
	screen := &FyneScreen{
		mediafile:    tracks[0],
		httpserver:   mediaServer,
		controlURL:   controlServer.URL,
		SessionQueue: newSessionQueue(items, 0),
		tvdata: &soapcalls.TVPayload{
			ControlURL:                  controlServer.URL,
			MediaURL:                    "http://127.0.0.1:39000/" + filepath.Base(tracks[0]),
			SubtitlesURL:                "http://127.0.0.1:39000/subtitles.srt",
			CallbackURL:                 "http://127.0.0.1:39000/callback",
			MediaType:                   "audio/mpeg",
			MediaPath:                   tracks[0],
			CurrentTimers:               make(map[string]*time.Timer),
			MediaRenderersStates:        make(map[string]*soapcalls.States),
			InitialMediaRenderersStates: make(map[string]bool),
		},
	}
	return screen, bodies
}

func assertGUIArtworkRoute(t *testing.T, server *httphandlers.HTTPserver, asset interface{ HandlerPath() string }, want int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, asset.HandlerPath(), nil)
	recorder := httptest.NewRecorder()
	server.ServeMediaHandler().ServeHTTP(recorder, req)
	if recorder.Code != want {
		t.Fatalf("route %q status = %d, want %d", asset.HandlerPath(), recorder.Code, want)
	}
}

func writeQueueArtworkTrack(t *testing.T, dir, name string, fill color.RGBA) string {
	t.Helper()
	mediaPath := filepath.Join(dir, name)
	if err := os.WriteFile(mediaPath, queueMP3Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	cover := image.NewRGBA(image.Rect(0, 0, 32, 24))
	for y := range 24 {
		for x := range 32 {
			cover.SetRGBA(x, y, fill)
		}
	}
	coverPath := strings.TrimSuffix(mediaPath, filepath.Ext(mediaPath)) + ".png"
	coverFile, err := os.Create(coverPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(coverFile, cover); err != nil {
		coverFile.Close()
		t.Fatal(err)
	}
	if err := coverFile.Close(); err != nil {
		t.Fatal(err)
	}
	return mediaPath
}

func queueMP3Bytes() []byte {
	data := make([]byte, 261)
	copy(data, "ID3")
	return data
}
