package httphandlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go2tv.app/go2tv/v2/soapcalls"
)

func TestStartServerServesConvertedDirectDLNACaptions(t *testing.T) {
	vtt := "WEBVTT\n\nfirst\n00:00:05.000 --> 00:00:10.000 align:start\nFirst caption\n\n00:00:35.000 --> 00:00:40.000\nSecond caption\n"
	path := filepath.Join(t.TempDir(), "captions.vtt")
	if err := os.WriteFile(path, []byte(vtt), 0600); err != nil {
		t.Fatal(err)
	}
	tv := &soapcalls.TVPayload{
		MediaURL:     "http://127.0.0.1/media.mp4",
		SubtitlesURL: "http://127.0.0.1/captions.vtt",
		CallbackURL:  "http://127.0.0.1/callback",
	}
	server := NewServer("127.0.0.1:0")
	started := make(chan error)
	go server.StartServer(started, []byte("video"), path, tv, &callbackTestScreen{})
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.StopServer)
	if tv.SubtitlesURL != "http://127.0.0.1/captions.srt" {
		t.Fatalf("advertised subtitle URL = %q", tv.SubtitlesURL)
	}
	response := httptest.NewRecorder()
	server.ServeMediaHandler()(response, httptest.NewRequest(http.MethodGet, tv.SubtitlesURL, nil))
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/srt; charset=utf-8" {
		t.Fatalf("subtitle response: status=%d MIME=%q", response.Code, response.Header().Get("Content-Type"))
	}
	if body := response.Body.String(); !strings.Contains(body, "00:00:05,000 --> 00:00:10,000") || !strings.Contains(body, "First caption") || strings.Contains(body, "WEBVTT") || strings.Contains(body, "align:start") {
		t.Fatalf("receiver captions = %q", body)
	}
}

func TestPrepareDLNAASSAndSSAAsSRT(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	for _, extension := range []string{".ass", ".ssa"} {
		t.Run(extension, func(t *testing.T) {
			var source string
			if extension == ".ass" {
				source = "[Script Info]\nScriptType: v4.00+\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\nStyle: Default,Arial,20,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,2,2,10,10,10,1\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,Hello ASS\n"
			} else {
				source = "[Script Info]\nScriptType: v4.00\n[V4 Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, TertiaryColour, BackColour, Bold, Italic, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, AlphaLevel, Encoding\nStyle: Default,Arial,20,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,1,2,2,2,10,10,10,0,1\n[Events]\nFormat: Marked, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: Marked=0,0:00:01.00,0:00:03.00,Default,,0,0,0,,Hello SSA\n"
			}
			url, prepared, err := PrepareDLNASubtitles(context.Background(), "http://host/subtitles"+extension, strings.NewReader(source), ffmpeg)
			if err != nil {
				t.Fatal(err)
			}
			if url != "http://host/subtitles.srt" {
				t.Fatalf("subtitle URL = %q", url)
			}
			body, ok := prepared.([]byte)
			if !ok || !strings.Contains(string(body), "00:00:01,000 --> 00:00:03,000") || !strings.Contains(string(body), "Hello ") {
				t.Fatalf("receiver captions = %q", prepared)
			}
		})
	}
}
