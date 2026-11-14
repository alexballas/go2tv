package controller

import (
	"context"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/utils"
)

func newAudioTrackController(t *testing.T, protocol string) (*Controller, *fakeServer) {
	t.Helper()
	log := &eventLog{}
	server := &fakeServer{log: log}
	c := New(Config{
		Discovery:        newFakeDiscovery(playback.Device{ID: "renderer", Protocol: protocol}),
		TransportFactory: &fakeFactory{log: log},
		MediaServer:      server,
		OperationTimeout: time.Second,
		AudioTracksProbe: func(context.Context, playback.SourceOpener) ([]utils.AudioTrack, error) {
			return []utils.AudioTrack{
				{Number: 1, Language: "jpn", Codec: "ac3", Channels: 6, Default: true},
				{Number: 2, Language: "eng", Codec: "aac", Channels: 2},
			}, nil
		},
	})
	t.Cleanup(c.Close)
	awaitDevices(t, c, 1)
	if result := c.SelectDevice(t.Context(), Mutation{}, "renderer"); !result.OK() {
		t.Fatal(result)
	}
	return c, server
}

func awaitAudioTracks(t *testing.T, c *Controller) Snapshot {
	t.Helper()
	return awaitSnapshotState(t, c, func(s Snapshot) bool { return len(s.AudioTracks) == 2 })
}

func TestAudioTrackSelectionAppliesToNextLoad(t *testing.T) {
	tt := []struct {
		name          string
		protocol      string
		track         int
		transcode     bool
		wantTrack     int
		wantTranscode bool
	}{
		{"dlna non-default transcodes", "DLNA", 2, false, 2, true},
		{"chromecast non-default transcodes", "Chromecast", 2, false, 2, true},
		{"transcode toggle keeps track", "DLNA", 2, true, 2, true},
		{"source default plays direct", "DLNA", 1, false, 0, false},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			c, server := newAudioTrackController(t, tc.protocol)
			ctx := t.Context()
			c.SelectMedia(ctx, Mutation{}, testMedia("movie.mkv", mediamodel.MediaKindVideo))
			snapshot := awaitAudioTracks(t, c)
			if snapshot.AudioTracks[0].Label != "Japanese · AC3 5.1" || !snapshot.AudioTracks[0].Default {
				t.Fatalf("tracks = %+v", snapshot.AudioTracks)
			}
			c.SetTranscode(ctx, Mutation{}, tc.transcode)
			if result := c.SelectAudioTrack(ctx, Mutation{}, tc.track); !result.OK() {
				t.Fatal(result)
			}
			if result := c.Play(ctx, PlayRequest{}); !result.OK() {
				t.Fatal(result)
			}
			server.mu.Lock()
			request := server.last
			server.mu.Unlock()
			if request.AudioTrack != tc.wantTrack || request.Transcode != tc.wantTranscode {
				t.Fatalf("server request audio=%d transcode=%v, want %d %v", request.AudioTrack, request.Transcode, tc.wantTrack, tc.wantTranscode)
			}
			if after, _ := c.Snapshot(ctx); after.Transcode != tc.wantTranscode {
				t.Fatalf("Transcode shown as %v, want %v", after.Transcode, tc.wantTranscode)
			}
		})
	}
}

func TestAudioTrackResetsWithMedia(t *testing.T) {
	c, server := newAudioTrackController(t, "DLNA")
	ctx := t.Context()
	c.SelectMedia(ctx, Mutation{}, testMedia("first.mkv", mediamodel.MediaKindVideo))
	awaitAudioTracks(t, c)
	if result := c.SelectAudioTrack(ctx, Mutation{}, 2); !result.OK() {
		t.Fatal(result)
	}
	if result := c.SelectAudioTrack(ctx, Mutation{}, 3); result.Code != CodeInvalid {
		t.Fatalf("missing track result = %+v", result)
	}
	c.SelectMedia(ctx, Mutation{}, testMedia("second.mkv", mediamodel.MediaKindVideo))
	if snapshot, _ := c.Snapshot(ctx); snapshot.SelectedAudioTrack != 0 {
		t.Fatalf("track carried to other media: %d", snapshot.SelectedAudioTrack)
	}
	awaitAudioTracks(t, c)
	if result := c.Play(ctx, PlayRequest{}); !result.OK() {
		t.Fatal(result)
	}
	server.mu.Lock()
	request := server.last
	server.mu.Unlock()
	if request.AudioTrack != 0 || request.Transcode {
		t.Fatalf("second media loaded with audio=%d transcode=%v", request.AudioTrack, request.Transcode)
	}
}

func TestAudioTrackKeepsTranscodeSettingInSync(t *testing.T) {
	c, _ := newAudioTrackController(t, "DLNA")
	ctx := t.Context()
	state := func() Snapshot {
		snapshot, err := c.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	c.SelectMedia(ctx, Mutation{}, testMedia("first.mkv", mediamodel.MediaKindVideo))
	awaitAudioTracks(t, c)

	c.SelectAudioTrack(ctx, Mutation{}, 2)
	if s := state(); !s.Transcode {
		t.Fatal("other track did not turn Transcode on")
	}
	c.SelectAudioTrack(ctx, Mutation{}, 1)
	if s := state(); s.Transcode {
		t.Fatal("default track kept the Transcode it turned on")
	}

	c.SelectAudioTrack(ctx, Mutation{}, 2)
	c.SetTranscode(ctx, Mutation{}, false)
	if s := state(); s.SelectedAudioTrack != 0 {
		t.Fatalf("turning Transcode off kept track %d", s.SelectedAudioTrack)
	}

	c.SelectAudioTrack(ctx, Mutation{}, 2)
	c.SelectMedia(ctx, Mutation{}, testMedia("second.mkv", mediamodel.MediaKindVideo))
	if s := state(); s.Transcode {
		t.Fatal("Transcode turned on for a track survived a media change")
	}

	awaitAudioTracks(t, c)
	c.SetTranscode(ctx, Mutation{}, true)
	c.SelectAudioTrack(ctx, Mutation{}, 2)
	c.SelectAudioTrack(ctx, Mutation{}, 1)
	if s := state(); !s.Transcode {
		t.Fatal("default track turned off the user's Transcode")
	}
}
