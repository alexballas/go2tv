//go:build darwin

package rtmp

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

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
