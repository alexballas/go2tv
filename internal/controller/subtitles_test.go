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

func TestSubtitlesFollowSelectedMedia(t *testing.T) {
	for _, protocol := range []string{"DLNA", "Chromecast"} {
		for _, action := range []string{"select media", "play media", "queue and play", "select queue", "play queue", "autoplay", "same media"} {
			t.Run(protocol+"/"+action, func(t *testing.T) {
				c, _, _ := newTestController(playback.Device{ID: "renderer", Protocol: protocol})
				t.Cleanup(c.Close)
				awaitDevices(t, c, 1)
				ctx := t.Context()
				first := testMedia("first.mp4", mediamodel.MediaKindVideo)
				second := testMedia("second.mp4", mediamodel.MediaKindVideo)
				for _, result := range []Result{
					c.SelectDevice(ctx, Mutation{}, "renderer"),
					c.QueueAndPlay(ctx, Mutation{}, first),
					c.SelectSubtitle(ctx, Mutation{}, SubtitleRef{RootID: "root", ID: "first-subs", Name: "first.srt", Open: first.OpenDirect}),
				} {
					if !result.OK() {
						t.Fatal(result)
					}
				}
				// Restart the same media with its chosen captions before changing it.
				if result := c.Play(ctx, PlayRequest{}); !result.OK() {
					t.Fatal(result)
				}
				added := c.AddQueueItem(ctx, QueueAddRequest{Media: second})
				if !added.OK() {
					t.Fatal(added)
				}
				var result Result
				switch action {
				case "select media":
					result = c.SelectMedia(ctx, Mutation{}, second)
				case "play media":
					result = c.PlayMedia(ctx, Mutation{}, second)
				case "queue and play":
					result = c.QueueAndPlay(ctx, Mutation{}, second)
				case "select queue":
					result = c.SelectQueueItem(ctx, Mutation{}, added.ItemID)
				case "play queue":
					result = c.Play(ctx, PlayRequest{QueueItemID: added.ItemID})
				case "autoplay":
					result = c.SetPolicy(ctx, PolicyRequest{Policy: Policy{AutoPlayNext: true}})
					before, _ := c.Snapshot(ctx)
					c.HandleMonitorEvent(ctx, playback.MonitorEvent{Generation: before.Generation, Terminal: playback.TerminalFinished})
					awaitAutoplaySnapshot(t, c, func(s Snapshot) bool { return s.HasSession && s.Generation > before.Generation })
				case "same media":
					result = c.SelectMedia(ctx, Mutation{}, first)
				}
				if !result.OK() {
					t.Fatal(result)
				}
				if result := c.Play(ctx, PlayRequest{}); !result.OK() {
					t.Fatal(result)
				}
				snapshot, err := c.Snapshot(ctx)
				if err != nil {
					t.Fatal(err)
				}
				server := c.cfg.MediaServer.(*fakeServer)
				server.mu.Lock()
				request := server.last
				server.mu.Unlock()
				keep := action == "same media"
				if (request.Subtitle != nil) != keep || (snapshot.SelectedSubtitle != "") != keep {
					t.Fatalf("captions attached = %v, selected = %q; keep = %v", request.Subtitle != nil, snapshot.SelectedSubtitle, keep)
				}
			})
		}
	}
}

func TestGaplessDoesNotReusePreviousSubtitles(t *testing.T) {
	c, _, factory := newTestController(playback.Device{ID: "renderer", Protocol: "DLNA"})
	t.Cleanup(c.Close)
	awaitDevices(t, c, 1)
	ctx := t.Context()
	first := testMedia("first.mp4", mediamodel.MediaKindVideo)
	addTestQueue(t, c, first, testMedia("second.mp4", mediamodel.MediaKindVideo))
	before, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []Result{
		c.SelectDevice(ctx, Mutation{}, "renderer"),
		c.SelectQueueItem(ctx, Mutation{}, before.Queue[0].ID),
		c.SelectSubtitle(ctx, Mutation{}, SubtitleRef{RootID: "root", ID: "first-subs", Name: "first.srt", Open: first.OpenDirect}),
		c.SetPolicy(ctx, PolicyRequest{Policy: Policy{AutoPlayNext: true, GaplessEnabled: true}}),
		c.Play(ctx, PlayRequest{}),
	} {
		if !result.OK() {
			t.Fatal(result)
		}
	}
	server := c.cfg.MediaServer.(*fakeServer)
	server.mu.Lock()
	current, queued := server.last, server.queued
	server.mu.Unlock()
	if current.Subtitle == nil || queued.Media == nil || queued.Subtitle != nil {
		t.Fatalf("current captions = %v, queued media = %v, queued captions = %v", current.Subtitle != nil, queued.Media != nil, queued.Subtitle != nil)
	}
	playing, _ := c.Snapshot(ctx)
	c.HandleMonitorEvent(ctx, playback.MonitorEvent{Generation: playing.Generation, GaplessPromoted: true, GaplessURI: factory.opened[0].next.MediaURL})
	after := awaitAutoplaySnapshot(t, c, func(s Snapshot) bool { return s.ActiveMediaName == "second.mp4" })
	if after.SelectedSubtitle != "" {
		t.Fatalf("promoted episode retained %q", after.SelectedSubtitle)
	}
}
