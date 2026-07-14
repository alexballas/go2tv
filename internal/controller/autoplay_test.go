package controller

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/internal/playback"
)

type autoplayTimer struct {
	ch      chan time.Time
	stopped chan struct{}
	once    sync.Once
}

func (t *autoplayTimer) C() <-chan time.Time { return t.ch }
func (t *autoplayTimer) Stop() bool {
	t.once.Do(func() { close(t.stopped) })
	return true
}

type autoplayTicker struct{ ch chan time.Time }

func (t *autoplayTicker) C() <-chan time.Time { return t.ch }
func (*autoplayTicker) Stop()                 {}

type autoplayClock struct{ timers chan *autoplayTimer }

func newAutoplayClock() *autoplayClock {
	return &autoplayClock{timers: make(chan *autoplayTimer, 4)}
}

func (*autoplayClock) Now() time.Time { return time.Time{} }
func (*autoplayClock) NewTicker(time.Duration) playback.Ticker {
	return &autoplayTicker{ch: make(chan time.Time)}
}
func (c *autoplayClock) NewTimer(time.Duration) playback.Timer {
	timer := &autoplayTimer{ch: make(chan time.Time, 1), stopped: make(chan struct{})}
	c.timers <- timer
	return timer
}

func waitAutoplayTimer(t *testing.T, clock *autoplayClock) *autoplayTimer {
	t.Helper()
	select {
	case timer := <-clock.timers:
		return timer
	case <-time.After(time.Second):
		t.Fatal("image timer not armed")
		return nil
	}
}

func awaitAutoplaySnapshot(t *testing.T, c *Controller, match func(Snapshot) bool) Snapshot {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	var last Snapshot
	for {
		snapshot, err := c.Snapshot(context.Background())
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		last = snapshot
		if match(snapshot) {
			return snapshot
		}
		select {
		case <-deadline.C:
			t.Fatalf("autoplay timeout: %#v", last)
		case <-ticker.C:
		}
	}
}

