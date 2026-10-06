//go:build !(android || ios)

package servermode

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/internal/mediaserver"
	"go2tv.app/go2tv/v2/internal/mediasource"
	"go2tv.app/go2tv/v2/internal/playback"
)

func subtitleTestOpener(text string) playback.SourceOpener {
	return func(context.Context) (io.ReadSeekCloser, time.Time, error) {
		return &memoryFile{Reader: *bytes.NewReader([]byte(text))}, time.Time{}, nil
	}
}

func TestTorrentSubtitleRenderingPolicy(t *testing.T) {
	tt := []struct {
		name, protocol                     string
		transcode, fallback, burn, overlay bool
	}{
		{"DLNA transcode burns", "DLNA", true, false, true, false},
		{"DLNA direct", "DLNA", false, false, false, false},
		{"Chromecast receiver", "Chromecast", true, false, false, true},
		{"Chromecast burn", "Chromecast", true, true, true, false},
		{"Chromecast direct ignores burn", "Chromecast", false, true, false, true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			server := &runtimeMediaServer{mediaserver.New(mediaserver.Config{ListenAddr: "127.0.0.1:0"})}
			ctx := context.Background()
			t.Cleanup(func() {
				if err := server.Stop(ctx); err != nil {
					t.Error(err)
				}
			})
			request := playback.ServerRequest{Media: subtitleTestOpener("video"), MediaExt: ".mkv", MediaType: "video/x-matroska", Target: playback.Device{Protocol: tc.protocol}, Transcode: tc.transcode, BurnSubtitle: tc.fallback, TorrentSource: &struct{ mediasource.Source }{}}
			if prepared := server.prepareRequest(request); prepared.BurnSubtitle != tc.burn {
				t.Fatalf("burn=%v want %v", prepared.BurnSubtitle, tc.burn)
			}
			route, err := server.Start(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			if (route.TorrentSubtitleURL != "") != tc.overlay || route.SubtitleURL != "" {
				t.Fatalf("unexpected duplicate/missing receiver captions: %+v", route)
			}
		})
	}
}

type subtitleSeekCast struct{ load playback.LoadRequest }

func (c *subtitleSeekCast) LoadOnExisting(_ context.Context, load playback.LoadRequest) error {
	c.load = load
	return nil
}
func (*subtitleSeekCast) Seek(context.Context, int) error { return nil }
func (*subtitleSeekCast) Status(context.Context) (playback.CastStatus, error) {
	return playback.CastStatus{}, nil
}

func TestReceiverSubtitlesSurviveRepeatedTranscodedSeeks(t *testing.T) {
	tt := []struct{ name, extension, captions string }{
		{"SRT", ".srt", "1\n00:00:05,000 --> 00:00:10,000\nFirst\n\n2\n00:00:35,000 --> 00:00:40,000\nSecond\n"},
		{"VTT", ".vtt", "WEBVTT\n\n00:00:05.000 --> 00:00:10.000\nFirst\n\n00:00:35.000 --> 00:00:40.000 align:start\nSecond\n"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			server := &runtimeMediaServer{mediaserver.New(mediaserver.Config{ListenAddr: "127.0.0.1:0"})}
			ctx := context.Background()
			t.Cleanup(func() {
				if err := server.Stop(ctx); err != nil {
					t.Error(err)
				}
			})
			request := playback.ServerRequest{Media: subtitleTestOpener("video"), MediaExt: ".mp4", MediaType: "video/mp4", Target: playback.Device{Protocol: "Chromecast"}, Subtitle: subtitleTestOpener(tc.captions), SubtitleExt: tc.extension, Transcode: true}
			route, err := server.Start(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			cast := &subtitleSeekCast{}
			engine := playback.NewSeekEngine(nil, cast, server)
			load := playback.LoadRequest{MediaURL: route.URL, SubtitleURL: route.SubtitleURL}
			for _, offset := range []int{30, 0, 30} {
				if _, err := engine.Seek(ctx, playback.SeekRequest{Protocol: "Chromecast", Transcoded: true, Seconds: offset, Duration: 60, Server: request, Load: load}); err != nil {
					t.Fatal(err)
				}
				if cast.load.Start != 0 || cast.load.SubtitleURL == "" || cast.load.SubtitleURL == load.SubtitleURL {
					t.Fatalf("seek LOAD: %+v", cast.load)
				}
				response, err := http.Get(cast.load.SubtitleURL)
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				text := string(data)
				if response.StatusCode != http.StatusOK || !strings.HasPrefix(text, "WEBVTT") || !strings.Contains(text, "Second") {
					t.Fatalf("captions: %d %q", response.StatusCode, text)
				}
				if offset == 30 {
					if strings.Contains(text, "First") || !strings.Contains(text, "00:00:05.000 --> 00:00:10.000") {
						t.Fatalf("shifted captions: %q", text)
					}
				} else if !strings.Contains(text, "First") || !strings.Contains(text, "00:00:35.000 --> 00:00:40.000") {
					t.Fatalf("original captions: %q", text)
				}
				if tc.extension == ".vtt" && !strings.Contains(text, "align:start") {
					t.Fatalf("lost VTT cue settings: %q", text)
				}
			}
		})
	}
}

func TestSubtitleRenderingPolicy(t *testing.T) {
	tt := []struct {
		name, protocol            string
		transcode, fallback, burn bool
	}{
		{"Chromecast receiver", "Chromecast", true, false, false},
		{"Chromecast compatibility", "Chromecast", true, true, true},
		{"Chromecast direct ignores fallback", "Chromecast", false, true, false},
		{"DLNA transcode", "DLNA", true, false, true},
		{"DLNA direct", "DLNA", false, false, false},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			server := &runtimeMediaServer{mediaserver.New(mediaserver.Config{ListenAddr: "127.0.0.1:0"})}
			ctx := context.Background()
			t.Cleanup(func() {
				if err := server.Stop(ctx); err != nil {
					t.Error(err)
				}
			})
			request := playback.ServerRequest{Media: subtitleTestOpener("video"), MediaExt: ".mp4", MediaType: "video/mp4", Subtitle: subtitleTestOpener("1\n00:00:01,000 --> 00:00:02,000\nCaption\n"), SubtitleExt: ".srt", Target: playback.Device{Protocol: tc.protocol}, Transcode: tc.transcode, BurnSubtitle: tc.fallback}
			prepared := server.prepareRequest(request)
			if prepared.BurnSubtitle != tc.burn {
				t.Fatalf("burn=%v want %v", prepared.BurnSubtitle, tc.burn)
			}
			route, err := server.Start(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			if (route.SubtitleURL == "") != tc.burn {
				t.Fatalf("subtitle route: %+v", route)
			}
			if !tc.burn {
				response, err := http.Get(route.SubtitleURL)
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				want := "00:00:01,000"
				if tc.protocol == "Chromecast" {
					want = "00:00:01.000"
				}
				if !strings.Contains(string(data), want) {
					t.Fatalf("captions: %q", data)
				}
			}
		})
	}
}
