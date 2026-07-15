package controller

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/internal/playback"
)

type controllerEventLogger struct {
	mu       sync.Mutex
	messages []string
}

func (l *controllerEventLogger) Debug(string) {}
func (l *controllerEventLogger) Info(message string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.messages = append(l.messages, message)
}
func (l *controllerEventLogger) Warning(string) {}
func (l *controllerEventLogger) Error(message string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.messages = append(l.messages, message)
}
func (l *controllerEventLogger) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.messages)
}

func newLoggingController(log EventLogger) *Controller {
	events := &eventLog{}
	return New(Config{
		Discovery:        newFakeDiscovery(playback.Device{ID: "one", Protocol: "DLNA"}),
		TransportFactory: &fakeFactory{log: events},
		MediaServer:      &fakeServer{log: events},
		OperationTimeout: time.Second,
		Logger:           log,
	})
}

func startLoggedPlayback(t *testing.T, c *Controller) Snapshot {
	t.Helper()
	awaitDevices(t, c, 1)
	if result := c.SelectDevice(context.Background(), Mutation{}, "one"); !result.OK() {
		t.Fatal(result)
	}
	if result := c.SelectMedia(context.Background(), Mutation{}, testMedia("example.mp4", mediamodel.MediaKindVideo)); !result.OK() {
		t.Fatal(result)
	}
	if result := c.Play(context.Background(), PlayRequest{}); !result.OK() {
		t.Fatal(result)
	}
	snapshot, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestPlaybackEventLogging(t *testing.T) {
	log := &controllerEventLogger{}
	c := newLoggingController(log)
	defer c.Close()
	playing := startLoggedPlayback(t, c)
	c.HandleMonitorEvent(context.Background(), playback.MonitorEvent{Generation: playing.Generation, Position: 6, Duration: 120})
	deadline := time.Now().Add(time.Second)
	for {
		snapshot, _ := c.Snapshot(context.Background())
		if snapshot.Position == 6 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("position update missing")
		}
		time.Sleep(time.Millisecond)
	}
	if result := c.Pause(context.Background(), Mutation{}); !result.OK() {
		t.Fatal(result)
	}
	if result := c.Resume(context.Background(), Mutation{}); !result.OK() {
		t.Fatal(result)
	}
	if result := c.Seek(context.Background(), SeekRequest{Seconds: 85}); !result.OK() {
		t.Fatal(result)
	}
	c.HandleMonitorEvent(context.Background(), playback.MonitorEvent{Generation: playing.Generation + 1, Terminal: playback.TerminalFinished})

	want := []string{
		"Playback started: example.mp4",
		"Playback paused at 00:06",
		"Playback resumed at 00:06",
		"Seeked to 01:25 in example.mp4",
		"Playback completed",
	}
	deadline = time.Now().Add(time.Second)
	for {
		messages := log.snapshot()
		if slices.Equal(messages, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("messages=%v, want=%v", messages, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPlaybackStopLogging(t *testing.T) {
	log := &controllerEventLogger{}
	c := newLoggingController(log)
	defer c.Close()
	startLoggedPlayback(t, c)
	if result := c.Stop(context.Background(), Mutation{}); !result.OK() {
		t.Fatal(result)
	}
	if messages := log.snapshot(); !slices.Contains(messages, "Playback stopped") {
		t.Fatalf("messages=%v", messages)
	}
}
