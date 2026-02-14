//go:build linux

package rtmp

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

func findDanglingGo2tvRTMPFFmpeg(port string) ([]rtmpProc, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read /proc: %w", err)
	}

	var out []rtmpProc
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		if pid == os.Getpid() {
			continue
		}

		args, err := readCmdlineArgs(pid)
		if err != nil || len(args) == 0 {
			continue
		}
		if !isGo2tvRTMPFfmpegArgs(args, port) {
			continue
		}

		out = append(out, rtmpProc{
			pid:     pid,
			tempDir: go2tvRTMPTempDirFromArgs(args),
		})
	}

	return out, nil
}

func readCmdlineArgs(pid int) ([]string, error) {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return nil, err
	}
	parts := bytes.Split(b, []byte{0})
	args := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		args = append(args, string(p))
	}
	return args, nil
}
