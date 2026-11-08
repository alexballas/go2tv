package interactive

import (
	"strconv"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"

	"go2tv.app/go2tv/v2/internal/playback"
)

// skipSettle groups rapid key presses into one seek, so a transcoded stream
// restarts once instead of once per press.
const skipSettle = 400 * time.Millisecond

const skipHelp = `"←" "→" (Back 10s / Forward 30s)`

// skipper accumulates relative skips and runs them one at a time.
type skipper struct {
	mu    sync.Mutex
	run   sync.Mutex
	delta int
	timer *time.Timer
	seek  func(delta int)
}

func (s *skipper) add(delta int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delta += delta
	if s.timer != nil {
		s.timer.Reset(skipSettle)
		return
	}
	s.timer = time.AfterFunc(skipSettle, s.fire)
}

func (s *skipper) pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.delta
}

func (s *skipper) fire() {
	s.mu.Lock()
	delta := s.delta
	s.delta, s.timer = 0, nil
	s.mu.Unlock()
	if delta == 0 {
		return
	}
	s.run.Lock()
	defer s.run.Unlock()
	s.seek(delta)
}

// skipDelta maps arrow keys to the shared skip steps.
func skipDelta(key tcell.Key) (int, bool) {
	switch key {
	case tcell.KeyLeft:
		return -playback.SkipBackSeconds, true
	case tcell.KeyRight:
		return playback.SkipForwardSeconds, true
	}
	return 0, false
}

func formatSkip(delta int) string {
	if delta > 0 {
		return "Skip +" + strconv.Itoa(delta) + "s"
	}
	return "Skip " + strconv.Itoa(delta) + "s"
}

// notice is a short-lived status line, e.g. a skip result.
type notice struct {
	mu    sync.Mutex
	text  string
	until time.Time
}

func (n *notice) set(text string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.text, n.until = text, time.Now().Add(3*time.Second)
}

func (n *notice) get() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if time.Now().After(n.until) {
		return ""
	}
	return n.text
}
