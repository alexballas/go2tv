package playback

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type seekDLNA struct {
	calls  []string
	seek   string
	load   LoadRequest
	pos    Position
	posErr error
}

func (m *seekDLNA) Load(_ context.Context, r LoadRequest) error {
	m.calls = append(m.calls, "load")
	m.load = r
	return nil
}
func (m *seekDLNA) Play(context.Context) error  { m.calls = append(m.calls, "play"); return nil }
func (m *seekDLNA) Pause(context.Context) error { return nil }
func (m *seekDLNA) Stop(context.Context) error  { m.calls = append(m.calls, "stop"); return nil }
func (m *seekDLNA) Seek(_ context.Context, s string) error {
	m.calls = append(m.calls, "seek")
	m.seek = s
	return nil
}
func (m *seekDLNA) Position(context.Context) (Position, error)       { return m.pos, m.posErr }
func (m *seekDLNA) State(context.Context) (string, error)            { return "", nil }
func (m *seekDLNA) SetNextURI(context.Context, string, string) error { return nil }
func (m *seekDLNA) ClearNextURI(context.Context) error               { return nil }
func (m *seekDLNA) Resubscribe(context.Context, string) error        { return nil }
func (m *seekDLNA) Unsubscribe(context.Context, string) error        { return nil }

type seekCast struct {
	calls       []string
	seconds     int
	load        LoadRequest
	statuses    []CastStatus
	statusErr   error
	statusIndex int
}

func (m *seekCast) Connect(context.Context) error           { return nil }
func (m *seekCast) Close(context.Context) error             { return nil }
func (m *seekCast) Load(context.Context, LoadRequest) error { return nil }
func (m *seekCast) Play(context.Context) error              { return nil }
func (m *seekCast) Pause(context.Context) error             { return nil }
func (m *seekCast) Stop(context.Context) error              { return nil }
func (m *seekCast) Seek(_ context.Context, s int) error {
	m.calls = append(m.calls, "seek")
	m.seconds = s
	return nil
}
func (m *seekCast) Status(context.Context) (CastStatus, error) {
	if m.statusErr != nil {
		return CastStatus{}, m.statusErr
	}
	if len(m.statuses) == 0 {
		return CastStatus{}, nil
	}
	i := m.statusIndex
	if i >= len(m.statuses) {
		i = len(m.statuses) - 1
	} else {
		m.statusIndex++
	}
	return m.statuses[i], nil
}
func (m *seekCast) LoadOnExisting(_ context.Context, r LoadRequest) error {
	m.calls = append(m.calls, "load-existing")
	m.load = r
	return nil
}

type seekServer struct {
	calls   []string
	request ServerRequest
}

func (m *seekServer) Start(_ context.Context, r ServerRequest) (MediaRoute, error) {
	m.calls = append(m.calls, "start")
	m.request = r
	return MediaRoute{URL: "http://route"}, nil
}
func (m *seekServer) Stop(context.Context) error                            { m.calls = append(m.calls, "stop"); return nil }
func (m *seekServer) Add(context.Context, RouteRequest) (MediaRoute, error) { return MediaRoute{}, nil }
func (m *seekServer) Remove(context.Context, string) error                  { return nil }

func TestSeekEngineFourPaths(t *testing.T) {
	t.Run("direct DLNA", func(t *testing.T) {
		d := &seekDLNA{}
		e := NewSeekEngine(d, nil, nil)
		if _, err := e.Seek(context.Background(), SeekRequest{Protocol: "DLNA", Seconds: 65, Duration: 100}); err != nil {
			t.Fatal(err)
		}
		if d.seek != "00:01:05" {
			t.Fatalf("seek %q", d.seek)
		}
	})
	t.Run("transcoded DLNA", func(t *testing.T) {
		d, s := &seekDLNA{}, &seekServer{}
		e := NewSeekEngine(d, nil, s)
		if _, err := e.Seek(context.Background(), SeekRequest{Protocol: "DLNA", Transcoded: true, Seconds: 20, Duration: 100}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(d.calls, []string{"stop", "load", "play"}) {
			t.Fatalf("calls %v", d.calls)
		}
		if s.request.SeekOffset != 20 || d.load.MediaURL != "http://route" {
			t.Fatalf("restart values %#v %#v", s.request, d.load)
		}
	})
	t.Run("direct Chromecast", func(t *testing.T) {
		c := &seekCast{}
		e := NewSeekEngine(nil, c, nil)
		if _, err := e.Seek(context.Background(), SeekRequest{Protocol: "Chromecast", Seconds: 20, Duration: 100}); err != nil {
			t.Fatal(err)
		}
		if c.seconds != 20 {
			t.Fatalf("seek %d", c.seconds)
		}
	})
	t.Run("transcoded Chromecast", func(t *testing.T) {
		c, s := &seekCast{}, &seekServer{}
		e := NewSeekEngine(nil, c, s)
		if _, err := e.Seek(context.Background(), SeekRequest{Protocol: "Chromecast", Transcoded: true, Seconds: 20, Duration: 100}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(c.calls, []string{"load-existing"}) || s.request.SeekOffset != 20 {
			t.Fatalf("calls %v request %#v", c.calls, s.request)
		}
	})
}

func TestValidateSeek(t *testing.T) {
	if !errors.Is(ValidateSeek(-1, 10), ErrSeekNegative) {
		t.Fatal("negative")
	}
	if !errors.Is(ValidateSeek(11, 10), ErrSeekPastDuration) {
		t.Fatal("past duration")
	}
	if err := ValidateSeek(10, 10); err != nil {
		t.Fatal(err)
	}
}
