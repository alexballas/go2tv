package controller

import (
	"bytes"
	"context"
	"io"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/metadata"
)

type testReadSeekCloser struct{ *bytes.Reader }

func (testReadSeekCloser) Close() error { return nil }
func testMedia(id string, kind mediamodel.MediaKind) MediaRef {
	opener := func(context.Context) (io.ReadSeekCloser, time.Time, error) {
		return testReadSeekCloser{bytes.NewReader(nil)}, time.Time{}, nil
	}
	return MediaRef{RootID: "root", ID: id, Name: id, Kind: kind, OpenDirect: opener, OpenTranscode: opener}
}

func addTestQueue(t *testing.T, c *Controller, media ...MediaRef) {
	t.Helper()
	for _, ref := range media {
		if result := c.AddQueueItem(context.Background(), QueueAddRequest{Media: ref}); !result.OK() {
			t.Fatal(result)
		}
	}
}

type fakeDiscovery struct {
	mu      sync.Mutex
	devices []playback.Device
	updates chan []playback.Device
}

func newFakeDiscovery(devices ...playback.Device) *fakeDiscovery {
	return &fakeDiscovery{devices: devices, updates: make(chan []playback.Device, 4)}
}
func (d *fakeDiscovery) Start(context.Context)                   {}
func (d *fakeDiscovery) Refresh(context.Context) error           { return nil }
func (d *fakeDiscovery) Notifications() <-chan []playback.Device { return d.updates }
func (d *fakeDiscovery) Snapshot() []playback.Device {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.devices)
}
func (d *fakeDiscovery) Subscribe(int) (<-chan []playback.Device, func()) {
	d.updates <- d.Snapshot()
	return d.updates, func() {}
}

type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *eventLog) add(event string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
}
func (l *eventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.events)
}

type fakeFactory struct {
	log       *eventLog
	mu        sync.Mutex
	opened    []*fakeTransport
	loadBlock chan struct{}
	stopBlock chan struct{}
	volume    int
}

func (f *fakeFactory) Open(_ context.Context, device playback.Device) (Transport, error) {
	f.log.add("open:" + device.ID)
	t := &fakeTransport{id: device.ID, log: f.log, loadBlock: f.loadBlock, stopBlock: f.stopBlock, volume: f.volume}
	f.mu.Lock()
	f.opened = append(f.opened, t)
	f.mu.Unlock()
	return t, nil
}

type fakeTransport struct {
	id        string
	log       *eventLog
	loadBlock chan struct{}
	stopBlock chan struct{}
	load      playback.LoadRequest
	volume    int
}

func (t *fakeTransport) Load(ctx context.Context, request playback.LoadRequest) error {
	t.load = request
	t.log.add("load:" + t.id)
	if t.loadBlock != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.loadBlock:
		}
	}
	return nil
}
func (t *fakeTransport) LoadOnExisting(ctx context.Context, request playback.LoadRequest) error {
	t.load = request
	t.log.add("load-existing:" + t.id)
	if t.loadBlock != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.loadBlock:
		}
	}
	return nil
}
func (t *fakeTransport) Play(context.Context) error  { t.log.add("play:" + t.id); return nil }
func (t *fakeTransport) Pause(context.Context) error { t.log.add("pause:" + t.id); return nil }
func (t *fakeTransport) Stop(ctx context.Context) error {
	t.log.add("stop:" + t.id)
	if t.stopBlock != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.stopBlock:
		}
	}
	return nil
}
func (t *fakeTransport) Close(context.Context) error { t.log.add("close:" + t.id); return nil }
func (t *fakeTransport) Volume(context.Context) (int, error) {
	t.log.add("volume:get:" + t.id)
	return t.volume, nil
}
func (t *fakeTransport) SetVolume(_ context.Context, volume int) error {
	t.volume = volume
	t.log.add("volume:set:" + t.id + ":" + strconv.Itoa(volume))
	return nil
}
func (t *fakeTransport) SetMute(context.Context, bool) error { t.log.add("mute:" + t.id); return nil }
func (t *fakeTransport) Seek(_ context.Context, value string) error {
	t.log.add("seek:" + value)
	return nil
}
func (t *fakeTransport) Position(context.Context) (playback.Position, error) {
	return playback.Position{}, nil
}
func (t *fakeTransport) State(context.Context) (string, error)            { return "PLAYING", nil }
func (t *fakeTransport) SetNextURI(context.Context, string, string) error { return nil }
func (t *fakeTransport) ClearNextURI(context.Context) error               { return nil }
func (t *fakeTransport) Resubscribe(context.Context, string) error        { return nil }
func (t *fakeTransport) Unsubscribe(context.Context, string) error        { return nil }