func TestAutoplayQueueTraversalMatchesDesktop(t *testing.T) {
	protocols := []string{"DLNA", "Chromecast"}
	for _, protocol := range protocols {
		t.Run(protocol, func(t *testing.T) {
			t.Run("tail wraps to first", func(t *testing.T) {
				device := playback.Device{ID: "renderer", Protocol: protocol}
				c, _, _ := newTestController(device)
				defer c.Close()
				awaitDevices(t, c, 1)
				c.SelectDevice(context.Background(), Mutation{}, device.ID)
				addTestQueue(t, c,
					testMedia("a.mp3", mediamodel.MediaKindAudio),
					testMedia("b.mp3", mediamodel.MediaKindAudio),
				)
				queued, _ := c.Snapshot(context.Background())
				if result := c.SetPolicy(context.Background(), PolicyRequest{Policy: Policy{AutoPlayNext: true, ImageDurationSeconds: 10}}); !result.OK() {
					t.Fatal(result)
				}
				if result := c.Play(context.Background(), PlayRequest{QueueItemID: queued.Queue[1].ID}); !result.OK() {
					t.Fatal(result)
				}
				playing, _ := c.Snapshot(context.Background())
				c.HandleMonitorEvent(context.Background(), playback.MonitorEvent{Generation: playing.Generation, Terminal: playback.TerminalFinished})
				after := awaitAutoplaySnapshot(t, c, func(snapshot Snapshot) bool {
					return snapshot.Generation > playing.Generation && snapshot.HasSession && snapshot.Queue[0].IsActive
				})
				if !after.Queue[0].IsSelected || after.Queue[0].ID != queued.Queue[0].ID {
					t.Fatalf("wrapped queue = %#v", after.Queue)
				}
			})

			t.Run("same type wraps", func(t *testing.T) {
				device := playback.Device{ID: "renderer", Protocol: protocol}
				c, _, _ := newTestController(device)
				defer c.Close()
				awaitDevices(t, c, 1)
				c.SelectDevice(context.Background(), Mutation{}, device.ID)
				addTestQueue(t, c,
					testMedia("a.mp3", mediamodel.MediaKindAudio),
					testMedia("b.mp4", mediamodel.MediaKindVideo),
					testMedia("c.mp3", mediamodel.MediaKindAudio),
				)
				queued, _ := c.Snapshot(context.Background())
				policy := Policy{AutoPlayNext: true, AutoPlaySameType: true, ImageDurationSeconds: 10}
				if result := c.SetPolicy(context.Background(), PolicyRequest{Policy: policy}); !result.OK() {
					t.Fatal(result)
				}
				if result := c.Play(context.Background(), PlayRequest{QueueItemID: queued.Queue[2].ID}); !result.OK() {
					t.Fatal(result)
				}
				playing, _ := c.Snapshot(context.Background())
				c.HandleMonitorEvent(context.Background(), playback.MonitorEvent{Generation: playing.Generation, Terminal: playback.TerminalFinished})
				after := awaitAutoplaySnapshot(t, c, func(snapshot Snapshot) bool {
					return snapshot.Generation > playing.Generation && snapshot.HasSession && snapshot.Queue[0].IsActive
				})
				if !after.Queue[0].IsSelected || after.Queue[0].ID != queued.Queue[0].ID {
					t.Fatalf("same-type queue = %#v", after.Queue)
				}
			})

			t.Run("only current candidate stops", func(t *testing.T) {
				device := playback.Device{ID: "renderer", Protocol: protocol}
				c, log, _ := newTestController(device)
				defer c.Close()
				awaitDevices(t, c, 1)
				c.SelectDevice(context.Background(), Mutation{}, device.ID)
				addTestQueue(t, c,
					testMedia("a.mp3", mediamodel.MediaKindAudio),
					testMedia("b.mp4", mediamodel.MediaKindVideo),
				)
				queued, _ := c.Snapshot(context.Background())
				policy := Policy{AutoPlayNext: true, AutoPlaySameType: true, ImageDurationSeconds: 10}
				if result := c.SetPolicy(context.Background(), PolicyRequest{Policy: policy}); !result.OK() {
					t.Fatal(result)
				}
				if result := c.Play(context.Background(), PlayRequest{QueueItemID: queued.Queue[0].ID}); !result.OK() {
					t.Fatal(result)
				}
				playing, _ := c.Snapshot(context.Background())
				c.HandleMonitorEvent(context.Background(), playback.MonitorEvent{Generation: playing.Generation, Terminal: playback.TerminalFinished})
				after := awaitAutoplaySnapshot(t, c, func(snapshot Snapshot) bool {
					return !snapshot.HasSession && snapshot.TerminalReason == playback.TerminalFinished
				})
				if after.Generation != playing.Generation || after.Queue[0].IsActive || count(log.snapshot(), "open:renderer") != 1 {
					t.Fatalf("unexpected replay: %#v %v", after, log.snapshot())
				}
			})
		})
	}
}

func TestAutoplayTransitionAnchorsActiveQueueID(t *testing.T) {
	for _, protocol := range []string{"DLNA", "Chromecast"} {
		t.Run(protocol, func(t *testing.T) {
			device := playback.Device{ID: "renderer", Protocol: protocol}
			c, _, _ := newTestController(device)
			defer c.Close()
			awaitDevices(t, c, 1)
			c.SelectDevice(context.Background(), Mutation{}, device.ID)
			addTestQueue(t, c,
				testMedia("a.mp3", mediamodel.MediaKindAudio),
				testMedia("b.mp3", mediamodel.MediaKindAudio),
				testMedia("c.mp3", mediamodel.MediaKindAudio),
				testMedia("d.mp3", mediamodel.MediaKindAudio),
			)
			queued, _ := c.Snapshot(context.Background())
			activeID := queued.Queue[1].ID
			wantID := queued.Queue[3].ID
			if result := c.SetPolicy(context.Background(), PolicyRequest{Policy: Policy{AutoPlayNext: true, ImageDurationSeconds: 10}}); !result.OK() {
				t.Fatal(result)
			}
			if result := c.Play(context.Background(), PlayRequest{QueueItemID: activeID}); !result.OK() {
				t.Fatal(result)
			}
			playing, _ := c.Snapshot(context.Background())
			if result := c.SelectQueueItem(context.Background(), Mutation{}, queued.Queue[0].ID); !result.OK() {
				t.Fatal(result)
			}
			if result := c.MoveQueueItem(context.Background(), Mutation{}, activeID, 1); !result.OK() {
				t.Fatal(result)
			}
			c.HandleMonitorEvent(context.Background(), playback.MonitorEvent{Generation: playing.Generation, Terminal: playback.TerminalFinished})
			after := awaitAutoplaySnapshot(t, c, func(snapshot Snapshot) bool {
				return snapshot.Generation > playing.Generation && snapshot.HasSession && snapshot.PlaybackState == "PLAYING" && snapshot.Queue[3].IsActive
			})
			if len(after.Queue) != 4 || !after.Queue[3].IsActive || !after.Queue[3].IsSelected || after.Queue[3].ID != wantID {
				t.Fatalf("transition queue = %#v", after.Queue)
			}
		})
	}
}

