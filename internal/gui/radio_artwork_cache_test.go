package gui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/test"

	"go2tv.app/go2tv/v2/internal/radiobrowser"
	"go2tv.app/go2tv/v2/metadata"
)

func TestRadioArtworkRecoversAfterTemporaryFailure(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	valid := radioTestPNG(t)
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(valid)
	}))
	defer upstream.Close()
	screen := &FyneScreen{Current: app.NewWindow("Radio"), Debug: newDebugWriter(10)}
	defer screen.Current.Close()
	radioWindow(t.Context(), screen)
	station := radiobrowser.Station{UUID: "station", Name: "Station", URL: "https://station.test/live", Favicon: upstream.URL}
	screen.resetRadioSelection(station)
	cache := screen.getRadioArtworkCache()
	base := time.Now()
	var elapsed atomic.Int64
	cache.now = func() time.Time { return base.Add(time.Duration(elapsed.Load())) }
	for range 2 {
		if asset := screen.resolveCurrentRadioArtwork(t.Context(), station.StreamURL()); asset != nil {
			t.Fatal("failed artwork fetch produced artwork")
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("failed artwork retried without backoff: %d requests", requests.Load())
	}
	if logs := strings.Join(debugLogEntries(screen.Debug), ""); !strings.Contains(logs, "radio artwork:") || !strings.Contains(logs, "503") {
		t.Fatalf("artwork failure missing from diagnostics: %q", logs)
	}
	elapsed.Store(int64(radioArtworkRetryDelay + time.Second))
	asset := screen.resolveCurrentRadioArtwork(t.Context(), station.StreamURL())
	if asset == nil || requests.Load() != 2 {
		t.Fatalf("artwork did not recover after retry delay: %v, %d requests", asset, requests.Load())
	}
	row := screen.radio.list.CreateItem().(*radioStationRow)
	fyne.DoAndWait(func() {
		screen.radio.setStations([]radiobrowser.Station{station})
		screen.radio.list.UpdateItem(0, row)
		if row.icon.Resource.Name() != asset.ID+asset.Extension {
			t.Fatal("recovered playback artwork was not shared with station rows")
		}
	})
	if requests.Load() != 2 {
		t.Fatal("station rows fetched artwork already cached by playback")
	}
}

func TestRadioArtworkConcurrentCallersShareFetch(t *testing.T) {
	valid := radioTestPNG(t)
	var requests atomic.Int32
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
			_, _ = w.Write(valid)
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	cache := newRadioArtworkCache()
	type result struct {
		asset *metadata.ArtworkAsset
		err   error
	}
	const callers = 8
	results := make(chan result, callers)
	for range callers {
		go func() {
			asset, err := cache.fetch(ctx, upstream.URL)
			results <- result{asset: asset, err: err}
		}()
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("artwork fetch did not start")
	}
	close(release)
	for range callers {
		select {
		case got := <-results:
			if got.err != nil || got.asset == nil {
				t.Fatalf("shared artwork fetch failed: %v", got.err)
			}
		case <-ctx.Done():
			t.Fatal("artwork caller did not finish")
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("concurrent artwork callers opened %d upstream requests", requests.Load())
	}
}

func TestRadioArtworkCancellationDoesNotBlockNextFetch(t *testing.T) {
	valid := radioTestPNG(t)
	started := make(chan struct{}, 1)
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			started <- struct{}{}
			<-r.Context().Done()
			return
		}
		_, _ = w.Write(valid)
	}))
	defer upstream.Close()
	cache := newRadioArtworkCache()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := cache.fetch(ctx, upstream.URL); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("artwork fetch did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled artwork fetch returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("artwork fetch ignored cancellation")
	}
	ctx, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	asset, err := cache.fetch(ctx, upstream.URL)
	if err != nil || asset == nil || requests.Load() != 2 {
		t.Fatalf("canceled request poisoned artwork cache: %v, %d requests", err, requests.Load())
	}
}
