package controller

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/internal/playback"
)

func liveMedia(streamURL string) MediaRef {
	return MediaRef{RootID: "radio", ID: "station-1", Name: "Radio 1.FM", Kind: mediamodel.MediaKindAudio, MIMEType: "audio/mpeg", Live: &LiveStream{URL: streamURL}}
}

func TestLiveStreamRelaysToDLNAWithoutQueueSeekOrSubtitles(t *testing.T) {
	log := &eventLog{}
	factory := &fakeFactory{log: log}
	server := &fakeServer{log: log}
	c := New(Config{Discovery: newFakeDiscovery(playback.Device{ID: "tv", Protocol: "DLNA"}), TransportFactory: factory, MediaServer: server, OperationTimeout: time.Second})
	defer c.Close()
	awaitDevices(t, c, 1)
	ctx := context.Background()
	opener := testMedia("captions.srt", mediamodel.MediaKindVideo).OpenDirect
	for _, result := range []Result{
		c.SelectDevice(ctx, Mutation{}, "tv"),
		c.SetTranscode(ctx, Mutation{}, true),
		c.SelectSubtitle(ctx, Mutation{}, SubtitleRef{RootID: "root", ID: "captions", Name: "captions.srt", Open: opener}),
	} {
		if !result.OK() {
			t.Fatal(result)
		}
	}
	if result := c.PlayMedia(ctx, Mutation{}, liveMedia("file:///etc/passwd")); result.OK() {
		t.Fatal("live stream accepted a non-network URL")
	}
	if result := c.PlayMedia(ctx, Mutation{}, liveMedia("http://station.test/live.mp3?listen=1")); !result.OK() {
		t.Fatal(result)
	}

	server.mu.Lock()
	request := server.last
	server.mu.Unlock()
	if request.Live == nil || request.Live.URL != "http://station.test/live.mp3?listen=1" || request.Media != nil || request.Subtitle != nil {
		t.Fatalf("live server request = %+v", request)
	}
	if !request.Transcode || request.MediaType != "audio/aac" || request.MediaExt != ".aac" {
		t.Fatalf("transcoded station not re-encoded to AAC: %+v", request)
	}
	load := factory.opened[0].load
	if load.MediaURL != "http://127.0.0.1/media" || !load.Live || load.Seekable || !load.Transcode || load.MediaType != "audio/aac" || load.SubtitleURL != "" {
		t.Fatalf("DLNA live load = %+v", load)
	}
	if load.Metadata.Title != "Radio 1.FM" || !load.Metadata.AudioOnly {
		t.Fatalf("live metadata = %+v", load.Metadata)
	}
	snapshot, _ := c.Snapshot(ctx)
	if len(snapshot.Queue) != 0 || !snapshot.SelectedLive || !snapshot.ActiveLive || snapshot.ActiveMediaID != (MediaID{RootID: "radio", ID: "station-1"}) {
		t.Fatalf("live snapshot = %+v", snapshot)
	}

	c.HandleMonitorEvent(ctx, playback.MonitorEvent{Generation: snapshot.Generation, State: PlaybackStatePlaying, Position: 42, Duration: 3600})
	request.Live.OnTitle("Artist - Song")
	snapshot = awaitSnapshotState(t, c, func(s Snapshot) bool { return s.Position == 42 && s.StreamTitle == "Artist - Song" })
	if snapshot.Duration != 0 {
		t.Fatalf("live duration = %d, want none", snapshot.Duration)
	}
	if result := c.Seek(ctx, SeekRequest{Seconds: 10}); result.Code != CodeInvalid {
		t.Fatalf("live seek = %+v", result)
	}

	if result := c.Stop(ctx, Mutation{}); !result.OK() {
		t.Fatal(result)
	}
	snapshot, _ = c.Snapshot(ctx)
	if snapshot.ActiveLive || snapshot.StreamTitle != "" || !snapshot.SelectedLive {
		t.Fatalf("stopped live snapshot = %+v", snapshot)
	}
}

type titledTransport struct {
	*fakeTransport
	titles chan string
}

func (t titledTransport) SetTrackTitle(_ context.Context, title string) error {
	t.titles <- title
	return nil
}

type titledFactory struct {
	*fakeFactory
	titles chan string
}

func (f titledFactory) Open(ctx context.Context, device playback.Device) (Transport, error) {
	transport, err := f.fakeFactory.Open(ctx, device)
	return titledTransport{fakeTransport: transport.(*fakeTransport), titles: f.titles}, err
}

func TestLiveStreamChromecastLoadsStationAndPushesTitles(t *testing.T) {
	station := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Icy-MetaData") != "1" {
			http.Error(w, "titles not requested", http.StatusBadRequest)
			return
		}
		w.Header().Set("icy-metaint", "8")
		_, _ = io.WriteString(w, "audio...")
		_, _ = w.Write(append([]byte{2}, []byte("StreamTitle='Song B';\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00")...))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer station.Close()
	log := &eventLog{}
	titles := make(chan string, 4)
	factory := titledFactory{fakeFactory: &fakeFactory{log: log}, titles: titles}
	c := New(Config{Discovery: newFakeDiscovery(playback.Device{ID: "cast", Protocol: "Chromecast"}), TransportFactory: factory, MediaServer: &fakeServer{log: log}, OperationTimeout: time.Second})
	defer c.Close()
	awaitDevices(t, c, 1)
	ctx := context.Background()
	if result := c.SelectDevice(ctx, Mutation{}, "cast"); !result.OK() {
		t.Fatal(result)
	}
	if result := c.PlayMedia(ctx, Mutation{}, liveMedia(station.URL)); !result.OK() {
		t.Fatal(result)
	}
	if load := factory.opened[0].load; load.MediaURL != station.URL || !load.Live {
		t.Fatalf("Chromecast live load = %+v", load)
	}
	awaitSnapshotState(t, c, func(s Snapshot) bool { return s.StreamTitle == "Song B" })
	select {
	case title := <-titles:
		if title != "Song B" {
			t.Fatalf("receiver title = %q", title)
		}
	case <-time.After(time.Second):
		t.Fatal("track title not pushed to receiver")
	}
	if result := c.Stop(ctx, Mutation{}); !result.OK() {
		t.Fatal(result)
	}
	// Transcoding needs the relay even on receivers that fetch stations.
	if result := c.SetTranscode(ctx, Mutation{}, true); !result.OK() {
		t.Fatal(result)
	}
	if result := c.PlayMedia(ctx, Mutation{}, liveMedia(station.URL)); !result.OK() {
		t.Fatal(result)
	}
	if load := factory.opened[len(factory.opened)-1].load; load.MediaURL != "http://127.0.0.1/media" || load.MediaType != "audio/aac" {
		t.Fatalf("transcoded Chromecast live load = %+v", load)
	}
}
