//go:build !linux && !darwin

package rtmp

// CleanupDanglingFFmpegRTMPServers is a no-op on this OS.
func CleanupDanglingFFmpegRTMPServers(string) (int, error) {
	return 0, nil
}