func TestAutoplayKeepsActiveRenderer(t *testing.T) {
	for _, protocol := range []string{"DLNA", "Chromecast"} {
		t.Run(protocol, func(t *testing.T) {
			otherProtocol := "DLNA"
			if protocol == "DLNA" {
				otherProtocol = "Chromecast"
			}
			active := playback.Device{ID: "active", Protocol: protocol}
			other := playback.Device{ID: "other", Protocol: otherProtocol}
			c, log, _ := newTestController(active, other)
			defer c.Close()
			awaitDevices(t, c, 2)
			c.SelectDevice(context.Background(), Mutation{}, active.ID)
			addTestQueue(t, c,
				testMedia("a.mp3", mediamodel.MediaKindAudio),
				testMedia("b.mp3", mediamodel.MediaKindAudio),
			)
			queued, _ := c.Snapshot(context.Background())
			c.SetPolicy(context.Background(), PolicyRequest{Policy: Policy{AutoPlayNext: true, ImageDurationSeconds: 10}})
			if result := c.Play(context.Background(), PlayRequest{QueueItemID: queued.Queue[0].ID}); !result.OK() {
				t.Fatal(result)
			}
			playing, _ := c.Snapshot(context.Background())
			c.SelectDevice(context.Background(), Mutation{}, other.ID)
			c.HandleMonitorEvent(context.Background(), playback.MonitorEvent{Generation: playing.Generation, Terminal: playback.TerminalFinished})
			after := awaitAutoplaySnapshot(t, c, func(snapshot Snapshot) bool {
				return snapshot.Generation > playing.Generation && snapshot.HasSession && snapshot.Queue[1].IsActive
			})
			if after.ActiveDeviceID != active.ID || after.SelectedDeviceID != other.ID || count(log.snapshot(), "open:other") != 0 {
				t.Fatalf("renderer changed: %#v %v", after, log.snapshot())
			}
		})
	}
}

func TestChromecastAutoplayLostStatusOpensFreshConnection(t *testing.T) {
	device := playback.Device{ID: "cast", Protocol: "Chromecast"}
	c, log, factory := newTestController(device)
	defer c.Close()
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, device.ID)
	addTestQueue(t, c,
		testMedia("a.mp3", mediamodel.MediaKindAudio),
		testMedia("b.mp3", mediamodel.MediaKindAudio),
	)
	queued, _ := c.Snapshot(context.Background())
	if result := c.SetPolicy(context.Background(), PolicyRequest{Policy: Policy{AutoPlayNext: true, ImageDurationSeconds: 10}}); !result.OK() {
		t.Fatal(result)
	}
	if result := c.Play(context.Background(), PlayRequest{QueueItemID: queued.Queue[0].ID}); !result.OK() {
		t.Fatal(result)
	}
	factory.mu.Lock()
	factory.opened[0].closeErr = errors.New("connection lost")
	factory.mu.Unlock()
	playing, _ := c.Snapshot(context.Background())
	c.HandleMonitorEvent(context.Background(), playback.MonitorEvent{
		Generation: playing.Generation,
		Terminal:   playback.TerminalFinished,
		Err:        errors.New("status lost"),
	})
	awaitAutoplaySnapshot(t, c, func(snapshot Snapshot) bool {
		return snapshot.Generation > playing.Generation && snapshot.HasSession && snapshot.Queue[1].IsActive
	})
	events := log.snapshot()
	if count(events, "open:cast") != 2 || count(events, "load-existing:cast") != 0 {
		t.Fatalf("dead connection reused: %v", events)
	}
}

