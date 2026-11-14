//go:build android || ios

package gui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/storage"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/radiobrowser"
	"go2tv.app/go2tv/v2/utils"
)

func TestMobileTrackSettingsDescribeAvailableChoices(t *testing.T) {
	tt := []struct {
		name, input, title           string
		remote, radio, multipleAudio bool
		visible                      bool
	}{
		{name: "empty local source"},
		{name: "empty URL", remote: true},
		{name: "whitespace URL", input: "  ", remote: true},
		{name: "radio", input: "https://station.test/live", remote: true, radio: true},
		{name: "audio file", input: "music.mp3"},
		{name: "image", input: "photo.jpg"},
		{name: "video with one audio track", input: "movie.mkv", visible: true, title: "Subtitles"},
		{name: "video URL", input: "https://example.test/movie.mp4", remote: true, visible: true, title: "Subtitles"},
		{name: "video with audio choices", input: "movie.mkv", multipleAudio: true, visible: true, title: "Audio & subtitles"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			s := newMobileTorrentTestScreen(t)
			s.ExternalMediaURL.SetChecked(tc.remote)
			if tc.radio {
				s.resetRadioSelection(radiobrowser.Station{UUID: "station", URL: tc.input, Codec: "MP3"})
			}
			if !tc.remote && tc.input != "" {
				s.mediafile = storage.NewFileURI("/sdcard/" + tc.input)
			}
			s.MediaText.SetText(tc.input)
			clear := widget.NewButton("", nil)
			c := newMobileTracks(s, widget.NewButton("", nil), clear, &mobileSubtitleControls{clear: clear, field: newValueField(nil)})
			if tc.multipleAudio {
				s.renderAudioPicker([]utils.AudioTrack{{Number: 1, Language: "jpn", Default: true}, {Number: 2, Language: "eng"}}, 0)
			}
			c.refresh(s)
			if c.summary.Visible() != tc.visible {
				t.Fatalf("settings visible = %v, want %v", c.summary.Visible(), tc.visible)
			}
			c.show(s)
			if !tc.visible {
				if c.sheet != nil {
					t.Fatal("irrelevant settings sheet opened")
				}
				return
			}
			if c.summary.heading.Text != tc.title || c.title.Text != tc.title || strings.Contains(c.summary.label.Text, "Default audio") || strings.Contains(c.summary.label.Text, "Embedded Track") {
				t.Fatalf("misleading settings summary: %q, sheet title: %q", c.summary.label.Text, c.title.Text)
			}
			if c.audioRow.Visible() != tc.multipleAudio {
				t.Fatal("sheet title and audio choices disagree")
			}
			for _, option := range c.modeSelect.Options {
				if option == "From video" {
					t.Fatal("offered a video track before any were discovered")
				}
			}
			c.sheet.close()
		})
	}
}

func TestMobileDevicesMoveUpWhenTrackSettingsDisappear(t *testing.T) {
	s := newMobileTorrentTestScreen(t)
	s.mediafile = storage.NewFileURI("/sdcard/movie.mkv")
	s.MediaText.SetText("movie.mkv")
	clear := widget.NewButton("", nil)
	c := newMobileTracks(s, widget.NewButton("", nil), clear, &mobileSubtitleControls{clear: clear, field: newValueField(nil)})
	source := container.NewVScroll(container.NewVBox(widget.NewLabel("Media"), c.summary, widget.NewCheck("Loop", nil)))
	devices, player := widget.NewLabel("Devices"), widget.NewLabel("Play")
	content := container.New(&mobileMainLayout{source: source}, newContentSized(source), devices, player)
	c.relayout = content.Refresh
	s.Current.SetContent(content)
	s.Current.Resize(fyne.NewSize(390, 800))
	c.refresh(s)
	videoY, playerY := devices.Position().Y, player.Position().Y
	s.mediafile = nil
	s.ExternalMediaURL.SetChecked(true)
	s.MediaText.SetText("https://station.test/live.mp3")
	c.refresh(s)
	if c.summary.Visible() || devices.Position().Y >= videoY {
		t.Fatalf("radio retained video settings space: devices y=%v, previously %v", devices.Position().Y, videoY)
	}
	if got, want := devices.Position().Y, source.Content.MinSize().Height+theme.Padding(); got != want {
		t.Fatalf("gap below radio controls: devices y=%v, want %v", got, want)
	}
	if player.Position().Y != playerY {
		t.Fatal("source shrink moved the bottom playback bar")
	}
}

