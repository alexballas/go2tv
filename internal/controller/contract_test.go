package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/internal/playback"
)

func TestErrorContract(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code ErrorCode
	}{
		{name: "busy", err: ErrBusy, code: CodeBusy},
		{name: "conflict wrapped", err: fmt.Errorf("write: %w", ErrConflict), code: CodeConflict},
		{name: "invalid media", err: ErrInvalidMedia, code: CodeInvalid},
		{name: "canceled", err: context.Canceled, code: CodeCanceled},
		{name: "deadline", err: context.DeadlineExceeded, code: CodeDeadline},
		{name: "unknown", err: errors.New("adapter failed"), code: CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ErrorCodeOf(tt.err); got != tt.code {
				t.Fatalf("code = %q, want %q", got, tt.code)
			}
		})
	}

	result := fail("request", 7, ErrConflict)
	if result.RequestID != "request" || result.Revision != 7 || result.Code != CodeConflict {
		t.Fatalf("result = %#v", result)
	}
	if err := result.Err(); !errors.Is(err, ErrConflict) || ErrorCodeOf(err) != CodeConflict {
		t.Fatalf("result error = %v", err)
	}
	if err := (Result{}).Err(); err != nil {
		t.Fatalf("successful result error = %v", err)
	}
}

func TestLegacyJSONFieldNamesRemainStable(t *testing.T) {
	policy, err := json.Marshal(Policy{AutoPlayNext: true, ImageDurationSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	wantPolicy := `{"LoopSelected":false,"AutoPlayNext":true,"AutoPlaySameType":false,"GaplessEnabled":false,"ImageDurationSeconds":10}`
	if string(policy) != wantPolicy {
		t.Fatalf("policy JSON = %s", policy)
	}
	result, err := json.Marshal(Result{RequestID: "id", Revision: 3, Code: CodeBusy, Message: "operation busy"})
	if err != nil {
		t.Fatal(err)
	}
	wantResult := `{"RequestID":"id","Revision":3,"Code":"busy","Message":"operation busy"}`
	if string(result) != wantResult {
		t.Fatalf("result JSON = %s", result)
	}
}

func TestInputValidationContracts(t *testing.T) {
	valid := testMedia("song.mp3", mediamodel.MediaKindAudio)
	valid.MIMEType = "audio/mpeg"
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid media: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*MediaRef)
	}{
		{name: "blank root", mutate: func(media *MediaRef) { media.RootID = " " }},
		{name: "unknown kind", mutate: func(media *MediaRef) { media.Kind = mediamodel.MediaKindUnknown }},
		{name: "invalid MIME", mutate: func(media *MediaRef) { media.MIMEType = "not a mime" }},
		{name: "missing opener", mutate: func(media *MediaRef) { media.OpenDirect = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			media := valid
			tt.mutate(&media)
			if err := media.Validate(); !errors.Is(err, ErrInvalidMedia) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if err := (SubtitleRef{}).Validate(); err != nil {
		t.Fatalf("zero subtitle: %v", err)
	}
	if err := (SubtitleRef{ID: "subtitle"}).Validate(); !errors.Is(err, ErrInvalidSubtitle) {
		t.Fatalf("partial subtitle: %v", err)
	}
	if err := (Policy{}).Validate(); err != nil {
		t.Fatalf("zero policy: %v", err)
	}
	if err := (Policy{GaplessEnabled: true}).Validate(); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("gapless without autoplay: %v", err)
	}
	if err := (Config{OperationTimeout: -time.Second}).Validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("negative timeout: %v", err)
	}
	if err := (Config{TransportFactory: &fakeFactory{log: &eventLog{}}}).Validate(); err != nil {
		t.Fatalf("factory-only config: %v", err)
	}
}

func TestArtworkCacheZeroValueAndCopyOwnership(t *testing.T) {
	var cache ArtworkCache
	source := []byte("abc")
	value, err := cache.Get(context.Background(), "cover", func(context.Context) ([]byte, string, error) {
		return source, "image/jpeg", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	source[0] = 'x'
	value.Data[1] = 'x'
	cached, ok := cache.Lookup("cover")
	if !ok || string(cached.Data) != "abc" {
		t.Fatalf("cached value = %q, found=%v", cached.Data, ok)
	}
	if _, err := cache.Get(context.Background(), "invalid", func(context.Context) ([]byte, string, error) {
		return []byte("data"), "text/plain", nil
	}); !errors.Is(err, ErrInvalidArtwork) {
		t.Fatalf("invalid artwork error = %v", err)
	}
}

func TestCanceledPlayDoesNotStart(t *testing.T) {
	c, log, _ := newTestController(playback.Device{ID: "one", Protocol: "DLNA"})
	defer c.Close()
	awaitDevices(t, c, 1)
	if result := c.SelectDevice(context.Background(), Mutation{}, "one"); !result.OK() {
		t.Fatal(result)
	}
	if result := c.SelectMedia(context.Background(), Mutation{}, testMedia("song.mp3", mediamodel.MediaKindAudio)); !result.OK() {
		t.Fatal(result)
	}
	before, _ := c.Snapshot(context.Background())
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	result := c.Play(ctx, PlayRequest{Mutation: Mutation{RequestID: "expired"}})
	if result.Code != CodeDeadline || result.RequestID != "expired" {
		t.Fatalf("result = %#v", result)
	}
	after, _ := c.Snapshot(context.Background())
	if after.Revision != before.Revision || after.Generation != before.Generation || after.HasSession || slices.Contains(log.snapshot(), "open:one") {
		t.Fatalf("canceled play changed state: before=%#v after=%#v log=%v", before, after, log.snapshot())
	}
}

type nilTransportFactory struct{}

func (nilTransportFactory) Open(context.Context, playback.Device) (Transport, error) { return nil, nil }

func TestMissingAndInvalidAdaptersFailSafely(t *testing.T) {
	device := playback.Device{ID: "one", Protocol: "DLNA"}
	c := New(Config{Discovery: newFakeDiscovery(device)})
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, device.ID)
	if result := c.SetVolume(context.Background(), Mutation{}, 20); result.Code != CodeInvalid {
		c.Close()
		t.Fatalf("missing factory result = %#v", result)
	}
	c.Close()

	events := &eventLog{}
	c = New(Config{
		Discovery:        newFakeDiscovery(device),
		TransportFactory: nilTransportFactory{},
		MediaServer:      &fakeServer{log: events},
	})
	defer c.Close()
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, device.ID)
	c.SelectMedia(context.Background(), Mutation{}, testMedia("song.mp3", mediamodel.MediaKindAudio))
	if result := c.Play(context.Background(), PlayRequest{}); result.Code != CodeInternal {
		t.Fatalf("nil transport result = %#v", result)
	}
	snapshot, err := c.Snapshot(context.Background())
	if err != nil || snapshot.HasSession || snapshot.PlaybackState != PlaybackStateStopped {
		t.Fatalf("snapshot = %#v, err=%v", snapshot, err)
	}
}

func TestShutdownDeadlineAndOwnedMonitor(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	events := &eventLog{}
	c := New(Config{
		Discovery:        newFakeDiscovery(playback.Device{ID: "one", Protocol: "DLNA"}),
		TransportFactory: &fakeFactory{log: events},
		MediaServer:      &fakeServer{log: events},
		OperationTimeout: time.Second,
		RunMonitor: func(context.Context, playback.MonitorConfig, playback.Device, Transport) {
			close(started)
			<-release
		},
	})
	awaitDevices(t, c, 1)
	c.SelectDevice(context.Background(), Mutation{}, "one")
	c.SelectMedia(context.Background(), Mutation{}, testMedia("song.mp3", mediamodel.MediaKindAudio))
	if result := c.Play(context.Background(), PlayRequest{}); !result.OK() {
		t.Fatal(result)
	}
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := c.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		close(release)
		t.Fatalf("shutdown error = %v", err)
	}
	select {
	case <-c.Done():
		close(release)
		t.Fatal("shutdown ignored owned monitor")
	default:
	}
	close(release)
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if result := c.SetTranscode(context.Background(), Mutation{}, true); result.Code != CodeClosed {
		t.Fatalf("post-shutdown result = %#v", result)
	}
	log := events.snapshot()
	if !slices.Contains(log, "stop:one") || !slices.Contains(log, "close:one") {
		t.Fatalf("shutdown did not release transport: %v", log)
	}
}

func TestParentContextOwnsControllerLifetime(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	c := New(Config{ParentContext: parent})
	cancel()
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not stop controller")
	}
	if _, err := c.Snapshot(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("snapshot error = %v", err)
	}
}
