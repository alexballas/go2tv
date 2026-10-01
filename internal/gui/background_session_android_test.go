//go:build android

package gui

import (
	"testing"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/test"

	"go2tv.app/go2tv/v2/internal/torrentstream"
)

type backgroundTestApp struct {
	fyne.App
	driver *backgroundTestDriver
}

func TestTorrentCleanupPreservesPendingAndroidPlayback(t *testing.T) {
	tt := []struct {
		name               string
		claimBeforeCleanup bool
	}{
		{name: "Play before queued cleanup", claimBeforeCleanup: true},
		{name: "cancellation completed before Play"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			s := newMobileTorrentTestScreen(t)
			selectMobileTestTorrent(t, s)
			app := fyne.CurrentApp()
			queue := &queuedUIDriver{Driver: app.Driver(), requests: make(chan queuedUIRequest, 32)}
			driver := &backgroundTestDriver{Driver: queue}
			fyne.SetCurrentApp(&backgroundTestApp{App: app, driver: driver})
			t.Cleanup(func() {
				fyne.SetCurrentApp(app)
				backgroundSessionState.running = false
			})
			cancelTorrent(s)
			done := s.torrentCancellationDone()
			if done == nil {
				t.Fatal("cancellation completed before UI cleanup")
			}
			// Hold the real cleanup callbacks, unlike Fyne's immediate test driver.
			var callbacks []queuedUIRequest
			for len(callbacks) == 0 || callbacks[len(callbacks)-1].done == nil {
				select {
				case request := <-queue.requests:
					callbacks = append(callbacks, request)
				case <-done:
					t.Fatal("cancellation signalled completion before UI cleanup")
				case <-time.After(5 * time.Second):
					t.Fatal("cancellation did not reach UI cleanup")
				}
			}
			if tc.claimBeforeCleanup && !claimMobilePlayback(s) {
				t.Fatal("Play rejected")
			}
			stops := driver.stops
			for _, request := range callbacks {
				runQueuedUI(request)
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("cancellation blocked after UI cleanup")
			}
			if tc.claimBeforeCleanup && driver.stops != stops {
				t.Fatal("cleanup stopped the pending cast's foreground service")
			}
			if !tc.claimBeforeCleanup && !claimMobilePlayback(s) {
				t.Fatal("Play rejected after cancellation completed")
			}
			stops = driver.stops
			syncTorrentBackgroundSession(s)
			if driver.stops != stops || !backgroundSessionState.running || !s.PlayPause.Disabled() {
				t.Fatal("pending playback lost its service or enabled duplicate Play")
			}
			// Failed startup must release both the guard and the service.
			finished := make(chan struct{})
			go func() { finishMobilePlayback(s); close(finished) }()
			for {
				select {
				case request := <-queue.requests:
					runQueuedUI(request)
				case <-finished:
					if s.mobilePlaybackStarting() || backgroundSessionState.running {
						t.Fatal("failed startup retained the guard or foreground service")
					}
					return
				case <-time.After(5 * time.Second):
					t.Fatal("startup completion blocked")
				}
			}
		})
	}
}

func (a *backgroundTestApp) Driver() fyne.Driver { return a.driver }

type backgroundTestDriver struct {
	fyne.Driver
	starts, stops int
}

func (d *backgroundTestDriver) StartBackgroundSession(string, string, fyne.Resource) {
	d.starts++
}
func (d *backgroundTestDriver) StopBackgroundSession()         { d.stops++ }
func (d *backgroundTestDriver) RequestNotificationPermission() {}

func TestTorrentKeepsAndroidBackgroundSessionAfterStop(t *testing.T) {
	app := test.NewApp()
	t.Cleanup(app.Quit)
	driver := &backgroundTestDriver{Driver: app.Driver()}
	fyne.SetCurrentApp(&backgroundTestApp{App: app, driver: driver})
	t.Cleanup(func() {
		fyne.SetCurrentApp(app)
		backgroundSessionState.running = false
	})
	s := &FyneScreen{}
	// This test exercises the OS service contract; no torrent reads are needed.
	s.torrent.session = &torrentstream.Session{}
	syncBackgroundSession(s, "Stopped")
	if driver.starts != 1 || driver.stops != 0 {
		t.Fatal("stopped playback must keep torrent downloading in the background")
	}
	syncBackgroundSession(s, "Stopped")
	if driver.starts != 1 {
		t.Fatal("repeated stopped state must not start a second background service")
	}
	s.torrent.session = nil
	s.State = "Paused"
	syncTorrentBackgroundSession(s)
	if driver.stops != 0 {
		t.Fatal("closing a torrent must preserve a paused cast's background service")
	}
	s.State = "Stopped"
	syncTorrentBackgroundSession(s)
	if driver.stops != 1 {
		t.Fatal("canceling the final download must release the background service")
	}
}