func TestDLNAAutoplayIgnoresAlreadyStoppedRenderer(t *testing.T) {
	device := playback.Device{ID: "renderer", Protocol: "DLNA"}
	c, _, factory := newTestController(device)
	defer c.Close()
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, device.ID)
	addTestQueue(t, c,
		testMedia("a.mp3", mediamodel.MediaKindAudio),
		testMedia("b.mp3", mediamodel.MediaKindAudio),
	)
	queued, _ := c.Snapshot(context.Background())
	c.SetPolicy(context.Background(), PolicyRequest{Policy: Policy{AutoPlayNext: true, ImageDurationSeconds: 10}})
	if result := c.Play(context.Background(), PlayRequest{QueueItemID: queued.Queue[0].ID}); !result.OK() {
		t.Fatal(result)
	}
	factory.mu.Lock()
	factory.opened[0].stopErr = errors.New("already stopped")
	factory.mu.Unlock()
	playing, _ := c.Snapshot(context.Background())
	c.HandleMonitorEvent(context.Background(), playback.MonitorEvent{Generation: playing.Generation, Terminal: playback.TerminalFinished})
	after := awaitAutoplaySnapshot(t, c, func(snapshot Snapshot) bool {
		return snapshot.Generation > playing.Generation && snapshot.HasSession && snapshot.Queue[1].IsActive
	})
	if after.LastError != "" || after.PlaybackState != "PLAYING" {
		t.Fatalf("transition = %#v", after)
	}
}

func TestAutoplayDefersFinishDuringPause(t *testing.T) {
	for _, protocol := range []string{"DLNA", "Chromecast"} {
		t.Run(protocol, func(t *testing.T) {
			device := playback.Device{ID: "renderer", Protocol: protocol}
			c, log, factory := newTestController(device)
			defer c.Close()
			factory.pauseBlock = make(chan struct{})
			awaitDevices(t, c, 1)
			c.SelectDevice(context.Background(), Mutation{}, device.ID)
			addTestQueue(t, c,
				testMedia("a.mp3", mediamodel.MediaKindAudio),
				testMedia("b.mp3", mediamodel.MediaKindAudio),
			)
			queued, _ := c.Snapshot(context.Background())
			c.SetPolicy(context.Background(), PolicyRequest{Policy: Policy{AutoPlayNext: true, ImageDurationSeconds: 10}})
			if result := c.Play(context.Background(), PlayRequest{QueueItemID: queued.Queue[0].ID}); !result.OK() {
				t.Fatal(result)
			}
			playing, _ := c.Snapshot(context.Background())
			pauseResult := make(chan Result, 1)
			go func() { pauseResult <- c.Pause(context.Background(), Mutation{}) }()
			deadline := time.Now().Add(time.Second)
			for !slices.Contains(log.snapshot(), "pause:renderer") && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if !slices.Contains(log.snapshot(), "pause:renderer") {
				t.Fatal("pause did not start")
			}
			c.HandleMonitorEvent(context.Background(), playback.MonitorEvent{Generation: playing.Generation, Terminal: playback.TerminalFinished})
			for !controllerHasDeferredMonitor(t, c) && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if !controllerHasDeferredMonitor(t, c) {
				t.Fatal("finish was not deferred")
			}
			close(factory.pauseBlock)
			if result := <-pauseResult; !result.OK() {
				t.Fatal(result)
			}
			after := awaitAutoplaySnapshot(t, c, func(snapshot Snapshot) bool {
				return snapshot.Generation > playing.Generation && snapshot.HasSession && snapshot.Queue[1].IsActive
			})
			if after.PlaybackState != "PLAYING" || !after.Queue[1].IsSelected {
				t.Fatalf("deferred transition = %#v", after)
			}
		})
	}
}

