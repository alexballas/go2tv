package rtmp

import (
	"os"
	"path/filepath"
	"strings"
)

const go2tvRTMPTempDirPrefix = "go2tv-rtmp-"

func isGo2tvRTMPFfmpegArgs(args []string, port string) bool {
	if len(args) == 0 {
		return false
	}

	exe := filepath.Base(args[0])
	if exe != "ffmpeg" && !strings.HasPrefix(exe, "ffmpeg") {
		// Some distros use ffmpeg.real or similar.
		if !strings.Contains(exe, "ffmpeg") {
			return false
		}
	}

	if !hasFlagValue(args, "-listen", "1") {
		return false
	}
	if !hasFlagValue(args, "-f", "hls") {
		return false
	}

	inputURL, ok := flagValue(args, "-i")
	if !ok {
		return false
	}
	if !strings.HasPrefix(inputURL, "rtmp://0.0.0.0:") || !strings.Contains(inputURL, "/live/") {
		return false
	}
	expectedPrefix := "rtmp://0.0.0.0:" + port + "/live/"
	if !strings.HasPrefix(inputURL, expectedPrefix) {
		return false
	}

	segmentPath, ok := flagValue(args, "-hls_segment_filename")
	if !ok {
		return false
	}
	playlistPath := args[len(args)-1]

	if !looksLikeGo2tvRTMPPath(segmentPath) || !looksLikeGo2tvRTMPPath(playlistPath) {
		return false
	}
	if !strings.HasSuffix(normalizePath(segmentPath), "/segment_%03d.ts") {
		return false
	}
	if !strings.HasSuffix(normalizePath(playlistPath), "/playlist.m3u8") {
		return false
	}

	segDir := normalizePath(filepath.Dir(segmentPath))
	plDir := normalizePath(filepath.Dir(playlistPath))
	return segDir != "" && segDir == plDir
}

func go2tvRTMPTempDirFromArgs(args []string) string {
	segmentPath, ok := flagValue(args, "-hls_segment_filename")
	if !ok {
		return ""
	}
	segmentDir := filepath.Dir(segmentPath)
	if !looksLikeGo2tvRTMPPath(segmentDir) {
		return ""
	}
	return segmentDir
}

func looksLikeGo2tvRTMPPath(p string) bool {
	if p == "" {
		return false
	}
	return strings.Contains(normalizePath(p), "/"+go2tvRTMPTempDirPrefix)
}

func isSafeGo2tvRTMPTempDir(tempDir string) bool {
	if tempDir == "" {
		return false
	}

	cleanTempDir := filepath.Clean(tempDir)
	base := filepath.Base(cleanTempDir)
	if !strings.HasPrefix(base, go2tvRTMPTempDirPrefix) {
		return false
	}

	root := filepath.Clean(os.TempDir())
	rootWithSep := root + string(os.PathSeparator)
	return cleanTempDir == root || strings.HasPrefix(cleanTempDir, rootWithSep)
}

func normalizePath(p string) string {
	return strings.ReplaceAll(p, "\\", "/")
}

func hasFlagValue(args []string, flag, want string) bool {
	v, ok := flagValue(args, flag)
	return ok && v == want
}

func flagValue(args []string, flag string) (string, bool) {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			return args[i+1], true
		}
	}
	return "", false
}
