package mediaserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"

	"go2tv.app/go2tv/v2/internal/mkvsubs"
	"go2tv.app/go2tv/v2/internal/playback"
)

type captionSource struct{ data []byte }

func (s captionSource) Open(ctx context.Context) (io.ReadSeekCloser, error) {
	r, _, err := byteOpener(s.data)(ctx)
	return r, err
}
func (s captionSource) URL() string  { return "" }
func (s captionSource) MIME() string { return "video/x-matroska" }
func (s captionSource) Size() int64  { return int64(len(s.data)) }

func readTorrentCaptions(t *testing.T, endpoint string) []mkvsubs.Cue {
	t.Helper()
	response, err := http.Get(endpoint + "?time=0")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result struct {
		Available bool
		Cues      []mkvsubs.Cue
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !result.Available || response.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("caption response: status=%d available=%v headers=%v", response.StatusCode, result.Available, response.Header)
	}
	return result.Cues
}

type captionCast struct{ load playback.LoadRequest }

func (c *captionCast) LoadOnExisting(_ context.Context, request playback.LoadRequest) error {
	c.load = request
	return nil
}
func (*captionCast) Seek(context.Context, int) error { return nil }
func (*captionCast) Status(context.Context) (playback.CastStatus, error) {
	return playback.CastStatus{}, nil
}

func TestTorrentCaptionsRoutesAndTranscodedSeek(t *testing.T) {
	data, err := os.ReadFile("../mkvsubs/testdata/torrent-text.mkv")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	server := New(Config{ListenAddr: "127.0.0.1:0"})
	request := mediaRequest(data, ".mkv", "video/x-matroska")
	request.Target.Protocol = "Chromecast"
	request.TorrentSource = captionSource{data}
	route := startTestServer(t, server, request)
	assertCaptions := func(endpoint, text string) {
		t.Helper()
		cues := readTorrentCaptions(t, endpoint)
		if len(cues) != 1 || cues[0] != (mkvsubs.Cue{Start: 5, End: 10, Text: text}) {
			t.Fatalf("captions = %+v", cues)
		}
	}
	mediaURL, _ := url.Parse(route.URL)
	subtitleURL, _ := url.Parse(route.TorrentSubtitleURL)
	if route.TorrentSubtitleID == "" || mediaURL.Host != subtitleURL.Host {
		t.Fatalf("receiver origins/routes: %+v", route)
	}
	assertCaptions(route.TorrentSubtitleURL, "First caption")
	preflight, err := http.NewRequest(http.MethodOptions, route.TorrentSubtitleURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(preflight)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent || response.Header.Get("Access-Control-Allow-Private-Network") != "true" {
		t.Fatalf("preflight: %d %v", response.StatusCode, response.Header)
	}
	added, err := server.AddMedia(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	assertCaptions(added.TorrentSubtitleURL, "First caption")
	if err := server.Remove(ctx, added.TorrentSubtitleID); err != nil {
		t.Fatal(err)
	}
	response, err = http.Get(added.TorrentSubtitleURL + "?time=0")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("removed captions status=%d", response.StatusCode)
	}
	assertCaptions(route.TorrentSubtitleURL, "First caption")
	request.Transcode, request.MediaExt, request.MediaType = true, ".mp4", "video/mp4"
	cast := &captionCast{}
	engine := playback.NewSeekEngine(nil, cast, server)
	load := playback.LoadRequest{MediaURL: route.URL, TorrentSubtitleURL: route.TorrentSubtitleURL}
	for _, seek := range []struct {
		seconds int
		text    string
	}{{30, "Second caption"}, {0, "First caption"}, {30, "Second caption"}} {
		if _, err := engine.Seek(ctx, playback.SeekRequest{Protocol: "Chromecast", Transcoded: true, Seconds: seek.seconds, Duration: 60, Server: request, Load: load}); err != nil {
			t.Fatal(err)
		}
		if cast.load.TorrentSubtitleURL == "" || cast.load.TorrentSubtitleURL == load.TorrentSubtitleURL || cast.load.Start != 0 {
			t.Fatalf("seek LOAD: %+v", cast.load)
		}
		assertCaptions(cast.load.TorrentSubtitleURL, seek.text)
	}
}

func TestTorrentCaptionsRespectProtocolAndExternalSubtitles(t *testing.T) {
	tt := []struct {
		name, protocol string
		external, burn bool
	}{
		{name: "DLNA", protocol: "DLNA"},
		{name: "external Chromecast", protocol: "Chromecast", external: true},
		{name: "burned Chromecast has no duplicate overlay", protocol: "Chromecast", burn: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			server := New(Config{ListenAddr: "127.0.0.1:0"})
			request := mediaRequest([]byte("media"), ".mkv", "video/x-matroska")
			request.Target.Protocol, request.TorrentSource = tc.protocol, captionSource{}
			request.Transcode, request.BurnSubtitle = tc.burn, tc.burn
			if tc.external {
				request.Subtitle, request.SubtitleExt = byteOpener([]byte("WEBVTT\n")), ".vtt"
			}
			route := startTestServer(t, server, request)
			if route.TorrentSubtitleURL != "" || route.TorrentSubtitleID != "" {
				t.Fatalf("automatic captions override selection: %+v", route)
			}
		})
	}
}
