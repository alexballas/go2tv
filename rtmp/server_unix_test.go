//go:build !windows

package rtmp

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestServerStopReapsFFmpegBehindExitedWrapper(t *testing.T) {
	dir := t.TempDir()
	ffmpeg := filepath.Join(dir, "ffmpeg")
	// The wrapper exits at once; its FFmpeg keeps the stderr pipe Wait joins.
	script := "#!/bin/sh\nsleep 300 &\necho $! > \"${0%/*}/child\"\nexit 0\n"
	if err := os.WriteFile(ffmpeg, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	s := NewServer()
	if _, err := s.Start(ffmpeg, "regression", "0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	var child int
	deadline := time.Now().Add(3 * time.Second)
	for child == 0 {
		data, _ := os.ReadFile(filepath.Join(dir, "child"))
		child, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		if time.Now().After(deadline) {
			t.Fatal("wrapper did not start its FFmpeg")
		}
		time.Sleep(time.Millisecond)
	}
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })

	started := time.Now()
	s.Stop()
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("Stop took %v behind the wrapper's FFmpeg", elapsed)
	}
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if processGone(child) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Stop left the wrapper's FFmpeg running")
		}
	}
}

// processGone treats a zombie as gone: without a reaping init (some
// containers) a killed orphan is never collected.
func processGone(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return true
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	_, after, ok := strings.Cut(string(stat), ") ")
	return ok && strings.HasPrefix(after, "Z")
}
