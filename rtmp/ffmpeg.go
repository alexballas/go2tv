package rtmp

import (
	"fmt"
	"path/filepath"
)

// BuildCLICommand constructs the ffmpeg command arguments for the RTMP server
func BuildCLICommand(streamKey, port, tempDir string) ([]string, error) {
	playlistPath := filepath.Join(tempDir, "playlist.m3u8")
	rtmpURL := fmt.Sprintf("rtmp://0.0.0.0:%s/live/%s", port, streamKey)

	return []string{
		"-listen", "1",
		"-timeout", "30000000",
		"-fflags", "nobuffer",
		"-flags", "low_delay",
		"-probesize", "32",
		"-analyzeduration", "0",
		"-i", rtmpURL,
		"-r", "60",
		"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-g", "60", "-sc_threshold", "0",
		"-c:a", "aac", "-ar", "48000", "-ac", "2",
		"-f", "hls",
		"-hls_time", "1",
		"-hls_list_size", "3",
		"-hls_flags", "delete_segments+append_list+independent_segments",
		"-hls_segment_filename", filepath.Join(tempDir, "segment_%03d.ts"),
		playlistPath,
	}, nil
}
