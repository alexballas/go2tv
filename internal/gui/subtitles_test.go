//go:build !(android || ios)

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

	"github.com/alexballas/refyne/v2/lang"

	"go2tv.app/go2tv/v2/httphandlers"
)

func TestChromecastEmbeddedSubtitleSelection(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	dir := t.TempDir()
	args := []string{"-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "color=size=16x16:duration=1"}
	for i, caption := range []string{"First caption", "Second caption"} {
		path := filepath.Join(dir, fmt.Sprintf("%d.srt", i))
		if err := os.WriteFile(path, []byte("1\n00:00:00,000 --> 00:00:01,000\n"+caption+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "-i", path)
	}
	media := filepath.Join(dir, "embedded.mkv")
	args = append(args, "-map", "0:v", "-map", "1:s", "-map", "2:s", "-c:v", "mpeg4", "-c:s", "ass",
		"-metadata:s:s:0", "title=First", "-metadata:s:s:1", "title=Second", media)
	if output, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("create ASS media: %v: %s", err, output)
	}
	noSubs := filepath.Join(dir, "no-subs.mkv")
	if output, err := exec.Command(ffmpeg, "-nostdin", "-loglevel", "error", "-i", media,
		"-map", "0:v", "-c", "copy", noSubs).CombinedOutput(); err != nil {
		t.Fatalf("create media without subtitles: %v: %s", err, output)
	}
	sidecar := filepath.Join(dir, "sidecar.srt")
	if err := os.WriteFile(sidecar, []byte("1\n00:00:00,000 --> 00:00:01,000\nSidecar caption\n"), 0600); err != nil {
		t.Fatal(err)
	}
	tt := []struct {
		name, mode, selected, want        string
		sidecar, url, screencast          bool
		failExtraction, bitmap, wantError bool
	}{
		{name: "automatic first ASS track", mode: subtitleAutomatic, want: "First caption"},
		{name: "automatic sidecar takes priority", mode: subtitleAutomatic, sidecar: true, want: "Sidecar caption"},
		{name: "manual second track", mode: subtitleEmbedded, selected: "Second", want: "Second caption"},
		{name: "none disables fallback", mode: subtitleNone},
		{name: "external file", mode: subtitleExternal, sidecar: true, want: "Sidecar caption"},
		{name: "external without file", mode: subtitleExternal},
		{name: "embedded without selection", mode: subtitleEmbedded},
		{name: "URL excludes local tracks", mode: subtitleAutomatic, url: true},
		{name: "screencast excludes local tracks", mode: subtitleAutomatic, screencast: true},
		{name: "automatic extraction failure allows playback", mode: subtitleAutomatic, failExtraction: true},
		{name: "manual extraction failure reported", mode: subtitleEmbedded, selected: "Second", failExtraction: true, wantError: true},
		{name: "automatic bitmap track allows playback", mode: subtitleAutomatic, bitmap: true},
		{name: "manual bitmap track failure reported", mode: subtitleEmbedded, selected: "eng", bitmap: true, wantError: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			screen, card := newMediaCardTestScreen(t)
			screen.ffmpegPath = ffmpeg
			mediaPath := media
			if tc.bitmap {
				// Minimal Matroska with a PGS track exercises FFmpeg's actual
				// bitmap-to-text rejection, without a large video fixture.
				mediaPath = filepath.Join("testdata", "bitmap-subtitles.mkv")
			}
			if err := setCurrentMediaPath(screen, mediaPath); err != nil {
				t.Fatal(err)
			}
			if tc.bitmap && len(screen.SelectInternalSubs.Options) != 1 {
				t.Fatalf("bitmap subtitle not detected: %v", screen.SelectInternalSubs.Options)
			}
			card.subtitles.SetSelected(lang.L(tc.mode))
			if tc.selected != "" {
				screen.SelectInternalSubs.SetSelected(tc.selected)
			}
			if tc.sidecar {
				screen.subsfile = sidecar
			}
			if tc.url {
				screen.ExternalMediaURL.SetChecked(true)
			}
			screen.Screencast = tc.screencast
			if tc.failExtraction {
				screen.ffmpegPath = filepath.Join(dir, "missing-ffmpeg")
			}
			t.Cleanup(func() {
				for _, path := range screen.tempFiles {
					if err := os.Remove(path); err != nil {
						t.Error(err)
					}
				}
			})
			err := extractChromecastSubtitles(screen)
			if tc.wantError {
				if err == nil || screen.subsfile != "" || len(screen.tempFiles) != 0 {
					t.Fatalf("failed extraction: error=%v path=%q temp=%v", err, screen.subsfile, screen.tempFiles)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if card.subtitles.Selected != lang.L(tc.mode) || screen.SelectInternalSubs.Selected != tc.selected {
				t.Fatal("playback changed subtitle preference")
			}
			server := httphandlers.NewServer("127.0.0.1:0")
			url, err := registerChromecastSubtitles(server, "192.0.2.1:8080", screen.subsfile, 0)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if url != "" || len(screen.tempFiles) != 0 {
					t.Fatalf("unexpected subtitles: URL=%q temp=%v", url, screen.tempFiles)
				}
				return
			}
			recorder := httptest.NewRecorder()
			server.ServeMediaHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/subtitles.vtt", nil))
			if url == "" || recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), tc.want) {
				t.Fatalf("caption response: URL=%q status=%d body=%s", url, recorder.Code, recorder.Body.String())
			}
			if tc.mode == subtitleAutomatic {
				// Queue advancement must discard an automatically extracted caption.
				if err := setCurrentMediaPath(screen, noSubs); err != nil {
					t.Fatal(err)
				}
				if err := extractChromecastSubtitles(screen); err != nil {
					t.Fatal(err)
				}
				url, err := registerChromecastSubtitles(server, "192.0.2.1:8080", screen.subsfile, 0)
				if err != nil || url != "" {
					t.Fatalf("next file retained captions: URL=%q error=%v", url, err)
				}
			}
		})
	}
}

func TestChromecastSubtitlesAcrossServerRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "embedded.srt")
	if err := os.WriteFile(path, []byte("1\n00:00:10,000 --> 00:00:12,000\nCaption\n"), 0600); err != nil {
		t.Fatal(err)
	}
	tt := []struct {
		name string
		seek int
		want string
	}{
		{"transcoded playback", 0, "00:00:10.000 --> 00:00:12.000"},
		{"seek restarts server", 10, "00:00:00.000 --> 00:00:02.000"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			server := httphandlers.NewServer("127.0.0.1:0")
			url, err := registerChromecastSubtitles(server, "192.0.2.1:8080", path, tc.seek)
			if err != nil {
				t.Fatal(err)
			}
			if url != "http://192.0.2.1:8080/subtitles.vtt" {
				t.Fatalf("subtitle URL = %q", url)
			}
			recorder := httptest.NewRecorder()
			server.ServeMediaHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/subtitles.vtt", nil))
			if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), tc.want) || !strings.Contains(recorder.Body.String(), "Caption") {
				t.Fatalf("caption response: %d %s", recorder.Code, recorder.Body.String())
			}
			if got := recorder.Header().Get("Content-Type"); got != "text/vtt; charset=utf-8" {
				t.Fatalf("content type = %q", got)
			}
			if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "*" {
				t.Fatalf("CORS = %q", got)
			}
			// Moving to a file without subtitles must discard the previous track.
			url, err = registerChromecastSubtitles(server, "192.0.2.1:8080", "", 0)
			if err != nil || url != "" {
				t.Fatalf("cleared subtitles: URL = %q, error = %v", url, err)
			}
			recorder = httptest.NewRecorder()
			server.ServeMediaHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/subtitles.vtt", nil))
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("stale captions still served: %d", recorder.Code)
			}
		})
	}
}
