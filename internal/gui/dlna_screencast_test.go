//go:build !(android || ios)

package gui

import (
	"testing"
)

// TestScreencastAudioEnabled pins the GO2TV_DLNA_SCREENCAST_AUDIO contract.
// Audio is on unless the value parses to false, because the advertised
// AVC_TS_MP_HD_AAC_MULT5 profile promises an AAC track. A value that does not
// parse must not silently mute the stream.
func TestScreencastAudioEnabled(t *testing.T) {
	tt := []struct {
		name  string
		value string
		want  bool
	}{
		{"unset defaults to on", "", true},
		{"blank defaults to on", "   ", true},
		{"false disables", "false", false},
		{"zero disables", "0", false},
		{"uppercase FALSE disables", "FALSE", false},
		{"surrounding space still parses", "  false  ", false},
		{"true enables", "true", true},
		{"one enables", "1", true},
		{"unparsable keeps the default", "yes-please", true},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			if got := screencastAudioEnabled(tc.value); got != tc.want {
				t.Fatalf("screencastAudioEnabled(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

// TestScreencastSessionTypedNilSurvives covers the reason the wrapper carries
// nil guards at all. screen.screencastSession is an interface, so a nil
// *dlnaScreencastSession stored in it is NOT a nil interface, and
// stopScreencastSession's `if session != nil` guard therefore lets the call
// through. Every method has to survive that.
func TestScreencastSessionTypedNilSurvives(t *testing.T) {
	var concrete *dlnaScreencastSession
	var session screencastSession = concrete

	if session == nil {
		t.Fatal("a typed nil in an interface should not compare equal to nil; the guards below are what protect the call")
	}

	if got := session.Done(); got != nil {
		t.Fatalf("Done() = %v, want nil", got)
	}
	if got := session.StderrTail(10); got != "" {
		t.Fatalf("StderrTail() = %q, want empty", got)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := concrete.Stream(); got != nil {
		t.Fatalf("Stream() = %v, want nil", got)
	}
}

// TestScreencastSessionNilInnerSession covers the other nil the guards handle:
// a wrapper that exists but whose underlying ts.Session never started.
func TestScreencastSessionNilInnerSession(t *testing.T) {
	s := &dlnaScreencastSession{}

	if got := s.Stream(); got != nil {
		t.Fatalf("Stream() = %v, want nil", got)
	}
	if got := s.Done(); got != nil {
		t.Fatalf("Done() = %v, want nil", got)
	}
	if got := s.StderrTail(10); got != "" {
		t.Fatalf("StderrTail() = %q, want empty", got)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
