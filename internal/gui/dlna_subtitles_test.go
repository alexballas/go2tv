//go:build !(android || ios)

package gui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/utils"
)

func createEmbeddedSubtitleTestMedia(t *testing.T, ffmpeg, dir, codec string) string {
	t.Helper()
	args := []string{"-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "color=size=320x180:duration=1"}
	for i, caption := range []string{"First caption", "Second caption"} {
		path := filepath.Join(dir, fmt.Sprintf("%d.srt", i))
		if err := os.WriteFile(path, []byte("1\n00:00:00,000 --> 00:00:01,000\n"+caption+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "-i", path)
	}
	media := filepath.Join(dir, "embedded-"+codec+".mkv")
	args = append(args, "-map", "0:v", "-map", "1:s", "-map", "2:s", "-c:v", "mpeg4", "-c:s", codec,
		"-metadata:s:s:0", "title=First", "-metadata:s:s:1", "title=Second", media)
	if output, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("create subtitle media: %v: %s", err, output)
	}
	return media
}

func TestDLNAEmbeddedSubtitleSelection(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	dir := t.TempDir()
	ass := createEmbeddedSubtitleTestMedia(t, ffmpeg, dir, "ass")
	srt := createEmbeddedSubtitleTestMedia(t, ffmpeg, dir, "srt")
	webvtt := createEmbeddedSubtitleTestMedia(t, ffmpeg, dir, "webvtt")
	bitmap := filepath.Join("testdata", "bitmap-subtitles.mkv")
	mixed := filepath.Join(dir, "bitmap-first.mkv")
	if output, err := exec.Command(ffmpeg, "-nostdin", "-loglevel", "error", "-i", ass, "-i", bitmap,
		"-map", "0:v", "-map", "1:s", "-map", "0:s", "-c", "copy", mixed).CombinedOutput(); err != nil {
		t.Fatalf("create bitmap-first media: %v: %s", err, output)
	}
	sidecar := filepath.Join(dir, "sidecar.srt")
	if err := os.WriteFile(sidecar, []byte("1\n00:00:00,000 --> 00:00:01,000\nSidecar caption\n"), 0600); err != nil {
		t.Fatal(err)
	}
	tt := []struct {
		name, media, mode, selected, want string
		transcode, native, sidecar        bool
		failExtraction, wantError, url    bool
		receiverFirst, burnFirst          bool
		bitmap, plainText, missingMedia   bool
		track                             int
	}{
		{name: "automatic ASS uses native TV captions", media: ass, mode: subtitleAutomatic},
		{name: "automatic ASS burn preserves original", media: ass, mode: subtitleAutomatic, transcode: true, native: true},
		{name: "switch Chromecast captions to styled burn", media: ass, mode: subtitleAutomatic, transcode: true, native: true, receiverFirst: true},
		{name: "switch Chromecast captions to native DLNA", media: ass, mode: subtitleAutomatic, receiverFirst: true},
		{name: "automatic SRT uses native TV captions", media: srt, mode: subtitleAutomatic},
		{name: "automatic SRT burn", media: srt, mode: subtitleAutomatic, transcode: true, native: true, plainText: true},
		{name: "automatic WebVTT burn", media: webvtt, mode: subtitleAutomatic, transcode: true, native: true, plainText: true},
		{name: "disable transcoding after SRT burn", media: srt, mode: subtitleAutomatic, burnFirst: true},
		{name: "disable transcoding after styled ASS burn", media: ass, mode: subtitleAutomatic, burnFirst: true},
		{name: "sidecar takes priority", media: ass, mode: subtitleAutomatic, sidecar: true, want: "Sidecar caption"},
		{name: "burn keeps sidecar priority", media: ass, mode: subtitleAutomatic, transcode: true, sidecar: true, want: "Sidecar caption"},
		{name: "manual second track", media: ass, mode: subtitleEmbedded, selected: "Second", want: "Second caption"},
		{name: "manual ASS burn", media: ass, mode: subtitleEmbedded, selected: "Second", transcode: true, native: true, track: 1},
		{name: "manual SRT burn", media: srt, mode: subtitleEmbedded, selected: "Second", transcode: true, native: true, plainText: true, track: 1},
		{name: "none disables fallback", media: ass, mode: subtitleNone},
		{name: "none disables burn fallback", media: ass, mode: subtitleNone, transcode: true},
		{name: "embedded requires selection", media: ass, mode: subtitleEmbedded},
		{name: "external requires file", media: ass, mode: subtitleExternal},
		{name: "URL excludes local fallback", media: ass, mode: subtitleAutomatic, transcode: true, url: true},
		{name: "automatic preparation failure is optional", media: webvtt, mode: subtitleAutomatic, transcode: true, missingMedia: true},
		{name: "manual failure reported", media: ass, mode: subtitleEmbedded, selected: "Second", failExtraction: true, wantError: true},
		{name: "manual burn preparation failure reported", media: srt, mode: subtitleEmbedded, selected: "Second", transcode: true, missingMedia: true, wantError: true},
		{name: "bitmap allows playback", media: bitmap, mode: subtitleAutomatic},
		{name: "bitmap burn", media: bitmap, mode: subtitleAutomatic, transcode: true, native: true, bitmap: true},
		{name: "manual bitmap requires transcoding", media: bitmap, mode: subtitleEmbedded, selected: "eng", bitmap: true, wantError: true},
		{name: "manual bitmap burn", media: bitmap, mode: subtitleEmbedded, selected: "eng", transcode: true, native: true, bitmap: true},
		{name: "bitmap-first uses native TV captions", media: mixed, mode: subtitleAutomatic},
		{name: "first bitmap burn", media: mixed, mode: subtitleAutomatic, transcode: true, native: true, bitmap: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			screen, card := newMediaCardTestScreen(t)
			screen.ffmpegPath = ffmpeg
			if err := setCurrentMediaPath(screen, tc.media); err != nil {
				t.Fatal(err)
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
			if tc.failExtraction {
				screen.ffmpegPath = filepath.Join(dir, "missing-ffmpeg")
			}
			if tc.missingMedia {
				// A source can disappear after selection; both probing and extraction fail.
				screen.mediafile = filepath.Join(dir, "missing-media.mkv")
			}
			t.Cleanup(func() {
				for _, path := range screen.tempFiles {
					if err := os.Remove(path); err != nil {
						t.Error(err)
					}
				}
			})
			if tc.receiverFirst {
				if err := extractChromecastSubtitles(screen); err != nil || screen.subsfile == "" {
					t.Fatalf("prepare receiver captions: %v", err)
				}
			}
			if tc.burnFirst {
				if err := prepareDesktopSubtitles(screen, screen.mediafile, true, true); err != nil ||
					(screen.subsfile == "" && screen.embeddedSubtitle == nil) {
					t.Fatalf("prepare burned captions: %v", err)
				}
			}
			priorTemps := len(screen.tempFiles)
			err := prepareDesktopSubtitles(screen, screen.mediafile, tc.transcode, tc.transcode)
			if tc.wantError {
				if err == nil || screen.subsfile != "" || screen.embeddedSubtitle != nil {
					t.Fatalf("failed selection: error=%v path=%q original=%+v", err, screen.subsfile, screen.embeddedSubtitle)
				}
				if tc.bitmap && (!errors.Is(err, utils.ErrBitmapSubtitles) || !strings.Contains(err.Error(), "Enable Transcode")) {
					t.Fatalf("bitmap error lacks recovery instructions: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if card.subtitles.Selected != lang.L(tc.mode) || screen.SelectInternalSubs.Selected != tc.selected {
				t.Fatal("playback changed subtitle preference")
			}
			if tc.mode == subtitleAutomatic && !tc.transcode && !tc.sidecar && len(screen.tempFiles) != priorTemps {
				t.Fatal("direct playback extracted automatic subtitles")
			}
			if tc.native {
				original := screen.embeddedSubtitle
				if original == nil || original.Path != screen.mediafile || original.Track != tc.track || original.Bitmap != tc.bitmap || original.PlainText != tc.plainText || screen.subsfile != "" {
					t.Fatalf("embedded subtitle source lost: %+v, sidecar=%q", original, screen.subsfile)
				}
				return
			}
			if tc.want == "" {
				if screen.subsfile != "" || screen.embeddedSubtitle != nil {
					t.Fatalf("unexpected captions: path=%q original=%+v", screen.subsfile, screen.embeddedSubtitle)
				}
				return
			}
			assertDLNACaptionResponse(t, screen.subsfile, tc.want)
		})
	}
}

func assertDLNACaptionResponse(t *testing.T, path, want string) {
	t.Helper()
	server := httphandlers.NewServer("")
	endpoint := "/" + utils.ConvertFilename(path)
	server.AddHandler(endpoint, nil, nil, path)
	response := httptest.NewRecorder()
	server.ServeMediaHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, endpoint, nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), want) ||
		!strings.Contains(response.Body.String(), "00:00:00,000 --> 00:00:01,000") {
		t.Fatalf("DLNA captions: status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestQueueNextAutomaticEmbeddedSubtitles(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	media := createEmbeddedSubtitleTestMedia(t, ffmpeg, t.TempDir(), "ass")
	current := filepath.Join(t.TempDir(), "current.mkv")
	tt := []struct {
		name            string
		transcode, none bool
	}{
		{name: "direct uses native TV captions"},
		{name: "styled burn", transcode: true},
		{name: "none disables fallback", transcode: true, none: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			screen, _ := newQueueArtworkTestScreen(t, []string{current, media})
			screen.ffmpegPath = ffmpeg
			screen.Transcode = tc.transcode
			screen.CustomSubsCheck = widget.NewCheck("", nil)
			screen.CustomSubsCheck.SetChecked(tc.none)
			t.Cleanup(func() {
				for _, path := range screen.tempFiles {
					if err := os.Remove(path); err != nil {
						t.Error(err)
					}
				}
			})
			next, err := queueNext(screen, false)
			if err != nil {
				t.Fatal(err)
			}
			if err := next.Context().Err(); err != nil {
				t.Fatalf("queued playback context ended with preparation: %v", err)
			}
			switch {
			case tc.none || !tc.transcode:
				if next.FFmpegSubsPath != "" || next.FFmpegEmbeddedSubtitle != nil || !strings.HasSuffix(next.SubtitlesURL, "/.") {
					t.Fatalf("unexpected Go2TV captions: %+v", next)
				}
				if len(screen.tempFiles) != 0 {
					t.Fatal("queue extracted captions for native playback")
				}
			case tc.transcode:
				original := next.FFmpegEmbeddedSubtitle
				if original == nil || original.Path != media || original.Track != 0 || next.FFmpegSubsPath != "" {
					t.Fatalf("queued styled subtitles lost: %+v", next)
				}
			}
			if screen.mediafile != current || screen.subsfile != "" || screen.embeddedSubtitle != nil {
				t.Fatal("preparing next captions changed current media")
			}
		})
	}
}

func TestAutomaticEmbeddedSubtitlesRespectCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path, original, err := prepareEmbeddedSubtitles(ctx, "ffmpeg", "media.mkv", []int{0, 1}, true, true)
	if !errors.Is(err, context.Canceled) || path != "" || original != nil {
		t.Fatalf("cancelled preparation: path=%q original=%+v error=%v", path, original, err)
	}
	if _, err := utils.GetSubsContext(ctx, "ffmpeg", "media.mkv"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled discovery: %v", err)
	}
}