func TestDeferredFinishKeepsChromecastStatusLoss(t *testing.T) {
	device := playback.Device{ID: "cast", Protocol: "Chromecast"}
	c, log, factory := newTestController(device)
	defer c.Close()
	factory.pauseBlock = make(chan struct{})
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, device.ID)
	addTestQueue(t, c,
		testMedia("a.mp3", mediamodel.MediaKindAudio),
		testMedia("b.mp3", mediamodel.MediaKindAudio),
	)
	queued, _ := c.Snapshot(context.Background())
	c.SetPolicy(context.Background(), PolicyRequest{Policy: Policy{AutoPlayNext: true, ImageDurationSeconds: 10}})
	if result := c.Play(context.Background(), PlayRequest{QueueItemID: queued.Queue[0].ID}); !result.OK() {
		t.Fatal(result)
	}
	playing, _ := c.Snapshot(context.Background())
	pauseResult := make(chan Result, 1)
	go func() { pauseResult <- c.Pause(context.Background(), Mutation{}) }()
	deadline := time.Now().Add(time.Second)
	for !slices.Contains(log.snapshot(), "pause:cast") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !slices.Contains(log.snapshot(), "pause:cast") {
		t.Fatal("pause did not start")
	}
	lost := errors.New("status lost")
	handleControllerMonitor(t, c, playback.MonitorEvent{Generation: playing.Generation, Terminal: playback.TerminalFinished, Err: lost})
	handleControllerMonitor(t, c, playback.MonitorEvent{Generation: playing.Generation, Terminal: playback.TerminalError})
	close(factory.pauseBlock)
	select {
	case result := <-pauseResult:
		if !result.OK() {
			t.Fatal(result)
		}
	case <-time.After(time.Second):
		t.Fatal("pause did not finish")
	}
	awaitAutoplaySnapshot(t, c, func(snapshot Snapshot) bool {
		return snapshot.Generation > playing.Generation && snapshot.HasSession && snapshot.Queue[1].IsActive
	})
	events := log.snapshot()
	if count(events, "open:cast") != 2 || count(events, "load-existing:cast") != 0 {
		t.Fatalf("dead deferred connection reused: %v", events)
	}
}

func TestTerminalCleanupFencesReplay(t *testing.T) {
	device := playback.Device{ID: "renderer", Protocol: "DLNA"}
	c, _, factory := newTestController(device)
	defer c.Close()
	factory.stopBlock = make(chan struct{})
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, device.ID)
	c.SelectMedia(context.Background(), Mutation{}, testMedia("a.mp3", mediamodel.MediaKindAudio))
	if result := c.Play(context.Background(), PlayRequest{}); !result.OK() {
		t.Fatal(result)
	}
	playing, _ := c.Snapshot(context.Background())
	handleControllerMonitor(t, c, playback.MonitorEvent{Generation: playing.Generation, Terminal: playback.TerminalFinished})
	stopping, _ := c.Snapshot(context.Background())
	if stopping.PlaybackState != "STOPPING" || stopping.HasSession {
		t.Fatalf("cleanup state = %#v", stopping)
	}
	if result := c.Stop(context.Background(), Mutation{}); result.Code != CodeBusy {
		t.Fatalf("stop during cleanup = %#v", result)
	}
	if result := c.Play(context.Background(), PlayRequest{}); result.Code != CodeBusy {
		t.Fatalf("replay during cleanup = %#v", result)
	}
	close(factory.stopBlock)
	awaitAutoplaySnapshot(t, c, func(snapshot Snapshot) bool {
		return snapshot.PlaybackState == "STOPPED"
	})
	if result := c.Play(context.Background(), PlayRequest{}); !result.OK() {
		t.Fatal(result)
	}
}

func TestReplacementIgnoresCanceledSessionTerminal(t *testing.T) {
	device := playback.Device{ID: "renderer", Protocol: "DLNA"}
	log := &eventLog{}
	factory := &fakeFactory{log: log}
	monitorStarted := make(chan uint64, 2)
	monitorStopped := make(chan uint64, 2)
	c := New(Config{
		Discovery:        newFakeDiscovery(device),
		TransportFactory: factory,
		MediaServer:      &fakeServer{log: log},
		OperationTimeout: time.Second,
		RunMonitor: func(ctx context.Context, cfg playback.MonitorConfig, _ playback.Device, _ Transport) {
			generation, sink := cfg.Generation, cfg.Sink
			monitorStarted <- generation
			<-ctx.Done()
			sink.HandleMonitorEvent(ctx, playback.MonitorEvent{Generation: generation, Terminal: playback.TerminalFinished})
			monitorStopped <- generation
		},
	})
	defer c.Close()
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, device.ID)
	addTestQueue(t, c,
		testMedia("a.mp3", mediamodel.MediaKindAudio),
		testMedia("b.mp3", mediamodel.MediaKindAudio),
	)
	queued, _ := c.Snapshot(context.Background())
	if result := c.Play(context.Background(), PlayRequest{QueueItemID: queued.Queue[0].ID}); !result.OK() {
		t.Fatal(result)
	}
	select {
	case <-monitorStarted:
	case <-time.After(time.Second):
		t.Fatal("first monitor not started")
	}
	if result := c.Play(context.Background(), PlayRequest{QueueItemID: queued.Queue[1].ID}); !result.OK() {
		t.Fatal(result)
	}
	select {
	case generation := <-monitorStopped:
		if generation != 1 {
			t.Fatalf("stopped generation = %d", generation)
		}
	case <-time.After(time.Second):
		t.Fatal("old monitor did not stop")
	}
	for range 20 {
		after, _ := c.Snapshot(context.Background())
		if !after.HasSession || !after.Queue[1].IsActive || after.PlaybackState != "PLAYING" || count(log.snapshot(), "load:renderer") != 2 {
			t.Fatalf("replacement corrupted: %#v %v", after, log.snapshot())
		}
		time.Sleep(time.Millisecond)
	}
}