type fakeServer struct {
	log     *eventLog
	mu      sync.Mutex
	request playback.ServerRequest
}

func (s *fakeServer) Start(_ context.Context, request playback.ServerRequest) (playback.MediaRoute, error) {
	s.mu.Lock()
	s.request = request
	s.mu.Unlock()
	s.log.add("server:start:" + request.Target.ID)
	return playback.MediaRoute{URL: "http://127.0.0.1/media"}, nil
}
func (s *fakeServer) Stop(context.Context) error { s.log.add("server:stop"); return nil }
func (s *fakeServer) Add(context.Context, playback.RouteRequest) (playback.MediaRoute, error) {
	return playback.MediaRoute{URL: "http://127.0.0.1/artwork.jpg"}, nil
}
func (s *fakeServer) Remove(context.Context, string) error { return nil }

func (s *fakeServer) lastRequest() playback.ServerRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.request
}

func newTestController(devices ...playback.Device) (*Controller, *eventLog, *fakeFactory) {
	log := &eventLog{}
	factory := &fakeFactory{log: log}
	c := New(Config{Discovery: newFakeDiscovery(devices...), TransportFactory: factory, MediaServer: &fakeServer{log: log}, OperationTimeout: time.Second})
	return c, log, factory
}

func awaitDevices(t *testing.T, c *Controller, count int) Snapshot {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := c.Snapshot(context.Background())
		if err == nil && len(snapshot.Devices) == count {
			return snapshot
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("devices not received")
	return Snapshot{}
}

func TestPolicyAtomicAndExpectedRevision(t *testing.T) {
	c, _, _ := newTestController(playback.Device{ID: "one", Protocol: "DLNA"})
	defer c.Close()
	snapshot := awaitDevices(t, c, 1)
	result := c.SetPolicy(context.Background(), PolicyRequest{Mutation: Mutation{RequestID: "bad", ExpectedRevision: &snapshot.Revision}, Policy: Policy{LoopSelected: true, AutoPlayNext: true, ImageDurationSeconds: 10}})
	if result.Code != CodeInvalid {
		t.Fatalf("invalid policy code = %q", result.Code)
	}
	after, _ := c.Snapshot(context.Background())
	if after.Policy != DefaultPolicy() || after.Revision != snapshot.Revision {
		t.Fatalf("invalid mutation committed: %#v", after)
	}
	stale := snapshot.Revision - 1
	result = c.SelectDevice(context.Background(), Mutation{RequestID: "stale", ExpectedRevision: &stale}, "one")
	if result.Code != CodeConflict || result.RequestID != "stale" || result.Revision != snapshot.Revision {
		t.Fatalf("conflict result = %#v", result)
	}
}

func TestTwoClientExpectedRevisionConflict(t *testing.T) {
	c, _, _ := newTestController(playback.Device{ID: "one", Protocol: "DLNA"})
	defer c.Close()
	snapshot := awaitDevices(t, c, 1)
	results := make(chan Result, 2)
	for i := range 2 {
		go func(enabled bool) {
			results <- c.SetTranscode(context.Background(), Mutation{RequestID: "client", ExpectedRevision: &snapshot.Revision}, enabled)
		}(i == 0)
	}
	first, second := <-results, <-results
	if first.OK() == second.OK() {
		t.Fatalf("results = %#v %#v", first, second)
	}
	if !first.OK() && first.Code != CodeConflict || !second.OK() && second.Code != CodeConflict {
		t.Fatalf("results = %#v %#v", first, second)
	}
}

func TestActiveTargetUnaffectedBySelection(t *testing.T) {
	c, log, _ := newTestController(
		playback.Device{ID: "one", Protocol: "DLNA"},
		playback.Device{ID: "two", Protocol: "Chromecast"},
	)
	defer c.Close()
	awaitDevices(t, c, 2)
	if result := c.SelectDevice(context.Background(), Mutation{}, "one"); !result.OK() {
		t.Fatal(result)
	}
	if result := c.SelectMedia(context.Background(), Mutation{}, testMedia("a.mp3", mediamodel.MediaKindAudio)); !result.OK() {
		t.Fatal(result)
	}
	if result := c.Play(context.Background(), PlayRequest{}); !result.OK() {
		t.Fatal(result)
	}
	if result := c.SelectDevice(context.Background(), Mutation{}, "two"); !result.OK() {
		t.Fatal(result)
	}
	if result := c.Pause(context.Background(), Mutation{}); !result.OK() {
		t.Fatal(result)
	}
	snapshot, _ := c.Snapshot(context.Background())
	if snapshot.ActiveDeviceID != "one" || snapshot.SelectedDeviceID != "two" {
		t.Fatalf("targets = active %q selected %q", snapshot.ActiveDeviceID, snapshot.SelectedDeviceID)
	}
	if !slices.Contains(log.snapshot(), "pause:one") || slices.Contains(log.snapshot(), "pause:two") {
		t.Fatalf("events = %v", log.snapshot())
	}
	if !slices.Contains(log.snapshot(), "server:start:one") {
		t.Fatalf("media server target missing: %v", log.snapshot())
	}
}

func TestAdjustVolumeReadsDeviceAndStepsByOne(t *testing.T) {
	tests := []struct {
		name  string
		delta int
		want  int
	}{
		{name: "down", delta: -1, want: 36},
		{name: "up", delta: 1, want: 38},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, log, factory := newTestController(playback.Device{ID: "one", Protocol: "DLNA"})
			defer c.Close()
			factory.volume = 37
			awaitDevices(t, c, 1)
			if result := c.SelectDevice(context.Background(), Mutation{}, "one"); !result.OK() {
				t.Fatal(result)
			}
			if result := c.AdjustVolume(context.Background(), Mutation{}, tt.delta); !result.OK() {
				t.Fatal(result)
			}
			snapshot, err := c.Snapshot(context.Background())
			if err != nil || snapshot.Volume != tt.want {
				t.Fatalf("volume = %d, err = %v", snapshot.Volume, err)
			}
			events := log.snapshot()
			if !slices.Contains(events, "volume:get:one") || !slices.Contains(events, "volume:set:one:"+strconv.Itoa(tt.want)) {
				t.Fatalf("events = %v", events)
			}
		})
	}
}

