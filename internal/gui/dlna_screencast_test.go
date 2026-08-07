//go:build !(android || ios)

package gui

import (
	"testing"
)

// The thin dlnaScreencastSession wrapper must be safe on a nil receiver, since
// actions.go may hold one across a failed start.
func TestDLNAScreencastSessionNilSafety(t *testing.T) {
	var s *dlnaScreencastSession

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
