package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type routeServer struct {
	added   RouteRequest
	removed string
}

func (s *routeServer) Start(context.Context, ServerRequest) (MediaRoute, error) {
	return MediaRoute{}, nil
}
func (s *routeServer) Stop(context.Context) error { return nil }
func (s *routeServer) Add(_ context.Context, r RouteRequest) (MediaRoute, error) {
	s.added = r
	return MediaRoute{URL: "http://sub", ID: r.ID}, nil
}
func (s *routeServer) Remove(_ context.Context, id string) error { s.removed = id; return nil }

func TestPrepareChromecastSubtitlePaths(t *testing.T) {
	dir := t.TempDir()
	srt := filepath.Join(dir, "sub.srt")
	if err := os.WriteFile(srt, []byte("1\n00:00:01,000 --> 00:00:02,000\nHi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &routeServer{}
	route, err := PrepareChromecastSubtitle(context.Background(), s, strings.NewReader(strings.Repeat("a", 32)), srt, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(s.added.Contents), "WEBVTT") || !strings.Contains(string(s.added.Contents), "01.000") {
		t.Fatalf("contents %q", s.added.Contents)
	}
	if err := route.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.removed == "" {
		t.Fatal("route not removed")
	}

	vtt := filepath.Join(dir, "sub.vtt")
	if err := os.WriteFile(vtt, []byte("WEBVTT\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareChromecastSubtitle(context.Background(), s, strings.NewReader(strings.Repeat("b", 32)), vtt, false); err != nil {
		t.Fatal(err)
	}
	burn, err := PrepareChromecastSubtitle(context.Background(), s, nil, srt, true)
	if err != nil || burn.BurnPath != srt || burn.URL != "" {
		t.Fatalf("burn %#v %v", burn, err)
	}
}

type gapQueue struct{ next GaplessItem }

func (q gapQueue) Next(string) (GaplessItem, bool) { return q.next, true }

type gapPolicy struct {
	protocol string
	autoplay bool
}

func (p gapPolicy) Protocol() string { return p.protocol }
func (p gapPolicy) Autoplay() bool   { return p.autoplay }

type gapSession struct {
	active   string
	promoted GaplessItem
}

func (s *gapSession) ActiveID() string      { return s.active }
func (s *gapSession) Promote(i GaplessItem) { s.promoted = i }

type gapDLNA struct {
	seekDLNA
	clearErr error
}

func (d *gapDLNA) ClearNextURI(context.Context) error { return d.clearErr }

func TestGaplessEnableDisableAndFailure(t *testing.T) {
	item := GaplessItem{ID: "next", MediaURL: "http://next"}
	d, session := &gapDLNA{}, &gapSession{active: "now"}
	e := NewGaplessEngine(gapQueue{item}, gapPolicy{"DLNA", true}, session, d, nil)
	if err := e.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !e.Promote() || session.promoted.ID != "next" {
		t.Fatalf("promoted %#v", session.promoted)
	}
	if err := e.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}

	bad := NewGaplessEngine(gapQueue{item}, gapPolicy{"Chromecast", true}, session, d, nil)
	if !errors.Is(bad.Enable(context.Background()), ErrGaplessUnavailable) {
		t.Fatal("Chromecast gapless enabled")
	}

	d.clearErr = errors.New("clear")
	e = NewGaplessEngine(gapQueue{item}, gapPolicy{"DLNA", true}, session, d, nil)
	if err := e.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.Disable(context.Background()); err == nil {
		t.Fatal("clear failure missing")
	}
	if len(d.calls) == 0 || d.calls[len(d.calls)-1] != "stop" {
		t.Fatalf("stop not called: %v", d.calls)
	}
}
