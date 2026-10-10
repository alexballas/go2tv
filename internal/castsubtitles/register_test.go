package castsubtitles

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/mediasource"
	"go2tv.app/go2tv/v2/internal/mkvsubs"
)

type captionSource struct {
	path string
	size int64
}

func TestTorrentCaptionRegistrationWithMediaOrigin(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	subs := filepath.Join(t.TempDir(), "captions.srt")
	if err := os.WriteFile(subs, []byte("1\n00:00:05,000 --> 00:00:10,000\nFirst caption\n\n2\n00:00:35,000 --> 00:00:40,000\nSecond caption\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fixtures := []struct {
		name, audioCodec, audioOffset string
		origin                        float64
	}{
		{"aligned PCM", "pcm_s16le", "0", 5},
		{"audio before video", "pcm_s16le", "-1", 4},
		{"AAC encoder delay", "aac", "0", 4.977},
		{"Opus encoder delay", "libopus", "0", 4.993},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "offset.mkv")
			command := exec.Command(ffmpeg, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=size=32x32:rate=2:duration=45",
				"-itsoffset", fixture.audioOffset, "-f", "lavfi", "-i", "sine=frequency=440:duration=46", "-i", subs,
				"-map", "0:v", "-map", "1:a", "-map", "2:s", "-c:v", "libx264", "-c:a", fixture.audioCodec, "-c:s", "srt", "-output_ts_offset", "5", path)
			if output, err := command.CombinedOutput(); err != nil {
				if strings.Contains(string(output), "Unknown encoder") {
					t.Skipf("fixture encoder unavailable: %s", output)
				}
				t.Fatalf("create offset torrent: %v: %s", err, output)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(mediasource.Register(path, captionSource{path, info.Size()}))
			tt := []struct {
				name       string
				transcoded bool
				seek       int
				want       mkvsubs.Cue
			}{
				{name: "native", want: mkvsubs.Cue{10, 15, "First caption"}},
				{name: "initial transcode", transcoded: true, want: mkvsubs.Cue{10 - fixture.origin, 15 - fixture.origin, "First caption"}},
				{name: "transcoded seek", transcoded: true, seek: 30, want: mkvsubs.Cue{10 - fixture.origin, 15 - fixture.origin, "Second caption"}},
			}
			for _, tc := range tt {
				t.Run(tc.name, func(t *testing.T) {
					server := httphandlers.NewServer("")
					captions, err := Register(server, "host:1234", path, "", Options{Transcoded: tc.transcoded, AutomaticTorrent: true, SeekSeconds: tc.seek})
					if err != nil {
						t.Fatal(err)
					}
					response := httptest.NewRecorder()
					server.ServeMediaHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, captions.TorrentSubtitleURL+"?time=0", nil))
					var result struct{ Cues []mkvsubs.Cue }
					if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					if response.Code != http.StatusOK || len(result.Cues) != 1 || result.Cues[0].Text != tc.want.Text ||
						math.Abs(result.Cues[0].Start-tc.want.Start) > 1e-6 || math.Abs(result.Cues[0].End-tc.want.End) > 1e-6 {
						t.Fatalf("receiver captions = %+v, status=%d, want %+v", result.Cues, response.Code, tc.want)
					}
				})
			}
		})
	}
}

func (s captionSource) Open(context.Context) (io.ReadSeekCloser, error) { return os.Open(s.path) }
func (captionSource) URL() string                                       { return "" }
func (captionSource) MIME() string                                      { return "video/x-matroska" }
func (s captionSource) Size() int64                                     { return s.size }

