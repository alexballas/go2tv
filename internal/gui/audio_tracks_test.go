//go:build !(android || ios)

package gui

import (
	"path/filepath"
	"testing"

	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/utils"
)

func TestMediaCardAudioPickerChoosesTrackForNextCast(t *testing.T) {
	s, _ := newMediaCardTestScreen(t)
	movie := filepath.Join(t.TempDir(), "movie.mkv")
	if err := setCurrentMediaPath(s, movie); err != nil {
		t.Fatal(err)
	}
	row := s.audioPicker.objects[0]
	if row.Visible() {
		t.Fatal("picker shown before tracks are known")
	}
	s.audio.mu.Lock()
	s.audio.tracks = []utils.AudioTrack{
		{Number: 1, Language: "jpn", Codec: "ac3", Channels: 6, Default: true},
		{Number: 2, Language: "eng", Codec: "aac", Channels: 2},
	}
	s.audio.mu.Unlock()
	s.refreshAudioPicker()
	picker := s.audioPicker.selectWidget
	if !row.Visible() || picker.Disabled() || picker.Selected != "Japanese · AC3 5.1 (default)" {
		t.Fatalf("picker visible=%v disabled=%v selected=%q", row.Visible(), picker.Disabled(), picker.Selected)
	}
	if track, transcode := s.audioCast(movie, false); track != 0 || transcode {
		t.Fatalf("source default re-streams: track=%d transcode=%v", track, transcode)
	}

	picker.SetSelectedIndex(1)
	for _, userTranscode := range []bool{false, true} {
		if track, transcode := s.audioCast(movie, userTranscode); track != 2 || !transcode {
			t.Fatalf("audioCast(transcode=%v) = %d %v, want 2 true", userTranscode, track, transcode)
		}
	}
	if s.castAudio() != 2 {
		t.Fatal("session lost the track needed for seek restarts")
	}

	s.State = "Playing"
	s.refreshAudioPicker()
	if !picker.Disabled() {
		t.Fatal("track changed during playback")
	}

	if err := setCurrentMediaPath(s, filepath.Join(t.TempDir(), "other.mkv")); err != nil {
		t.Fatal(err)
	}
	if row.Visible() {
		t.Fatal("previous media's tracks shown for new media")
	}
	if track, transcode := s.audioCast(movie, false); track != 0 || transcode {
		t.Fatal("choice survived a media change")
	}
}

func TestAudioTrackFollowsTranscodeAndFFmpeg(t *testing.T) {
	s, _ := newMediaCardTestScreen(t)
	s.TranscodeCheckBox = widget.NewCheck("", func(on bool) {
		s.Transcode = on
		s.audioTranscodeToggled(on)
	})
	movie := filepath.Join(t.TempDir(), "movie.mkv")
	if err := setCurrentMediaPath(s, movie); err != nil {
		t.Fatal(err)
	}
	s.audio.mu.Lock()
	s.audio.tracks = []utils.AudioTrack{{Number: 1, Default: true}, {Number: 2}}
	s.audio.mu.Unlock()
	s.refreshAudioPicker()
	picker, transcode := s.audioPicker.selectWidget, s.TranscodeCheckBox

	picker.SetSelectedIndex(1)
	if !transcode.Checked || !s.Transcode {
		t.Fatal("other track did not turn Transcode on")
	}
	picker.SetSelectedIndex(0)
	if transcode.Checked {
		t.Fatal("default track kept the Transcode it turned on")
	}

	picker.SetSelectedIndex(1)
	transcode.SetChecked(false)
	if picker.SelectedIndex() != 0 || s.castAudioFor(movie) != 0 {
		t.Fatal("turning Transcode off kept the other track")
	}

	transcode.SetChecked(true)
	picker.SetSelectedIndex(1)
	picker.SetSelectedIndex(0)
	if !transcode.Checked {
		t.Fatal("default track turned off the user's Transcode")
	}

	transcode.SetChecked(false)
	picker.SetSelectedIndex(1)
	s.ffmpegPath = filepath.Join(t.TempDir(), "missing-ffmpeg")
	s.refreshAudioPicker()
	if s.audioPicker.objects[0].Visible() || transcode.Checked || s.castAudioFor(movie) != 0 {
		t.Fatal("track probed with the previous FFmpeg survived a path change")
	}
}

// castAudioFor resolves a cast without the user's Transcode choice.
func (s *FyneScreen) castAudioFor(media string) int {
	track, _ := s.audioCast(media, false)
	return track
}
