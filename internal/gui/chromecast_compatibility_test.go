//go:build !(android || ios)

package gui

import (
	"context"
	"testing"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/devices"
)

func TestChromecastCompatibilityDoesNotBlockWaitingTorrent(t *testing.T) {
	tt := []struct {
		name             string
		change           func(*FyneScreen)
		apply            bool
		cancel           bool
		initialTranscode bool
	}{
		{name: "same selection enables transcoding", apply: true},
		{name: "changed media ignores old result", change: func(s *FyneScreen) { s.mediafile = "replacement.mp4" }},
		{name: "changed device ignores old result", change: func(s *FyneScreen) { s.selectedDeviceType = devices.DeviceTypeDLNA }},
		{name: "changed ffmpeg ignores old result", change: func(s *FyneScreen) { s.ffmpegPath = "replacement-ffmpeg" }},
		{name: "shutdown cancels blocked probe", cancel: true},
		{name: "manual disable stays disabled", initialTranscode: true, change: func(s *FyneScreen) { s.TranscodeCheckBox.SetChecked(false) }},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			app := test.NewApp()
			t.Cleanup(app.Quit)
			s := newQueueMediaSelectionTestScreen()
			parent, cancelParent := context.WithCancel(context.Background())
			t.Cleanup(cancelParent)
			s.torrent.ctx = parent
			session, path := newTestTorrentSession(t)
			s.mediafile = path
			s.selectedDeviceType = devices.DeviceTypeChromecast
			s.selectedDevice = devType{addr: "http://127.0.0.1:8009", deviceType: devices.DeviceTypeChromecast}
			ffmpeg, entered, release := torrentTestBlockedProbe(t)
			s.ffmpegPath = ffmpeg
			s.TranscodeCheckBox = widget.NewCheck("", func(checked bool) { s.Transcode = checked })
			s.TranscodeCheckBox.SetChecked(tc.initialTranscode)
			queue := &queuedUIDriver{Driver: app.Driver(), requests: make(chan queuedUIRequest, 16)}
			fyne.SetCurrentApp(&queuedUIApp{App: app, driver: queue})
			t.Cleanup(func() { fyne.SetCurrentApp(app) })
			nextUI := func() {
				t.Helper()
				select {
				case request := <-queue.requests:
					runQueuedUI(request)
				case <-time.After(5 * time.Second):
					t.Fatal("UI callback did not arrive")
				}
			}
			drainUI := func() {
				for {
					select {
					case request := <-queue.requests:
						runQueuedUI(request)
					default:
						return
					}
				}
			}

			returned := make(chan struct{})
			go func() {
				s.checkChromecastCompatibility()
				close(returned)
			}()
			t.Cleanup(func() { release(); <-returned })
			select {
			case <-returned:
			case <-time.After(time.Second):
				t.Fatal("device selection blocked on torrent codec probe")
			}
			nextUI()
			drainUI()
			waitForTorrentTestProbe(t, entered)
			if completed, _ := session.Progress(); completed != 0 {
				t.Fatal("test torrent unexpectedly downloaded data")
			}

			// Other user actions must run while the probe still needs data.
			responsive := false
			fyne.Do(func() {
				responsive = true
				if tc.change != nil {
					tc.change(s)
				}
			})
			nextUI()
			if !responsive || s.TranscodeCheckBox.Checked && !tc.initialTranscode {
				t.Fatal("pending codec probe blocked UI or changed transcoding early")
			}
			if tc.cancel {
				cancelParent()
			} else {
				release()
			}
			nextUI()
			if s.TranscodeCheckBox.Checked != tc.apply || s.Transcode != tc.apply {
				t.Fatalf("transcoding enabled = %v, want %v", s.TranscodeCheckBox.Checked, tc.apply)
			}
			drainUI()
			if s.PlayPause.Disabled() {
				t.Fatal("probe completion left Cast disabled")
			}
		})
	}
}
