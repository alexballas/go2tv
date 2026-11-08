package rtmp

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestServerStopReapsProcessWithoutCallerWait(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	ffmpeg := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\nexec sleep 300\n"), 0700); err != nil {
		t.Fatal(err)
	}
	s := NewServer()
	if _, err := s.Start(ffmpeg, "regression", "0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	s.Stop()
	if s.run.cmd.ProcessState == nil {
		t.Fatal("Stop returned before reaping FFmpeg")
	}
	if _, err := os.Stat(s.TempDir()); !os.IsNotExist(err) {
		t.Fatalf("Stop retained temporary files: %v", err)
	}
}

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
