//go:build darwin

package rtmp

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
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
	// -ww to avoid truncation.
	cmd := exec.Command("ps", "-axww", "-o", "pid=,command=")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("ps stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ps start: %w", err)
	}
	defer func() { _ = cmd.Wait() }()

	scanner := bufio.NewScanner(out)
	var procs []rtmpProc
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 {
			continue
		}
		if pid == os.Getpid() {
			continue
		}
		args := fields[1:]
		if !isGo2tvRTMPFfmpegArgs(args, port) {
			continue
		}

		procs = append(procs, rtmpProc{
			pid:     pid,
			tempDir: go2tvRTMPTempDirFromArgs(args),
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("ps scan: %w", err)
	}

	return procs, nil
}