func TestQueueAddPreservesIDsAndSelectsByID(t *testing.T) {
	c, _, _ := newTestController()
	defer c.Close()
	first := c.AddQueueItem(context.Background(), QueueAddRequest{Media: testMedia("a.mp3", mediamodel.MediaKindAudio)})
	second := c.AddQueueItem(context.Background(), QueueAddRequest{Media: testMedia("b.mp3", mediamodel.MediaKindAudio)})
	if !first.OK() || !second.OK() || first.ItemID == "" || first.ItemID == second.ItemID {
		t.Fatalf("adds = %#v %#v", first, second)
	}
	if result := c.SelectQueueItem(context.Background(), Mutation{}, first.ItemID); !result.OK() {
		t.Fatal(result)
	}
	snapshot, _ := c.Snapshot(context.Background())
	if len(snapshot.Queue) != 2 || snapshot.Queue[0].ID != first.ItemID || !snapshot.Queue[0].IsSelected || snapshot.SelectedMedia != "a.mp3" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestSelectedQueueItemTopCardPlayPreservesIdentity(t *testing.T) {
	c, _, _ := newTestController(playback.Device{ID: "one", Protocol: "DLNA"})
	defer c.Close()
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, "one")
	added := c.AddQueueItem(context.Background(), QueueAddRequest{Media: testMedia("a.mp3", mediamodel.MediaKindAudio), Select: true})
	if !added.OK() {
		t.Fatal(added.Result)
	}
	snapshot, _ := c.Snapshot(context.Background())
	if len(snapshot.Queue) != 1 || snapshot.Queue[0].ID != added.ItemID || !snapshot.Queue[0].IsSelected || snapshot.SelectedMedia != "a.mp3" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if result := c.Play(context.Background(), PlayRequest{}); !result.OK() {
		t.Fatal(result)
	}
	playing, _ := c.Snapshot(context.Background())
	if !playing.Queue[0].IsActive || playing.Queue[0].ID != added.ItemID {
		t.Fatalf("top-card play lost queue identity: %#v", playing.Queue)
	}
}

