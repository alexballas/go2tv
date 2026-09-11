//go:build !(android || ios)

package gui

import (
	"testing"

	ttwidget "github.com/alexballas/fyne-tooltip/widget"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/widget"
)

func TestAudioAndImageSubtitlesResetDisableAndRecover(t *testing.T) {
	s, card := newMediaCardTestScreen(t)
	for _, tc := range []struct {
		path     string
		disabled bool
	}{
		{"track.mp3", true}, {"movie.mkv", false}, {"photo.jpg", true}, {"track.flac", true}, {"poster.PNG", true}, {"", false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			card.subtitles.SetSelected(lang.L(subtitleExternal))
			s.subsfile = "captions.srt"
			s.mediafile = tc.path
			card.refresh()
			if !card.subtitles.Visible() || card.subtitles.Disabled() != tc.disabled {
				t.Fatalf("subtitles visible=%v disabled=%v", card.subtitles.Visible(), card.subtitles.Disabled())
			}
			if tc.disabled && (card.subtitles.Selected != lang.L(subtitleAutomatic) || s.subsfile != "" || s.CustomSubsCheck.Checked) {
				t.Fatalf("disabled subtitles mode=%q file=%q custom=%v", card.subtitles.Selected, s.subsfile, s.CustomSubsCheck.Checked)
			}
		})
	}
}

func TestCastButtonTooltipExplainsMissingRequirement(t *testing.T) {
	s, _ := newMediaCardTestScreen(t)
	s.playbackStatus = newPlaybackStatusLabel("")
	s.playPauseToolTip = ttwidget.NewButton("Cast", nil)
	s.PlayPause = &s.playPauseToolTip.Button

	tests := []struct {
		name, device, media, want string
	}{
		{"device and media missing", "", "", "Select a device"},
		{"device missing", "", "track.mp3", "Select a device"},
		{"media missing", "device", "", "Choose media to cast"},
		{"ready", "device", "track.mp3", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s.selectedDevice = devType{addr: tt.device}
			s.mediafile = tt.media
			s.refreshPlaybackReadiness()
			if got := s.playPauseToolTip.ToolTip(); got != tt.want {
				t.Fatalf("tooltip=%q, want %q", got, tt.want)
			}
		})
	}
}

func TestPlaybackReadinessKeepsLayoutAndDisablesIdleActions(t *testing.T) {
	s, _ := newMediaCardTestScreen(t)
	s.playbackStatus = newPlaybackStatusLabel("")
	s.Stop = widget.NewButton("Stop", nil)
	s.SlideBar = newTappableSlider(s)
	for _, tc := range []struct {
		state, media, status string
		active               bool
	}{
		{"Stopped", "", "Choose media to cast", false},
		{"Stopped", "track.mp3", "Ready to cast", false},
		{"Playing", "track.mp3", "Playing", true},
		{"Paused", "track.mp3", "Paused", true},
		{"Stopped", "track.mp3", "Ready to cast", false},
	} {
		t.Run(tc.state+tc.media, func(t *testing.T) {
			s.selectedDevice = devType{addr: "device", name: "Living Room"}
			s.State, s.mediafile = tc.state, tc.media
			s.refreshPlaybackReadiness()
			if s.playbackStatus.Text != tc.status+" · Living Room" {
				t.Fatalf("status=%q", s.playbackStatus.Text)
			}
			if s.Stop.Disabled() == tc.active || s.SlideBar.Disabled() == tc.active {
				t.Fatal("idle/active actions incorrect")
			}
			if !s.playbackStatus.Visible() {
				t.Fatal("playback information disappeared")
			}
		})
	}
}
