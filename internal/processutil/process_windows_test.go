//go:build windows

package processutil

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const helperEnv = "PROCESSUTIL_WINDOWS_HELPER"

// The test binary doubles as a wrapper that starts FFmpeg and exits at once,
// and as the FFmpeg that keeps the inherited stdout open.
func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "wrapper":
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), helperEnv+"=ffmpeg")
		child.Stdout = os.Stdout
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		_ = os.WriteFile(os.Getenv("PROCESSUTIL_CHILD_PID"), []byte(strconv.Itoa(child.Process.Pid)), 0600)
		os.Exit(0)
	case "ffmpeg":
		time.Sleep(5 * time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestKillReachesFFmpegAfterWrapperExits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "child")
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), helperEnv+"=wrapper", "PROCESSUTIL_CHILD_PID="+path)
	cmd.Stdout = io.Discard
	Configure(cmd)
	if err := Start(cmd); err != nil {
		t.Fatal(err)
	}
	waited := make(chan struct{})
	go func() { _ = cmd.Wait(); Release(cmd); close(waited) }()
	defer func() { _ = Kill(cmd); <-waited }()

	var child syscall.Handle
	deadline := time.Now().Add(5 * time.Second)
	for child == 0 {
		data, _ := os.ReadFile(path)
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			// Held open so the PID cannot be reused while we observe it.
			child, _ = syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
		}
		if time.Now().After(deadline) {
			t.Fatal("wrapper did not start its FFmpeg")
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer syscall.CloseHandle(child)
	// The wrapper exits right after writing the PID; its FFmpeg holds stdout.
	time.Sleep(200 * time.Millisecond)

	if err := Kill(cmd); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if event, _ := syscall.WaitForSingleObject(child, 5000); event != syscall.WAIT_OBJECT_0 {
		t.Fatal("FFmpeg survived Kill after its wrapper exited")
	}
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait still blocked on the killed FFmpeg's pipe")
	}
}