func TestQueueAndPlayAppendsAndReplaces(t *testing.T) {
	c, log, _ := newTestController(playback.Device{ID: "one", Protocol: "DLNA"})
	defer c.Close()
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, "one")
	first := c.AddQueueItem(context.Background(), QueueAddRequest{Media: testMedia("a.mp3", mediamodel.MediaKindAudio)})
	if result := c.Play(context.Background(), PlayRequest{QueueItemID: first.ItemID}); !result.OK() {
		t.Fatal(result)
	}
	if result := c.QueueAndPlay(context.Background(), Mutation{}, testMedia("b.mp3", mediamodel.MediaKindAudio)); !result.OK() {
		t.Fatal(result)
	}
	snapshot, _ := c.Snapshot(context.Background())
	if len(snapshot.Queue) != 2 || snapshot.Queue[0].IsActive || !snapshot.Queue[1].IsActive || !snapshot.Queue[1].IsSelected || snapshot.SelectedMedia != "b.mp3" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if count(log.snapshot(), "load:one") != 2 {
		t.Fatalf("events = %v", log.snapshot())
	}
}

func TestReplacementTeardownBeforeLoad(t *testing.T) {
	c, log, _ := newTestController(playback.Device{ID: "one", Protocol: "DLNA"})
	defer c.Close()
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, "one")
	c.SelectMedia(context.Background(), Mutation{}, testMedia("a.mp3", mediamodel.MediaKindAudio))
	if result := c.Play(context.Background(), PlayRequest{}); !result.OK() {
		t.Fatal(result)
	}
	c.SelectMedia(context.Background(), Mutation{}, testMedia("b.mp3", mediamodel.MediaKindAudio))
	if result := c.Play(context.Background(), PlayRequest{}); !result.OK() {
		t.Fatal(result)
	}
	events := log.snapshot()
	secondOpen := slices.Index(events[1:], "open:one") + 1
	stop := slices.Index(events, "stop:one")
	serverStop := slices.Index(events, "server:stop")
	closeIndex := slices.Index(events, "close:one")
	if stop < 0 || serverStop < stop || closeIndex < serverStop || secondOpen < closeIndex {
		t.Fatalf("replacement order = %v", events)
	}
}