func controllerHasDeferredMonitor(t *testing.T, c *Controller) bool {
	t.Helper()
	var deferred bool
	done := make(chan struct{})
	if err := c.enqueue(message{done: done, fn: func(s *actorState) { deferred = s.deferred != nil }}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		return deferred
	case <-time.After(time.Second):
		t.Fatal("controller barrier timeout")
		return false
	}
}

func controllerImageTimerArmed(t *testing.T, c *Controller) bool {
	t.Helper()
	var armed bool
	done := make(chan struct{})
	if err := c.enqueue(message{done: done, fn: func(s *actorState) {
		armed = s.active != nil && s.active.imageTimer != nil
	}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		return armed
	case <-time.After(time.Second):
		t.Fatal("controller barrier timeout")
		return false
	}
}

func controllerImageEpoch(t *testing.T, c *Controller) uint64 {
	t.Helper()
	var epoch uint64
	done := make(chan struct{})
	if err := c.enqueue(message{done: done, fn: func(s *actorState) {
		if s.active != nil {
			epoch = s.active.imageEpoch
		}
	}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		return epoch
	case <-time.After(time.Second):
		t.Fatal("controller barrier timeout")
		return 0
	}
}

func handleControllerMonitor(t *testing.T, c *Controller, event playback.MonitorEvent) {
	t.Helper()
	done := make(chan struct{})
	if err := c.enqueue(message{done: done, fn: func(s *actorState) { s.monitor(event) }}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("controller barrier timeout")
	}
}

func TestChromecastAutoplayUsesPerItemTranscodePolicy(t *testing.T) {
	tests := []struct {
		name      string
		kind      mediamodel.MediaKind
		file      string
		mediaType string
		seekable  bool
	}{
		{name: "audio direct", kind: mediamodel.MediaKindAudio, file: "next.mp3", mediaType: "audio/*", seekable: true},
		{name: "image direct", kind: mediamodel.MediaKindImage, file: "next.jpg", mediaType: "image/*", seekable: true},
		{name: "video transcodes", kind: mediamodel.MediaKindVideo, file: "next.mkv", mediaType: "video/mp4", seekable: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			device := playback.Device{ID: "cast", Protocol: "Chromecast"}
			c, _, factory := newTestController(device)
			defer c.Close()
			awaitDevices(t, c, 1)
			c.SelectDevice(context.Background(), Mutation{}, device.ID)
			addTestQueue(t, c,
				testMedia("first.mkv", mediamodel.MediaKindVideo),
				testMedia(tt.file, tt.kind),
			)
			queued, _ := c.Snapshot(context.Background())
			c.SetTranscode(context.Background(), Mutation{}, true)
			c.SetPolicy(context.Background(), PolicyRequest{Policy: Policy{AutoPlayNext: true, ImageDurationSeconds: 10}})
			if result := c.Play(context.Background(), PlayRequest{QueueItemID: queued.Queue[0].ID}); !result.OK() {
				t.Fatal(result)
			}
			playing, _ := c.Snapshot(context.Background())
			c.HandleMonitorEvent(context.Background(), playback.MonitorEvent{Generation: playing.Generation, Terminal: playback.TerminalFinished})
			awaitAutoplaySnapshot(t, c, func(snapshot Snapshot) bool {
				return snapshot.Generation > playing.Generation && snapshot.HasSession && snapshot.Queue[1].IsActive
			})
			factory.mu.Lock()
			load := factory.opened[0].load
			factory.mu.Unlock()
			if load.MediaType != tt.mediaType || load.Seekable != tt.seekable {
				t.Fatalf("next load = %#v", load)
			}
		})
	}
}

func TestChromecastTranscodeDurationProbedPerQueueItem(t *testing.T) {
	device := playback.Device{ID: "cast", Protocol: "Chromecast"}
	log := &eventLog{}
	factory := &fakeFactory{log: log}
	var probeMu sync.Mutex
	probeCount := 0
	monitorConfigs := make(chan playback.MonitorConfig, 2)
	c := New(Config{
		Discovery:        newFakeDiscovery(device),
		TransportFactory: factory,
		MediaServer:      &fakeServer{log: log},
		OperationTimeout: time.Second,
		DurationProbe: func(context.Context, playback.SourceOpener) (float64, error) {
			probeMu.Lock()
			defer probeMu.Unlock()
			probeCount++
			return 100.25 + float64(probeCount), nil
		},
		RunMonitor: func(_ context.Context, cfg playback.MonitorConfig, _ playback.Device, _ Transport) {
			monitorConfigs <- cfg
		},
	})
	defer c.Close()
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, device.ID)
	addTestQueue(t, c,
		testMedia("first.mkv", mediamodel.MediaKindVideo),
		testMedia("second.mkv", mediamodel.MediaKindVideo),
	)
	queued, _ := c.Snapshot(context.Background())
	c.SetTranscode(context.Background(), Mutation{}, true)
	c.SetPolicy(context.Background(), PolicyRequest{Policy: Policy{AutoPlayNext: true, ImageDurationSeconds: 10}})
	if result := c.Play(context.Background(), PlayRequest{QueueItemID: queued.Queue[0].ID}); !result.OK() {
		t.Fatal(result)
	}
	factory.mu.Lock()
	firstDuration := factory.opened[0].load.Duration
	factory.mu.Unlock()
	if firstDuration != 101.25 {
		t.Fatalf("first duration = %v", firstDuration)
	}
	if cfg := <-monitorConfigs; cfg.ExpectedDuration != 101 || cfg.SeekOffset != 0 {
		t.Fatalf("first monitor = %#v", cfg)
	}
	playing, _ := c.Snapshot(context.Background())
	c.HandleMonitorEvent(context.Background(), playback.MonitorEvent{Generation: playing.Generation, Terminal: playback.TerminalFinished})
	awaitAutoplaySnapshot(t, c, func(snapshot Snapshot) bool {
		return snapshot.Generation > playing.Generation && snapshot.HasSession && snapshot.PlaybackState == "PLAYING" && snapshot.Queue[1].IsActive
	})
	factory.mu.Lock()
	secondDuration := factory.opened[0].load.Duration
	factory.mu.Unlock()
	probeMu.Lock()
	probes := probeCount
	probeMu.Unlock()
	if secondDuration != 102.25 || probes != 2 {
		t.Fatalf("second duration/probes = %v/%d", secondDuration, probes)
	}
	if cfg := <-monitorConfigs; cfg.ExpectedDuration != 102 || cfg.SeekOffset != 0 {
		t.Fatalf("second monitor = %#v", cfg)
	}
}

