package rtmp

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServerFailedStartReturnsAndCleansUp(t *testing.T) {
	s := NewServer()
	missing := filepath.Join(t.TempDir(), "missing-ffmpeg")
	result := make(chan error, 1)
	go func() {
		_, err := s.Start(missing, "regression", "0")
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("missing FFmpeg started RTMP server")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RTMP startup hung instead of reporting missing FFmpeg")
	}
	if _, err := os.Stat(s.TempDir()); !os.IsNotExist(err) {
		t.Fatalf("failed RTMP startup retained temporary files: %v", err)
	}
	// A failed start must also leave Stop usable.
	s.Stop()
}
