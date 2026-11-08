package interactive

import (
	"testing"
	"time"
)

func TestSkipperCombinesRapidPresses(t *testing.T) {
	seeks := make(chan int, 4)
	s := &skipper{seek: func(delta int) { seeks <- delta }}
	s.add(30)
	s.add(30)
	s.add(-10)
	if got := s.pending(); got != 50 {
		t.Fatalf("pending = %d, want 50", got)
	}
	select {
	case got := <-seeks:
		if got != 50 {
			t.Fatalf("seek delta = %d, want 50", got)
		}
	case <-time.After(5 * skipSettle):
		t.Fatal("skip never fired")
	}
	select {
	case extra := <-seeks:
		t.Fatalf("presses produced a second seek: %d", extra)
	case <-time.After(2 * skipSettle):
	}
}
