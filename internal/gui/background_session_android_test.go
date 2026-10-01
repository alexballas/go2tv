//go:build android

package gui

import (
	"testing"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/test"

	"go2tv.app/go2tv/v2/internal/torrentstream"
)

type backgroundTestApp struct {
	fyne.App
	driver *backgroundTestDriver
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
