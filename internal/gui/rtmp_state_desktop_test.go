//go:build !(android || ios)

package gui

import (
	"errors"
	"testing"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/internal/radiobrowser"
	"go2tv.app/go2tv/v2/rtmp"
)

func TestRTMPFailedStartPreservesSelectedRadio(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remote bool
	}{
		{"FFmpeg missing", false},
		{"remote session owns renderer", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newRadioPlaybackTestScreen(t)
			// Startup kills go2tv RTMP FFmpeg on this port; keep clear of a real 1935 session.
			fyne.CurrentApp().Preferences().SetString("RTMPPort", "0")
			if tc.remote {
				release, err := s.renderGate.acquireRemoteLease()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(release)
			}
			s.MediaText.OnChanged = s.invalidateRadioURL
			s.rtmpServerCheck = widget.NewCheck("", func(enabled bool) {
				if enabled {
					startRTMPServer(s)
				} else {
					stopRTMPServer(s)
				}
			})
			station := radiobrowser.Station{UUID: "station", Name: "Station", URL: "https://station.test/live"}
			fyne.DoAndWait(func() {
				s.selectRadioStation(station)
				s.rtmpServerCheck.SetChecked(true)
			})
			waitUI(t, 3*time.Second, func() bool { return s.ErrorVisible && !s.rtmpServerCheck.Checked })
			fyne.DoAndWait(func() {
				if !s.ExternalMediaURL.Checked || s.MediaText.Text != station.StreamURL() || s.radioTitle(s.MediaText.Text) != station.Name {
					t.Fatal("failed RTMP startup replaced the selected radio source")
				}
				if !tc.remote && !s.rtmpServerCheck.Disabled() {
					t.Fatal("missing FFmpeg left RTMP enabled after failed startup")
				}
			})
		})
	}
}

func TestRTMPStopRestoresControlsForCurrentFFmpegAvailability(t *testing.T) {
	for _, tc := range []struct {
		name      string
		ffmpegErr error
	}{
		{"FFmpeg still available", nil},
		{"FFmpeg removed while RTMP running", errors.New("FFmpeg unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newMediaCardTestScreen(t)
			s.TranscodeCheckBox = widget.NewCheck("", nil)
			s.ScreencastCheckBox = widget.NewCheck("", nil)
			s.SlideBar = newTappableSlider(s)
			s.rtmpServerCheck = widget.NewCheck("", nil)
			s.rtmpServerCheck.SetChecked(true)
			s.rtmpServer = rtmp.NewServer()
			s.rtmpURLCard = widget.NewCard("RTMP", "", nil)
			s.rtmpURLEntry, s.rtmpKeyEntry = widget.NewEntry(), widget.NewEntry()
			s.rtmpURLEntry.SetText("rtmp://localhost/live/")
			s.rtmpPrevExternalMediaURL = true
			s.rtmpPrevMediaText = "https://station.test/live"
			s.ExternalMediaURL.SetChecked(true)
			s.ExternalMediaURL.Disable()
			s.ffmpegCheckValid, s.ffmpegCheckErr = true, tc.ffmpegErr
			s.TranscodeCheckBox.Disable()
			s.ScreencastCheckBox.Disable()
			fyne.DoAndWait(func() { stopRTMPServer(s) })
			waitUI(t, time.Second, func() bool { return s.rtmpURLEntry.Text == "" })
			fyne.DoAndWait(func() {
				missing := tc.ffmpegErr != nil
				if s.rtmpServerCheck.Disabled() != missing || s.TranscodeCheckBox.Disabled() != missing || s.ScreencastCheckBox.Disabled() != missing {
					t.Fatalf("after RTMP stop: RTMP=%v transcode=%v desktop=%v disabled; FFmpeg missing=%v", s.rtmpServerCheck.Disabled(), s.TranscodeCheckBox.Disabled(), s.ScreencastCheckBox.Disabled(), missing)
				}
				if s.rtmpServerCheck.Checked || s.ExternalMediaURL.Disabled() || s.MediaText.Disabled() || s.MediaText.Text != s.rtmpPrevMediaText || s.rtmpURLCard.Visible() {
					t.Fatal("RTMP stop failed to restore editable URL source")
				}
			})
		})
	}
}