func TestImageAutoplayTimerFollowsPolicy(t *testing.T) {
	device := playback.Device{ID: "image", Protocol: "DLNA"}
	log := &eventLog{}
	factory := &fakeFactory{log: log}
	clock := newAutoplayClock()
	c := New(Config{
		Discovery:        newFakeDiscovery(device),
		TransportFactory: factory,
		MediaServer:      &fakeServer{log: log},
		Clock:            clock,
		OperationTimeout: time.Second,
	})
	defer c.Close()
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, device.ID)
	addTestQueue(t, c,
		testMedia("a.jpg", mediamodel.MediaKindImage),
		testMedia("b.jpg", mediamodel.MediaKindImage),
	)
	queued, _ := c.Snapshot(context.Background())
	if result := c.SetPolicy(context.Background(), PolicyRequest{Policy: Policy{ImageDurationSeconds: 5}}); !result.OK() {
		t.Fatal(result)
	}
	if result := c.Play(context.Background(), PlayRequest{QueueItemID: queued.Queue[0].ID}); !result.OK() {
		t.Fatal(result)
	}
	if controllerImageTimerArmed(t, c) {
		t.Fatal("image timer armed with autoplay off")
	}

	if result := c.SetPolicy(context.Background(), PolicyRequest{Policy: Policy{AutoPlayNext: true, ImageDurationSeconds: 5}}); !result.OK() {
		t.Fatal(result)
	}
	firstTimer := waitAutoplayTimer(t, clock)
	firstEpoch := controllerImageEpoch(t, c)
	if result := c.SetPolicy(context.Background(), PolicyRequest{Policy: Policy{AutoPlayNext: true, AutoPlaySameType: true, ImageDurationSeconds: 5}}); !result.OK() {
		t.Fatal(result)
	}
	select {
	case <-firstTimer.stopped:
		t.Fatal("unrelated policy change restarted image timer")
	default:
	}
	select {
	case <-clock.timers:
		t.Fatal("unrelated policy change created image timer")
	default:
	}
	if result := c.SetPolicy(context.Background(), PolicyRequest{Policy: Policy{ImageDurationSeconds: 5}}); !result.OK() {
		t.Fatal(result)
	}
	select {
	case <-firstTimer.stopped:
	case <-time.After(time.Second):
		t.Fatal("image timer not canceled")
	}
	playing, _ := c.Snapshot(context.Background())
	handleControllerMonitor(t, c, playback.MonitorEvent{Generation: playing.Generation, TimerEpoch: firstEpoch, Terminal: playback.TerminalFinished})
	stillPlaying, _ := c.Snapshot(context.Background())
	if !stillPlaying.HasSession || !stillPlaying.Queue[0].IsActive {
		t.Fatalf("stale timer advanced: %#v", stillPlaying)
	}
	firstTimer.ch <- time.Time{}
	stillPlaying, _ = c.Snapshot(context.Background())
	if !stillPlaying.HasSession || !stillPlaying.Queue[0].IsActive {
		t.Fatalf("disabled timer advanced: %#v", stillPlaying)
	}

	if result := c.SetPolicy(context.Background(), PolicyRequest{Policy: Policy{AutoPlayNext: true, ImageDurationSeconds: 5}}); !result.OK() {
		t.Fatal(result)
	}
	secondTimer := waitAutoplayTimer(t, clock)
	secondTimer.ch <- time.Time{}
	after := awaitAutoplaySnapshot(t, c, func(snapshot Snapshot) bool {
		return snapshot.HasSession && snapshot.Queue[1].IsActive
	})
	if !after.Queue[1].IsSelected {
		t.Fatalf("image transition = %#v", after.Queue)
	}
}