func TestSeekValidatesAndUsesActiveProtocol(t *testing.T) {
	c, log, _ := newTestController(playback.Device{ID: "one", Protocol: "DLNA"})
	defer c.Close()
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, "one")
	c.SelectMedia(context.Background(), Mutation{}, testMedia("a.mp3", mediamodel.MediaKindAudio))
	if result := c.Play(context.Background(), PlayRequest{}); !result.OK() {
		t.Fatal(result)
	}
	playing, _ := c.Snapshot(context.Background())
	c.HandleMonitorEvent(context.Background(), playback.MonitorEvent{Generation: playing.Generation, Position: 1, Duration: 60})
	deadline := time.Now().Add(time.Second)
	for {
		snapshot, _ := c.Snapshot(context.Background())
		if snapshot.Duration == 60 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("duration update missing")
		}
		time.Sleep(time.Millisecond)
	}
	if result := c.Seek(context.Background(), SeekRequest{Seconds: 61}); result.Code != CodeInvalid {
		t.Fatalf("past duration = %#v", result)
	}
	if result := c.Seek(context.Background(), SeekRequest{Seconds: 12}); !result.OK() {
		t.Fatal(result)
	}
	if !slices.Contains(log.snapshot(), "seek:00:00:12") {
		t.Fatalf("events = %v", log.snapshot())
	}
}

func TestChromecastLoadDoesNotPlayBeforeMediaStatus(t *testing.T) {
	c, log, _ := newTestController(playback.Device{ID: "cast", Protocol: "Chromecast"})
	defer c.Close()
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, "cast")
	c.SelectMedia(context.Background(), Mutation{}, testMedia("a.mp3", mediamodel.MediaKindAudio))
	if result := c.Play(context.Background(), PlayRequest{}); !result.OK() {
		t.Fatal(result)
	}
	if slices.Contains(log.snapshot(), "play:cast") {
		t.Fatalf("unexpected play after autoplay load: %v", log.snapshot())
	}
}

