//go:build !(android || ios)

package gui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/internal/radiobrowser"
)

func TestRadioSongTitleClearsOnStopAndRejectsPreviousSession(t *testing.T) {
	closed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("icy-metaint", "4")
		w.Header().Set("Content-Type", "audio/mpeg")
		block := "StreamTitle='Artist - Song';"
		units := (len(block) + 15) / 16
		fmt.Fprint(w, "data")
		w.Write([]byte{byte(units)})
		fmt.Fprint(w, block+strings.Repeat("\x00", units*16-len(block)))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		closed <- struct{}{}
	}))
	defer server.Close()
	screen, _ := newMediaCardTestScreen(t)
	status := newPlaybackStatusLabel("")
	screen.playbackStatus = status
	screen.State = "Playing"
	screen.setPlayingMediaPath(server.URL)
	screen.radioSelected = radiobrowser.Station{UUID: "one", Name: "Station", URL: server.URL}
	screen.startRadioSongMonitor(server.URL)
	defer screen.stopRadioSongMonitor()
	waitRadioUI(t, func() bool { return strings.Contains(status.Text, "Artist - Song") })
	screen.mu.RLock()
	previousGeneration := screen.radioSong.generation
	screen.mu.RUnlock()
	fyne.DoAndWait(func() { screen.updateScreenState("Stopped") })
	waitRadioUI(t, func() bool { return !strings.Contains(status.Text, "Artist - Song") })
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("radio track connection survived Stop")
	}
	fyne.DoAndWait(func() {
		screen.State = "Playing"
		screen.applyRadioSongTitle(previousGeneration, server.URL, "Stale title")
		if strings.Contains(status.Text, "Stale title") {
			t.Fatal("previous session's metadata reappeared")
		}
		screen.radioSelected = radiobrowser.Station{UUID: "two", URL: "https://other.test/live"}
		screen.applyRadioSongTitle(screen.radioSong.generation, server.URL, "Wrong station")
		if strings.Contains(status.Text, "Wrong station") {
			t.Fatal("old station title survived URL switch")
		}
	})
}

func TestRadioSongStatusUsesStationNameWithoutICY(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	screen, _ := newMediaCardTestScreen(t)
	status := newPlaybackStatusLabel("")
	screen.playbackStatus = status
	screen.State = "Playing"
	screen.setPlayingMediaPath(server.URL)
	screen.radioSelected = radiobrowser.Station{UUID: "one", Name: "Station", URL: server.URL}
	screen.startRadioSongMonitor(server.URL)
	defer screen.stopRadioSongMonitor()
	deadline := time.Now().Add(time.Second)
	for requests.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if requests.Load() == 0 {
		t.Fatal("non-ICY station was not checked")
	}
	fyne.DoAndWait(func() {
		if status.Text != "Playing · Station" {
			t.Fatalf("station without ICY status = %q", status.Text)
		}
	})
	fyne.DoAndWait(func() { screen.radioSelected.HLS = 1 })
	screen.startRadioSongMonitor(server.URL)
	fyne.DoAndWait(func() {
		if status.Text != "Playing · Station" {
			t.Fatalf("HLS station status = %q", status.Text)
		}
	})
}

func TestRadioSongStopDuringMonitorStartup(t *testing.T) {
	requested := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested <- struct{}{}
		w.Header().Set("icy-metaint", "4")
		w.Header().Set("Content-Type", "audio/mpeg")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	screen, _ := newMediaCardTestScreen(t)
	status := newPlaybackStatusLabel("")
	screen.playbackStatus = status
	screen.State = "Playing"
	screen.radioSelected = radiobrowser.Station{UUID: "one", URL: server.URL}
	parent := &blockedRadioParentContext{Context: context.Background(), entered: make(chan struct{}), release: make(chan struct{})}
	screen.radio = &radioView{parentCtx: parent}
	unblock := sync.OnceFunc(func() { close(parent.release) })
	done := make(chan struct{})
	go func() {
		screen.startRadioSongMonitor(server.URL)
		close(done)
	}()
	defer func() {
		unblock()
		<-done
		screen.stopRadioSongMonitor()
		server.Close()
	}()
	select {
	case <-parent.entered:
	case <-time.After(time.Second):
		t.Fatal("metadata startup did not reach context preparation")
	}
	// Stop wins while metadata startup is still preparing its child context.
	fyne.DoAndWait(func() { screen.updateScreenState("Stopped") })
	unblock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("metadata startup did not finish after Stop")
	}
	select {
	case <-requested:
		t.Fatal("metadata startup reopened the station after Stop")
	case <-time.After(100 * time.Millisecond):
	}
	fyne.DoAndWait(func() {
		if strings.Contains(status.Text, "Now playing") || screen.radioSong.title != "" {
			t.Fatal("stopped station retained its track label")
		}
	})
}

func TestRadioSongSharesFilenameLineWithoutGrowingPlayback(t *testing.T) {
	screen, _ := newMediaCardTestScreen(t)
	screen.playbackStatus = newPlaybackStatusLabel("")
	screen.playbackStatus.Truncation = fyne.TextTruncateEllipsis
	station := radiobrowser.Station{UUID: "radio", Name: "Station", URL: "https://station.test/live"}
	screen.radioSelected = station
	screen.selectedDevice = devType{addr: "device", name: "Living Room"}
	screen.setPlayingMediaPath(station.StreamURL())
	screen.State = "Playing"
	screen.radioSong.mediaURL = station.StreamURL()
	controls := container.NewVBox(screen.playbackStatus, widget.NewSlider(0, 100), widget.NewButton("Pause", nil))
	art := newSelectedArtwork()
	row := container.New(artworkPlaybackLayout{}, art, controls)
	row.Resize(fyne.NewSize(640, row.MinSize().Height))
	screen.refreshPlaybackReadiness()
	before, artworkSize := row.MinSize(), art.Size()
	title := "Artist - " + strings.Repeat("Long song title ", 20)
	screen.applyRadioSongTitle(screen.radioSong.generation, station.StreamURL(), title)
	row.Refresh()
	if screen.playbackStatus.Text != "Playing · Living Room · "+title || screen.playbackStatus.filename != title {
		t.Fatalf("radio track did not replace filename: %q", screen.playbackStatus.Text)
	}
	if row.MinSize() != before || art.Size() != artworkSize {
		t.Fatal("radio metadata grew playback controls or artwork")
	}
	screen.updateScreenState("Paused")
	fyne.DoAndWait(func() {
		if screen.playbackStatus.Text != "Paused · Living Room · "+title {
			t.Fatalf("paused radio lost track: %q", screen.playbackStatus.Text)
		}
	})
	screen.applyRadioSongTitle(screen.radioSong.generation, station.StreamURL(), "")
	if screen.playbackStatus.Text != "Paused · Living Room · Station" {
		t.Fatalf("empty track lost station fallback: %q", screen.playbackStatus.Text)
	}
}

type blockedRadioParentContext struct {
	context.Context
	entered, release chan struct{}
	once             sync.Once
}

func (c *blockedRadioParentContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	<-c.release
	return c.Context.Done()
}
