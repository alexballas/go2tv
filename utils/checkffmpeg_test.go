package utils

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestCheckFFmpegTimesOutHungExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	path := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec sleep 300\n"), 0700); err != nil {
		t.Fatal(err)
	}
	const timeout = 200 * time.Millisecond
	started := time.Now()
	err := checkFFmpeg(t.Context(), path, timeout)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("hung validation: %v, want deadline exceeded", err)
	}
	if time.Since(started) > timeout+2*time.Second {
		t.Fatal("validation did not reap the hung executable promptly")
	}
}