func TestChromecastAutoplayReusesConnection(t *testing.T) {
	c, log, factory := newTestController(playback.Device{ID: "cast", Protocol: "Chromecast"})
	defer c.Close()
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, "cast")
	addTestQueue(t, c, testMedia("a.mp3", mediamodel.MediaKindAudio), testMedia("b.mp3", mediamodel.MediaKindAudio))
	before, _ := c.Snapshot(context.Background())
	if result := c.SetPolicy(context.Background(), PolicyRequest{Policy: Policy{AutoPlayNext: true, ImageDurationSeconds: 10}}); !result.OK() {
		t.Fatal(result)
	}
	if result := c.Play(context.Background(), PlayRequest{QueueItemID: before.Queue[0].ID}); !result.OK() {
		t.Fatal(result)
	}
	playing, _ := c.Snapshot(context.Background())
	c.HandleMonitorEvent(context.Background(), playback.MonitorEvent{Generation: playing.Generation, Terminal: playback.TerminalFinished})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		after, _ := c.Snapshot(context.Background())
		if after.HasSession && after.Queue[1].IsActive {
			if len(factory.opened) != 1 || count(log.snapshot(), "load-existing:cast") != 1 || count(log.snapshot(), "stop:cast") != 0 || count(log.snapshot(), "close:cast") != 0 {
				t.Fatalf("connection not reused: %v", log.snapshot())
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("autoplay did not advance: %v", log.snapshot())
}

func TestLoopSelectedReplaysMediaOutsideQueue(t *testing.T) {
	c, log, factory := newTestController(playback.Device{ID: "cast", Protocol: "Chromecast"})
	defer c.Close()
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, "cast")
	c.SelectMedia(context.Background(), Mutation{}, testMedia("standalone.mp3", mediamodel.MediaKindAudio))
	if result := c.SetPolicy(context.Background(), PolicyRequest{Policy: Policy{LoopSelected: true, ImageDurationSeconds: 10}}); !result.OK() {
		t.Fatal(result)
	}
	if result := c.Play(context.Background(), PlayRequest{}); !result.OK() {
		t.Fatal(result)
	}
	playing, _ := c.Snapshot(context.Background())
	c.HandleMonitorEvent(context.Background(), playback.MonitorEvent{Generation: playing.Generation, Terminal: playback.TerminalFinished})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		after, _ := c.Snapshot(context.Background())
		if after.HasSession && after.Generation > playing.Generation {
			if after.SelectedMedia != "standalone.mp3" || len(factory.opened) != 1 || count(log.snapshot(), "load-existing:cast") != 1 {
				t.Fatalf("loop did not reuse active media: %#v %v", after, log.snapshot())
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("loop did not restart: %v", log.snapshot())
}

func TestLoopSelectedPreservesQueueIdentity(t *testing.T) {
	c, _, _ := newTestController(playback.Device{ID: "cast", Protocol: "Chromecast"})
	defer c.Close()
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, "cast")
	addTestQueue(t, c, testMedia("queued.mp3", mediamodel.MediaKindAudio))
	queued, _ := c.Snapshot(context.Background())
	id := queued.Queue[0].ID
	c.SetPolicy(context.Background(), PolicyRequest{Policy: Policy{LoopSelected: true, ImageDurationSeconds: 10}})
	if result := c.Play(context.Background(), PlayRequest{QueueItemID: id}); !result.OK() {
		t.Fatal(result)
	}
	playing, _ := c.Snapshot(context.Background())
	c.HandleMonitorEvent(context.Background(), playback.MonitorEvent{Generation: playing.Generation, Terminal: playback.TerminalFinished})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		after, _ := c.Snapshot(context.Background())
		if after.Generation > playing.Generation && after.HasSession {
			if len(after.Queue) != 1 || after.Queue[0].ID != id || !after.Queue[0].IsActive || !after.Queue[0].IsSelected {
				t.Fatalf("queue identity changed: %#v", after.Queue)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("queued loop did not restart")
}

func TestChromecastPauseResumeKeepsSession(t *testing.T) {
	c, log, factory := newTestController(playback.Device{ID: "cast", Protocol: "Chromecast"})
	defer c.Close()
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, "cast")
	c.SelectMedia(context.Background(), Mutation{}, testMedia("a.mp3", mediamodel.MediaKindAudio))
	if result := c.Play(context.Background(), PlayRequest{}); !result.OK() {
		t.Fatal(result)
	}
	if result := c.Pause(context.Background(), Mutation{}); !result.OK() {
		t.Fatal(result)
	}
	paused, _ := c.Snapshot(context.Background())
	if paused.PlaybackState != "PAUSED" {
		t.Fatalf("state = %q", paused.PlaybackState)
	}
	if result := c.Resume(context.Background(), Mutation{}); !result.OK() {
		t.Fatal(result)
	}
	if len(factory.opened) != 1 || count(log.snapshot(), "play:cast") != 1 || count(log.snapshot(), "stop:cast") != 0 || count(log.snapshot(), "close:cast") != 0 {
		t.Fatalf("events = %v", log.snapshot())
	}
}

func TestChromecastTranscodeLoadsMP4(t *testing.T) {
	c, _, factory := newTestController(playback.Device{ID: "cast", Protocol: "Chromecast"})
	defer c.Close()
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, "cast")
	c.SelectMedia(context.Background(), Mutation{}, testMedia("movie.mkv", mediamodel.MediaKindVideo))
	c.SetTranscode(context.Background(), Mutation{}, true)
	if result := c.Play(context.Background(), PlayRequest{}); !result.OK() {
		t.Fatal(result)
	}
	if got := factory.opened[0].load.MediaType; got != "video/mp4" {
		t.Fatalf("load media type = %q, want video/mp4", got)
	}
}

func TestLoadUsesDisplayNameAndArtwork(t *testing.T) {
	c, _, factory := newTestController(playback.Device{ID: "one", Protocol: "DLNA"})
	defer c.Close()
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, "one")
	media := testMedia("opaque-id", mediamodel.MediaKindAudio)
	media.Name = "Actual Song.mp3"
	media.Artwork = &metadata.ArtworkAsset{ID: "cover", Data: []byte("art"), MIMEType: "image/jpeg", Width: 20, Height: 30}
	c.SelectMedia(context.Background(), Mutation{}, media)
	if result := c.Play(context.Background(), PlayRequest{}); !result.OK() {
		t.Fatal(result)
	}
	load := factory.opened[0].load
	if load.Metadata.Title != "Actual Song.mp3" || !load.Seekable || load.Metadata.Artwork == nil || load.Metadata.Artwork.URL != "http://127.0.0.1/artwork.jpg" || string(load.ArtworkData) != "art" {
		t.Fatalf("load = %#v", load)
	}
	snapshot, _ := c.Snapshot(context.Background())
	if snapshot.SelectedMedia != "Actual Song.mp3" || snapshot.ArtworkID != "cover" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestTranscodedSeekIgnoresOldMonitorStop(t *testing.T) {
	c, _, factory := newTestController(playback.Device{ID: "one", Protocol: "DLNA"})
	defer c.Close()
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, "one")
	c.SelectMedia(context.Background(), Mutation{}, testMedia("a.mp3", mediamodel.MediaKindAudio))
	c.SetTranscode(context.Background(), Mutation{}, true)
	if result := c.Play(context.Background(), PlayRequest{}); !result.OK() {
		t.Fatal(result)
	}
	playing, _ := c.Snapshot(context.Background())
	c.HandleMonitorEvent(context.Background(), playback.MonitorEvent{Generation: playing.Generation, Position: 1, Duration: 60})
	for {
		snapshot, _ := c.Snapshot(context.Background())
		if snapshot.Duration == 60 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	stopBlock := make(chan struct{})
	factory.mu.Lock()
	factory.opened[0].stopBlock = stopBlock
	factory.mu.Unlock()
	result := make(chan Result, 1)
	go func() { result <- c.Seek(context.Background(), SeekRequest{Seconds: 12}) }()
	deadline := time.Now().Add(time.Second)
	for {
		snapshot, _ := c.Snapshot(context.Background())
		if snapshot.Generation > playing.Generation {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("seek generation not fenced")
		}
		time.Sleep(time.Millisecond)
	}
	c.HandleMonitorEvent(context.Background(), playback.MonitorEvent{Generation: playing.Generation, Terminal: playback.TerminalFinished})
	close(stopBlock)
	if got := <-result; !got.OK() {
		t.Fatal(got)
	}
	after, _ := c.Snapshot(context.Background())
	if !after.HasSession || after.Position != 12 {
		t.Fatalf("snapshot = %#v", after)
	}
}

func TestStopCancelsPendingPlay(t *testing.T) {
	c, _, factory := newTestController(playback.Device{ID: "one", Protocol: "DLNA"})
	defer c.Close()
	factory.loadBlock = make(chan struct{})
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, "one")
	c.SelectMedia(context.Background(), Mutation{}, testMedia("a.mp3", mediamodel.MediaKindAudio))
	playResult := make(chan Result, 1)
	go func() { playResult <- c.Play(context.Background(), PlayRequest{Mutation: Mutation{RequestID: "play"}}) }()
	deadline := time.Now().Add(time.Second)
	for !slices.Contains(factory.log.snapshot(), "load:one") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if result := c.Stop(context.Background(), Mutation{RequestID: "stop"}); !result.OK() {
		t.Fatalf("stop = %#v", result)
	}
	result := <-playResult
	if result.Code != CodeBusy {
		t.Fatalf("play = %#v", result)
	}
	snapshot, _ := c.Snapshot(context.Background())
	if snapshot.HasSession || snapshot.TerminalReason != playback.TerminalUserStop {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestRemoveActiveQueueItemRejected(t *testing.T) {
	c, log, _ := newTestController(playback.Device{ID: "one", Protocol: "DLNA"})
	defer c.Close()
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, "one")
	addTestQueue(t, c, testMedia("a.mp3", mediamodel.MediaKindAudio), testMedia("b.mp3", mediamodel.MediaKindAudio))
	snapshot, _ := c.Snapshot(context.Background())
	id := snapshot.Queue[0].ID
	if result := c.Play(context.Background(), PlayRequest{QueueItemID: id}); !result.OK() {
		t.Fatal(result)
	}
	beforeStops := count(log.snapshot(), "stop:one")
	if result := c.RemoveQueueItem(context.Background(), Mutation{}, id); result.Code != CodeInvalid {
		t.Fatalf("remove = %#v", result)
	}
	after, _ := c.Snapshot(context.Background())
	if !after.HasSession || len(after.Queue) != 2 || !after.Queue[0].IsActive || after.SelectedMedia != "a.mp3" || count(log.snapshot(), "stop:one") != beforeStops {
		t.Fatalf("active item changed: %#v %v", after, log.snapshot())
	}
}

func TestRemoveSelectedQueueItemRejected(t *testing.T) {
	c, _, _ := newTestController()
	defer c.Close()
	addTestQueue(t, c, testMedia("a.mp3", mediamodel.MediaKindAudio), testMedia("b.mp3", mediamodel.MediaKindAudio))
	snapshot, _ := c.Snapshot(context.Background())
	selectedID, otherID := snapshot.Queue[0].ID, snapshot.Queue[1].ID
	if result := c.SelectQueueItem(context.Background(), Mutation{}, selectedID); !result.OK() {
		t.Fatal(result)
	}
	if result := c.RemoveQueueItem(context.Background(), Mutation{}, selectedID); result.Code != CodeInvalid {
		t.Fatalf("selected remove = %#v", result)
	}
	if result := c.RemoveQueueItem(context.Background(), Mutation{}, otherID); !result.OK() {
		t.Fatalf("unselected remove = %#v", result)
	}
	after, _ := c.Snapshot(context.Background())
	if len(after.Queue) != 1 || after.Queue[0].ID != selectedID || !after.Queue[0].IsSelected || after.SelectedMedia != "a.mp3" {
		t.Fatalf("snapshot = %#v", after)
	}
}

func TestAudioOnlyAutoplayRejectsVideoFollowup(t *testing.T) {
	c, _, _ := newTestController(playback.Device{ID: "audio", Protocol: "Chromecast", AudioOnly: true})
	defer c.Close()
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, "audio")
	addTestQueue(t, c, testMedia("a.mp3", mediamodel.MediaKindAudio), testMedia("b.mp4", mediamodel.MediaKindVideo))
	snapshot, _ := c.Snapshot(context.Background())
	if result := c.SetPolicy(context.Background(), PolicyRequest{Policy: Policy{AutoPlayNext: true, ImageDurationSeconds: 10}}); !result.OK() {
		t.Fatal(result)
	}
	if result := c.Play(context.Background(), PlayRequest{QueueItemID: snapshot.Queue[0].ID}); !result.OK() {
		t.Fatal(result)
	}
	playing, _ := c.Snapshot(context.Background())
	c.HandleMonitorEvent(context.Background(), playback.MonitorEvent{Generation: playing.Generation, Terminal: playback.TerminalFinished})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		after, _ := c.Snapshot(context.Background())
		if !after.HasSession {
			if after.LastError != "device supports audio only" || after.TerminalReason != playback.TerminalFinished {
				t.Fatalf("snapshot = %#v", after)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("terminal event not applied")
}

func count(values []string, target string) int {
	result := 0
	for _, value := range values {
		if value == target {
			result++
		}
	}
	return result
}

func TestArtworkCacheLRUAndSingleflight(t *testing.T) {
	cache := NewArtworkCache(4)
	var mu sync.Mutex
	loads := 0
	loader := func(context.Context) ([]byte, string, error) {
		mu.Lock()
		loads++
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		return []byte("abc"), "image/jpeg", nil
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cache.Get(context.Background(), "a", loader); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	mu.Lock()
	got := loads
	mu.Unlock()
	if got != 1 {
		t.Fatalf("loads = %d", got)
	}
	if _, err := cache.Get(context.Background(), "b", func(context.Context) ([]byte, string, error) { return []byte("zz"), "image/jpeg", nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Get(context.Background(), "a", loader); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got = loads
	mu.Unlock()
	if got != 2 {
		t.Fatalf("evicted loads = %d", got)
	}
}
