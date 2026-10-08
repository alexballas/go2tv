package webui

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/internal/controller"
	"go2tv.app/go2tv/v2/internal/library"
	"go2tv.app/go2tv/v2/internal/mediaserver"
	"go2tv.app/go2tv/v2/internal/playback"
)

type captionTorrentTransport struct{ torrentTransport }

func (f *captionTorrentTransport) Open(context.Context, playback.Device) (playback.Transport, error) {
	return f, nil
}

func (f *captionTorrentTransport) LoadOnExisting(ctx context.Context, request playback.LoadRequest) error {
	return f.Load(ctx, request)
}

func TestWebTorrentCaptionsLoadSelectionAndCleanup(t *testing.T) {
	tt := []struct {
		name, protocol, filename      string
		transcode, disabled, external bool
		want                          bool
	}{
		{name: "automatic MKV", protocol: "Chromecast", filename: "movie.mkv", want: true},
		{name: "automatic WebM", protocol: "Chromecast", filename: "movie.webm", want: true},
		{name: "transcoded MKV", protocol: "Chromecast", filename: "movie.mkv", transcode: true, want: true},
		{name: "none", protocol: "Chromecast", filename: "movie.mkv", disabled: true},
		{name: "external", protocol: "Chromecast", filename: "movie.mkv", external: true},
		{name: "DLNA", protocol: "DLNA", filename: "movie.mkv"},
		{name: "MP4", protocol: "Chromecast", filename: "movie.mp4"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			lib, err := library.Open(library.Config{Roots: []string{t.TempDir()}})
			if err != nil {
				t.Fatal(err)
			}
			transport := &captionTorrentTransport{}
			server := mediaserver.New(mediaserver.Config{ListenAddr: "127.0.0.1:0"})
			control := controller.New(controller.Config{
				Discovery:        torrentDiscovery{playback.Device{ID: "renderer", Protocol: tc.protocol, Endpoint: "http://127.0.0.1:8009"}},
				TransportFactory: transport, MediaServer: server,
				DurationProbe: func(context.Context, playback.SourceOpener) (float64, error) { return 60, nil },
			})
			h, err := New(Config{Controller: control, Library: lib})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { h.Close(); control.Close(); _ = lib.Close() }()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for {
				snapshot, err := control.Snapshot(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if len(snapshot.Devices) > 0 {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(time.Millisecond):
				}
			}
			if result := control.SelectDevice(ctx, controller.Mutation{}, "renderer"); !result.OK() {
				t.Fatal(result)
			}
			if result := control.SetTranscode(ctx, controller.Mutation{}, tc.transcode); !result.OK() {
				t.Fatal(result)
			}
			if result := control.SetPolicy(ctx, controller.PolicyRequest{Policy: controller.Policy{DisableTorrentSubtitles: tc.disabled}}); !result.OK() {
				t.Fatal(result)
			}
			if tc.external {
				path := filepath.Join(t.TempDir(), "captions.vtt")
				if err := os.WriteFile(path, []byte("WEBVTT\n"), 0600); err != nil {
					t.Fatal(err)
				}
				open := func(context.Context) (io.ReadSeekCloser, time.Time, error) {
					reader, err := os.Open(path)
					return reader, time.Time{}, err
				}
				if result := control.SelectSubtitle(ctx, controller.Mutation{}, controller.SubtitleRef{RootID: "root", ID: "external", Name: "captions.vtt", Open: open}); !result.OK() {
					t.Fatal(result)
				}
			}
			pending := loadWebTorrent(t, h, tc.filename)
			index := 1
			if result := webTorrentCommand(h, "torrent.select", pending.ID, &index); !result.OK() {
				t.Fatal(result)
			}
			if result := control.Play(ctx, controller.PlayRequest{}); !result.OK() {
				t.Fatal(result)
			}
			endpoint := transport.load.TorrentSubtitleURL
			if (endpoint != "") != tc.want {
				t.Fatalf("torrent caption LOAD = %+v", transport.load)
			}
			if tc.external && transport.load.SubtitleURL == "" {
				t.Fatal("external subtitle selection lost")
			}
			if !tc.want {
				return
			}
			mediaURL, _ := url.Parse(transport.load.MediaURL)
			captionURL, _ := url.Parse(endpoint)
			if mediaURL.Host != captionURL.Host {
				t.Fatalf("receiver origins differ: %s %s", mediaURL, captionURL)
			}
			response, err := http.Get(endpoint + "?time=NaN")
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("caption endpoint not registered: %d", response.StatusCode)
			}
			// Disabling and replaying clears receiver configuration and removes the old route.
			if result := control.SetPolicy(ctx, controller.PolicyRequest{Policy: controller.Policy{DisableTorrentSubtitles: true}}); !result.OK() {
				t.Fatal(result)
			}
			if result := control.Play(ctx, controller.PlayRequest{}); !result.OK() {
				t.Fatal(result)
			}
			if transport.load.TorrentSubtitleURL != "" {
				t.Fatal("disabled replay retained captions")
			}
			if !tc.transcode {
				response, err = http.Get(endpoint + "?time=NaN")
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				if response.StatusCode != http.StatusNotFound {
					t.Fatalf("superseded captions retained: %d", response.StatusCode)
				}
			}
			if result := control.SetPolicy(ctx, controller.PolicyRequest{}); !result.OK() {
				t.Fatal(result)
			}
			if result := control.Play(ctx, controller.PlayRequest{}); !result.OK() {
				t.Fatal(result)
			}
			if transport.load.TorrentSubtitleURL == "" {
				t.Fatal("re-enabling automatic captions lost torrent source")
			}
			// Replacing the torrent with local media sends no torrent captions.
			if result := control.SetTranscode(ctx, controller.Mutation{}, false); !result.OK() {
				t.Fatal(result)
			}
			if result := control.QueueAndPlay(ctx, controller.Mutation{}, torrentTestLocalMedia(t)); !result.OK() {
				t.Fatal(result)
			}
			if transport.load.TorrentSubtitleURL != "" {
				t.Fatal("local replacement retained torrent captions")
			}
		})
	}
}
