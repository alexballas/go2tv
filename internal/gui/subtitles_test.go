//go:build !(android || ios)

package gui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/widget"

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
		burn, transcode, native           bool
		receiverFirst                     bool
		track                             int
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
		{name: "automatic ASS burn preserves original", mode: subtitleAutomatic, burn: true, transcode: true, native: true},
		{name: "switch receiver captions to original ASS burn", mode: subtitleAutomatic, burn: true, transcode: true, native: true, receiverFirst: true},
		{name: "manual ASS burn preserves selected track", mode: subtitleEmbedded, selected: "Second", burn: true, transcode: true, native: true, track: 1},
		{name: "burn keeps sidecar priority", mode: subtitleAutomatic, burn: true, transcode: true, sidecar: true, want: "Sidecar caption"},
		{name: "burn ignored during direct playback", mode: subtitleAutomatic, burn: true, want: "First caption"},
		{name: "transcode keeps receiver when burn disabled", mode: subtitleAutomatic, transcode: true, want: "First caption"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			screen, card := newMediaCardTestScreen(t)
			screen.ffmpegPath = ffmpeg
			screen.Transcode, screen.castBurnSubtitles = tc.transcode, tc.burn
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
			priorTemps := 0
			if tc.receiverFirst {
				screen.castBurnSubtitles = false
				if err := extractChromecastSubtitles(screen); err != nil || screen.subsfile == "" {
					t.Fatalf("prepare receiver captions: %v", err)
				}
				priorTemps = len(screen.tempFiles)
				screen.castBurnSubtitles = true
			}
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
			if tc.native {
				opts := desktopChromecastTranscodeOptions(screen, 30)
				if opts.EmbeddedSubtitle == nil || opts.EmbeddedSubtitle.Path != media || opts.EmbeddedSubtitle.Track != tc.track || opts.SubsPath != "" || len(screen.tempFiles) != priorTemps {
					t.Fatalf("original subtitle source lost: %+v, temps=%v", opts.EmbeddedSubtitle, screen.tempFiles)
				}
				server.AddHandler("/subtitles.vtt", nil, nil, []byte("stale captions"))
				url, err := registerDesktopChromecastSubtitles(screen, server, "host:1234", 30, true)
				response := httptest.NewRecorder()
				server.ServeMediaHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/subtitles.vtt", nil))
				if err != nil || url != "" || response.Code != http.StatusNotFound {
					t.Fatalf("duplicate receiver captions during native burn: %q, %d, %v", url, response.Code, err)
				}
				if tc.mode == subtitleAutomatic {
					if err := setCurrentMediaPath(screen, noSubs); err != nil {
						t.Fatal(err)
					}
					if err := extractChromecastSubtitles(screen); err != nil || screen.embeddedSubtitle != nil {
						t.Fatalf("queue retained previous subtitle track: %+v, %v", screen.embeddedSubtitle, err)
					}
				}
				return
			}
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

func TestDesktopChromecastSubtitleRenderingModes(t *testing.T) {
	app := test.NewApp()
	t.Cleanup(app.Quit)
	dir := t.TempDir()
	// This test routes captions without transcoding; only FFmpeg's availability
	// check needs to succeed, independently of the host's installed tools.
	ffmpeg := filepath.Join(dir, "ffmpeg")
	script := "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		ffmpeg += ".cmd"
		script = "@echo off\r\nexit /b 0\r\n"
	}
	if err := os.WriteFile(ffmpeg, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "captions.srt")
	if err := os.WriteFile(path, []byte("1\n00:00:05,000 --> 00:00:10,000\nFirst\n\n2\n00:00:35,000 --> 00:00:40,000\nSecond\n"), 0600); err != nil {
		t.Fatal(err)
	}
	screen := &FyneScreen{subsfile: path, ffmpegPath: ffmpeg}
	server := httphandlers.NewServer("")
	tt := []struct {
		name                         string
		transcoded, preference, rtmp bool
		missingFFmpeg                bool
		offset                       int
	}{
		{name: "receiver default", transcoded: true},
		{name: "receiver seek", transcoded: true, offset: 30},
		{name: "burn selected captions", transcoded: true, preference: true},
		{name: "burn seek keeps original captions", transcoded: true, preference: true, offset: 30},
		{name: "missing FFmpeg uses receiver", transcoded: true, preference: true, missingFFmpeg: true},
		{name: "missing FFmpeg keeps receiver seek", transcoded: true, preference: true, missingFFmpeg: true, offset: 30},
		{name: "RTMP uses receiver", transcoded: true, preference: true, rtmp: true},
		{name: "direct ignores fallback", preference: true, offset: 30},
		{name: "receiver restored", transcoded: true, offset: 30},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			app.Preferences().SetBool(chromecastBurnSubtitlesPref, tc.preference)
			screen.ffmpegPath = ffmpeg
			if tc.missingFFmpeg {
				screen.ffmpegPath = filepath.Join(dir, "missing-ffmpeg")
			}
			screen.captureChromecastSubtitleSettings()
			screen.rtmpServerCheck = widget.NewCheck("", nil)
			screen.rtmpServerCheck.SetChecked(tc.rtmp)
			endpoint, err := registerDesktopChromecastSubtitles(screen, server, "host:1234", tc.offset, tc.transcoded)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			server.ServeMediaHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://host:1234/subtitles.vtt", nil))
			if tc.transcoded {
				opts := desktopChromecastTranscodeOptions(screen, tc.offset)
				if opts.SeekSeconds != tc.offset {
					t.Fatalf("transcode seek=%d want %d", opts.SeekSeconds, tc.offset)
				}
				if tc.preference && !tc.rtmp && !tc.missingFFmpeg {
					if endpoint != "" || response.Code != http.StatusNotFound || opts.SubsPath != path {
						t.Fatalf("burn fallback: URL=%q status=%d subtitles=%q", endpoint, response.Code, opts.SubsPath)
					}
					return
				}
				if opts.SubsPath != "" {
					t.Fatalf("receiver captions also burned: %q", opts.SubsPath)
				}
			}
			text := response.Body.String()
			if endpoint == "" || response.Code != http.StatusOK {
				t.Fatalf("receiver captions: %q %d", endpoint, response.Code)
			}
			if tc.transcoded && tc.offset == 30 {
				if strings.Contains(text, "First") || !strings.Contains(text, "00:00:05.000 --> 00:00:10.000\nSecond") {
					t.Fatalf("shifted captions: %q", text)
				}
			} else if !strings.Contains(text, "First") || !strings.Contains(text, "00:00:35.000 --> 00:00:40.000") {
				t.Fatalf("original captions: %q", text)
			}
		})
	}
	app.Preferences().SetBool(chromecastBurnSubtitlesPref, true)
	screen.captureChromecastSubtitleSettings()
	screen.subsfile = ""
	if opts := desktopChromecastTranscodeOptions(screen, 30); opts.SubsPath != "" {
		t.Fatalf("fallback without captions tried burn-in: %q", opts.SubsPath)
	}
}