func TestCLICaptionSelectionAndTranscode(t *testing.T) {
	fixture := "../mkvsubs/testdata/torrent-text.mkv"
	info, err := os.Stat(fixture)
	if err != nil {
		t.Fatal(err)
	}
	mediaPath := filepath.Join(t.TempDir(), "movie.mkv")
	t.Cleanup(mediasource.Register(mediaPath, captionSource{fixture, info.Size()}))
	subtitlePath := filepath.Join(t.TempDir(), "captions.srt")
	if err := os.WriteFile(subtitlePath, []byte("1\n00:00:05,000 --> 00:00:10,000\nExternal first\n\n2\n00:00:35,000 --> 00:00:40,000\nExternal second\n"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httphandlers.NewServer("")
	tt := []struct {
		name                                 string
		external, transcode, burn, automatic bool
		offset                               int
		wantExternal, wantTorrent, wantBurn  bool
		wantBurnSource                       bool
	}{
		{name: "torrent direct", automatic: true, wantTorrent: true},
		{name: "torrent transcoded seek", transcode: true, automatic: true, offset: 30, wantTorrent: true},
		{name: "torrent burn fallback", transcode: true, burn: true, automatic: true, wantBurnSource: true},
		{name: "torrent burn disabled", transcode: true, burn: true},
		{name: "external direct overrides torrent", external: true, automatic: true, wantExternal: true},
		{name: "external transcode receiver", external: true, transcode: true, automatic: true, wantExternal: true},
		{name: "external transcode seek", external: true, transcode: true, automatic: true, offset: 30, wantExternal: true},
		{name: "external burn fallback", external: true, transcode: true, burn: true, automatic: true, wantBurn: true},
		{name: "direct ignores burn flag", external: true, burn: true, automatic: true, wantExternal: true},
		{name: "torrent disabled"},
		{name: "torrent restored", automatic: true, wantTorrent: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			path := ""
			if tc.external {
				path = subtitlePath
			}
			captions, err := Register(server, "host:1234", mediaPath, path, Options{Transcoded: tc.transcode, BurnSubtitles: tc.burn, AutomaticTorrent: tc.automatic, SeekSeconds: tc.offset})
			if err != nil {
				t.Fatal(err)
			}
			if (captions.SubtitleURL != "") != tc.wantExternal || (captions.TorrentSubtitleURL != "") != tc.wantTorrent || (captions.BurnPath != "") != tc.wantBurn || (captions.BurnSource != nil) != tc.wantBurnSource {
				t.Fatalf("caption selection: %+v", captions)
			}
			for _, endpoint := range []string{"/subtitles.vtt", "/torrent-subtitles.json?time=0"} {
				response := httptest.NewRecorder()
				server.ServeMediaHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://host:1234"+endpoint, nil))
				if strings.HasPrefix(endpoint, "/subtitles") {
					if !tc.wantExternal {
						if response.Code != http.StatusNotFound {
							t.Fatalf("stale external captions: %d", response.Code)
						}
						continue
					}
					text := response.Body.String()
					if response.Code != http.StatusOK || !strings.HasPrefix(text, "WEBVTT") {
						t.Fatalf("external captions: %d %q", response.Code, text)
					}
					if tc.offset == 30 && (strings.Contains(text, "External first") || !strings.Contains(text, "00:00:05.000 --> 00:00:10.000\nExternal second")) {
						t.Fatalf("seek captions: %q", text)
					}
				} else {
					if !tc.wantTorrent {
						if response.Code != http.StatusNotFound {
							t.Fatalf("stale torrent captions: %d", response.Code)
						}
						continue
					}
					var result struct{ Cues []mkvsubs.Cue }
					if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					text := "First caption"
					if tc.offset == 30 {
						text = "Second caption"
					}
					if response.Code != http.StatusOK || len(result.Cues) != 1 || result.Cues[0] != (mkvsubs.Cue{Start: 5, End: 10, Text: text}) {
						t.Fatalf("torrent captions: %d %+v", response.Code, result.Cues)
					}
				}
			}
		})
	}
	for _, path := range []string{filepath.Join(t.TempDir(), "local.mkv"), filepath.Join(t.TempDir(), "torrent.mp4")} {
		if strings.HasSuffix(path, ".mp4") {
			t.Cleanup(mediasource.Register(path, captionSource{fixture, info.Size()}))
		}
		captions, err := Register(server, "host:1234", path, "", Options{AutomaticTorrent: true})
		if err != nil || captions.TorrentSubtitleURL != "" {
			t.Fatalf("non-Matroska torrent/local: %+v %v", captions, err)
		}
	}
}
