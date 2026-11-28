//go:build linux || darwin

package rtmp

import (
	"errors"
	"os"
	"time"
)

type rtmpProc struct {
	pid     int
	tempDir string
}

// CleanupDanglingFFmpegRTMPServers kills ffmpeg RTMP servers that match
// go2tv's args for a target port and cleans their temp dirs.
func CleanupDanglingFFmpegRTMPServers(port string) (int, error) {
	procs, err := findDanglingGo2tvRTMPFFmpeg(port)
	if err != nil {
		return 0, err
	}

	var errs []error
	killed := 0
	for _, p := range procs {
		if err := terminatePID(p.pid, 2*time.Second); err != nil {
			errs = append(errs, err)
			continue
		}
		killed++
		if isSafeGo2tvRTMPTempDir(p.tempDir) {
			_ = os.RemoveAll(p.tempDir)
		}
	}

	return killed, errors.Join(errs...)
}
