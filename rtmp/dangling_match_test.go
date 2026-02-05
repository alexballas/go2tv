package rtmp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsGo2tvRTMPFfmpegArgs(t *testing.T) {
	t.Parallel()

	tempRoot := os.TempDir()
	tempDir := filepath.Join(tempRoot, "go2tv-rtmp-123")
	args, err := BuildCLICommand("key", "1935", tempDir)
	if err != nil {
		t.Fatalf("BuildCLICommand: %v", err)
	}

	full := append([]string{"ffmpeg"}, args...)
	if !isGo2tvRTMPFfmpegArgs(full, "1935") {
		t.Fatalf("expected match")
	}
	if isGo2tvRTMPFfmpegArgs(full, "1936") {
		t.Fatalf("expected no match on different port")
	}

	badTempDir := filepath.Join(tempRoot, "not-go2tv-rtmp-123")
	args2, err := BuildCLICommand("key", "1935", badTempDir)
	if err != nil {
		t.Fatalf("BuildCLICommand(bad): %v", err)
	}
	full2 := append([]string{"ffmpeg"}, args2...)
	if isGo2tvRTMPFfmpegArgs(full2, "1935") {
		t.Fatalf("expected no match")
	}
}

func TestIsSafeGo2tvRTMPTempDir(t *testing.T) {
	t.Parallel()

	tempRoot := os.TempDir()
	if isSafeGo2tvRTMPTempDir(tempRoot) {
		t.Fatalf("should not allow removing /tmp")
	}
	if !isSafeGo2tvRTMPTempDir(filepath.Join(tempRoot, "go2tv-rtmp-123")) {
		t.Fatalf("expected safe temp dir")
	}
	if isSafeGo2tvRTMPTempDir(filepath.Join(string(os.PathSeparator), "etc", "go2tv-rtmp-123")) {
		t.Fatalf("should not allow outside temp")
	}
}