func TestMobileSubtitleSheetManualSelection(t *testing.T) {
	s := newMobileTorrentTestScreen(t)
	s.MediaText.SetText("movie.mkv")
	s.mediafile = storage.NewFileURI("/sdcard/movie.mkv")
	field := newValueField(nil)
	clear := widget.NewButton("", func() { clearsubsAction(s) })
	c := newMobileTracks(s, widget.NewButton("", nil), clear, &mobileSubtitleControls{clear: clear, field: field})
	s.SubsText.OnChanged = func(value string) { field.Set(value, ""); c.refreshSummary(s) }
	c.setMode(s, mobileSubsExternal)
	setMobileSubtitleURI(s, storage.NewFileURI("/sdcard/Gundam.en.srt"))
	c.show(s)
	if !c.externalRow.Visible() || c.embeddedRow.Visible() || !strings.Contains(c.summary.label.Text, "Gundam.en.srt") {
		t.Fatalf("manual selection missing: %q", c.summary.label.Text)
	}
	c.sheet.close()
	c.show(s)
	if c.mode != mobileSubsExternal || c.modeSelect.Selected != "Choose subtitle file…" || s.subsfile == nil || field.label.Text != "Gundam.en.srt" {
		t.Fatalf("reopening sheet lost manual file: %v %q", s.subsfile, field.label.Text)
	}
	clear.OnTapped()
	if s.subsfile != nil || c.mode != mobileSubsExternal || strings.Contains(c.summary.label.Text, "Gundam.en.srt") {
		t.Fatalf("Clear kept subtitles or left manual mode: %v %q", s.subsfile, c.summary.label.Text)
	}
	c.setMode(s, mobileSubsNone)
	if c.externalRow.Visible() || !strings.Contains(c.summary.label.Text, "Off") {
		t.Fatalf("None still exposes manual file: %q", c.summary.label.Text)
	}
	c.sheet.close()
}

func TestMobileEmbeddedSubtitleChoiceReachesReceiver(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	dir := t.TempDir()
	args := []string{"-nostdin", "-v", "error"}
	for i, text := range []string{"First language", "Selected language"} {
		path := filepath.Join(dir, fmt.Sprintf("%d.srt", i))
		if err := os.WriteFile(path, []byte("1\n00:00:01,000 --> 00:00:02,000\n"+text+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "-i", path)
	}
	path := filepath.Join(dir, "movie.mkv")
	args = append(args, "-map", "0:s", "-map", "1:s", "-c:s", "srt", path)
	if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	s := newMobileTorrentTestScreen(t)
	s.ffmpegPath, s.mediafile = ffmpeg, storage.NewFileURI(path)
	s.MediaText.SetText("movie.mkv")
	s.tracks.mode, s.tracks.track = mobileSubsEmbedded, 1
	t.Cleanup(func() { s.tracks.clearExtracted(s) })
	if err := prepareMobileSubtitles(s, t.Context(), false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.subsfile.Path())
	if err != nil || !strings.Contains(string(data), "Selected language") || strings.Contains(string(data), "First language") {
		t.Fatalf("DLNA subtitle source: %q, %v", data, err)
	}
	s.httpserver = httphandlers.NewServer("")
	url, err := registerMobileChromecastSubtitles(s, "host:1234", 0, false)
	response := httptest.NewRecorder()
	s.httpserver.ServeMediaHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://host:1234/subtitles.vtt", nil))
	if err != nil || url == "" || !strings.Contains(response.Body.String(), "Selected language") {
		t.Fatalf("Chromecast captions: %q %v %q", url, err, response.Body.String())
	}
	// Selecting a manual file replaces the extracted track and removes its cache.
	extracted := s.tracks.extracted
	setMobileSubtitleURI(s, storage.NewFileURI("/sdcard/manual.srt"))
	if s.tracks.mode != mobileSubsExternal || s.subsfile.Name() != "manual.srt" {
		t.Fatal("manual file did not override embedded track")
	}
	if _, err := os.Stat(extracted); !os.IsNotExist(err) {
		t.Fatalf("extracted cache retained: %v", err)
	}
}

func TestMobileTrackSheetFitsPortraitAndLandscape(t *testing.T) {
	s := newMobileTorrentTestScreen(t)
	s.MediaText.SetText("movie.mkv")
	s.mediafile = storage.NewFileURI("/sdcard/movie.mkv")
	clear := widget.NewButton("", nil)
	c := newMobileTracks(s, widget.NewButton("", nil), clear, &mobileSubtitleControls{clear: clear, field: newValueField(nil)})
	c.setMode(s, mobileSubsExternal)
	s.Current.SetContent(widget.NewLabel("Go2TV"))
	tt := []struct {
		name string
		size fyne.Size
	}{
		{"small portrait", fyne.NewSize(320, 480)},
		{"landscape", fyne.NewSize(800, 320)},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			s.Current.Resize(tc.size)
			c.show(s)
			s.Current.Canvas().Capture()
			// The overlay uses the actual canvas, including any platform safe area.
			panel := c.sheet.content
			if panel.Size().Width == 0 || panel.Size().Height == 0 || panel.Position().Y < 0 || panel.Position().Y+panel.Size().Height > c.sheet.Size().Height || panel.Size().Width > c.sheet.Size().Width {
				t.Fatalf("sheet exceeds canvas: pos=%v size=%v canvas=%v", panel.Position(), panel.Size(), c.sheet.Size())
			}
			c.sheet.close()
		})
	}
}