func TestChromecastImageTimerWaitsForRenderer(t *testing.T) {
	device := playback.Device{ID: "image", Protocol: "Chromecast"}
	log := &eventLog{}
	clock := newAutoplayClock()
	c := New(Config{
		Discovery:        newFakeDiscovery(device),
		TransportFactory: &fakeFactory{log: log},
		MediaServer:      &fakeServer{log: log},
		Clock:            clock,
		OperationTimeout: time.Second,
	})
	defer c.Close()
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, device.ID)
	addTestQueue(t, c,
		testMedia("a.jpg", mediamodel.MediaKindImage),
		testMedia("b.mp3", mediamodel.MediaKindAudio),
	)
	queued, _ := c.Snapshot(context.Background())
	c.SetPolicy(context.Background(), PolicyRequest{Policy: Policy{AutoPlayNext: true, ImageDurationSeconds: 5}})
	if result := c.Play(context.Background(), PlayRequest{QueueItemID: queued.Queue[0].ID}); !result.OK() {
		t.Fatal(result)
	}
	playing, _ := c.Snapshot(context.Background())
	if controllerImageTimerArmed(t, c) {
		t.Fatal("image timer armed before renderer status")
	}
	handleControllerMonitor(t, c, playback.MonitorEvent{Generation: playing.Generation, State: "BUFFERING"})
	if controllerImageTimerArmed(t, c) {
		t.Fatal("bare buffering armed image timer")
	}
	handleControllerMonitor(t, c, playback.MonitorEvent{Generation: playing.Generation, State: "BUFFERING", ImageReady: true})
	if !controllerImageTimerArmed(t, c) {
		t.Fatal("ready image timer not armed")
	}
	_ = waitAutoplayTimer(t, clock)
}
