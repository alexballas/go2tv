package rtmp

import (
	"fmt"
	"path/filepath"
)

// BuildCLICommand constructs the ffmpeg command arguments for the RTMP server
func BuildCLICommand(streamKey, port, tempDir string) ([]string, error) {
	segmentPath := filepath.Join(tempDir, "segment_%03d.ts")
	playlistPath := filepath.Join(tempDir, "playlist.m3u8")
	rtmpURL := fmt.Sprintf("rtmp://0.0.0.0:%s/live/%s", port, streamKey)

	return []string{
		"-listen", "1",
		"-timeout", "30000000",
		"-i", rtmpURL,
		"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-g", "30",
		"-c:a", "aac", "-ar", "48000", "-ac", "2",
		"-f", "hls",
		"-hls_time", "2",
		"-hls_list_size", "5",
		"-hls_flags", "delete_segments+append_list",
		"-hls_segment_filename", segmentPath,
		playlistPath,
	}, nil
}
