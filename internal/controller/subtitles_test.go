package controller

import (
	"context"
	"testing"

	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/internal/mediasource"
	"go2tv.app/go2tv/v2/internal/playback"
)

func TestChromecastBurnFallbackPolicyOnLoad(t *testing.T) {
	tt := []struct {
		name, protocol            string
		transcode, fallback, burn bool
	}{
		{"receiver by default", "Chromecast", true, false, false},
		{"compatibility fallback", "Chromecast", true, true, true},
		{"direct remains receiver", "Chromecast", false, true, false},
		{"DLNA policy belongs to runtime", "DLNA", true, true, false},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := newTestController(playback.Device{ID: "renderer", Protocol: tc.protocol})
			t.Cleanup(c.Close)
			awaitDevices(t, c, 1)
			ctx := context.Background()
			if result := c.SelectDevice(ctx, Mutation{}, "renderer"); !result.OK() {
				t.Fatal(result)
			}
			if result := c.SetTranscode(ctx, Mutation{}, tc.transcode); !result.OK() {
				t.Fatal(result)
			}
			if result := c.SetPolicy(ctx, PolicyRequest{Policy: Policy{BurnChromecastSubtitles: tc.fallback}}); !result.OK() {
				t.Fatal(result)
			}
			media := testMedia("movie.mkv", mediamodel.MediaKindVideo)
			if result := c.SelectSubtitle(ctx, Mutation{}, SubtitleRef{RootID: "root", ID: "captions", Name: "captions.srt", Open: media.OpenDirect}); !result.OK() {
				t.Fatal(result)
			}
			if result := c.SelectMedia(ctx, Mutation{}, media); !result.OK() {
				t.Fatal(result)
			}
			if result := c.Play(ctx, PlayRequest{}); !result.OK() {
				t.Fatal(result)
			}
			server := c.cfg.MediaServer.(*fakeServer)
			server.mu.Lock()
			request := server.last
			server.mu.Unlock()
			if request.Subtitle == nil || request.BurnSubtitle != tc.burn {
				t.Fatalf("load lost subtitle policy: %+v", request)
			}
		})
	}
}

func TestAutomaticTorrentSubtitlePolicyOnLoad(t *testing.T) {
	tt := []struct {
		name, protocol                          string
		transcode, fallback, disabled, external bool
		wantSource, wantBurn                    bool
	}{
		{name: "Chromecast receiver", protocol: "Chromecast", transcode: true, wantSource: true},
		{name: "Chromecast burn", protocol: "Chromecast", transcode: true, fallback: true, wantSource: true, wantBurn: true},
		{name: "Chromecast direct ignores burn", protocol: "Chromecast", fallback: true, wantSource: true},
		{name: "DLNA runtime receives burn source", protocol: "DLNA", transcode: true, wantSource: true},
		{name: "automatic disabled", protocol: "Chromecast", transcode: true, fallback: true, disabled: true},
		{name: "external takes priority", protocol: "Chromecast", transcode: true, fallback: true, external: true, wantBurn: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := newTestController(playback.Device{ID: "renderer", Protocol: tc.protocol})
			t.Cleanup(c.Close)
			awaitDevices(t, c, 1)
			ctx := context.Background()
			for _, result := range []Result{
				c.SelectDevice(ctx, Mutation{}, "renderer"),
				c.SetTranscode(ctx, Mutation{}, tc.transcode),
				c.SetPolicy(ctx, PolicyRequest{Policy: Policy{BurnChromecastSubtitles: tc.fallback, DisableTorrentSubtitles: tc.disabled}}),
			} {
				if !result.OK() {
					t.Fatal(result)
				}
			}
			media := testMedia("movie.mkv", mediamodel.MediaKindVideo)
			media.TorrentSource = &struct{ mediasource.Source }{}
			if tc.external {
				if result := c.SelectSubtitle(ctx, Mutation{}, SubtitleRef{RootID: "root", ID: "subs", Name: "captions.srt", Open: media.OpenDirect}); !result.OK() {
					t.Fatal(result)
				}
			}
			if result := c.SelectMedia(ctx, Mutation{}, media); !result.OK() {
				t.Fatal(result)
			}
			if result := c.Play(ctx, PlayRequest{}); !result.OK() {
				t.Fatal(result)
			}
			server := c.cfg.MediaServer.(*fakeServer)
			server.mu.Lock()
			request := server.last
			server.mu.Unlock()
			if (request.TorrentSource != nil) != tc.wantSource || request.BurnSubtitle != tc.wantBurn || (request.Subtitle != nil) != tc.external {
				t.Fatalf("torrent subtitle load policy: %+v", request)
			}
		})
	}
}
