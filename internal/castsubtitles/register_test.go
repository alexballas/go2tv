package castsubtitles

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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
	}{
		{name: "torrent direct", automatic: true, wantTorrent: true},
		{name: "torrent transcoded seek", transcode: true, automatic: true, offset: 30, wantTorrent: true},
		{name: "torrent ignores external burn flag", transcode: true, burn: true, automatic: true, wantTorrent: true},
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
			captions, err := Register(server, "host:1234", mediaPath, path, Options{Transcoded: tc.transcode, BurnExternal: tc.burn, AutomaticTorrent: tc.automatic, SeekSeconds: tc.offset})
			if err != nil {
				t.Fatal(err)
			}
			if (captions.SubtitleURL != "") != tc.wantExternal || (captions.TorrentSubtitleURL != "") != tc.wantTorrent || (captions.BurnPath != "") != tc.wantBurn {
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
