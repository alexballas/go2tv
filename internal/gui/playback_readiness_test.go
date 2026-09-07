//go:build !(android || ios)

package gui

import (
	"testing"

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

func TestPlaybackReadinessKeepsLayoutAndDisablesIdleActions(t *testing.T) {
	s, _ := newMediaCardTestScreen(t)
	s.playbackTitle = widget.NewLabel("")
	s.playbackStatus = widget.NewLabel("")
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
			if !s.playbackTitle.Visible() || !s.playbackStatus.Visible() {
				t.Fatal("playback information disappeared")
			}
		})
	}
}
