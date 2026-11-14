package rtmp

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"go2tv.app/go2tv/v2/internal/processutil"
)

const stopReapTimeout = 5 * time.Second

// Server manages the RTMP server process (ffmpeg)
type Server struct {
	run     *serverRun
	tempDir string
	mu      sync.Mutex
	running bool
}

type serverRun struct {
	cmd    *exec.Cmd
	stderr bytes.Buffer
	done   chan struct{}
	err    error
}

// GenerateKey generates a random stream key
func GenerateKey() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 20)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

// TempDir returns the temporary directory used by the server
func (s *Server) TempDir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tempDir
}

// NewServer creates a new RTMP server instance
func NewServer() *Server {
	return &Server{}
}

// Start launches the RTMP server
func (s *Server) Start(ffmpegPath, streamKey, port string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return "", fmt.Errorf("server already running")
	}

	// Best-effort: kill stale go2tv RTMP ffmpeg servers on same port.
	// Ignore errors unless we also fail to start.
	_, cleanupErr := CleanupDanglingFFmpegRTMPServers(port)

	// Create temp directory for HLS segments
	tempDir, err := os.MkdirTemp("", "go2tv-rtmp-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	s.tempDir = tempDir

	args := BuildCLICommand(streamKey, port, tempDir)

	cmd := exec.Command(ffmpegPath, args...)
	processutil.Configure(cmd)
	run := &serverRun{cmd: cmd, done: make(chan struct{})}
	cmd.Stderr = &run.stderr

	// Start the command
	if err := processutil.Start(cmd); err != nil {
		s.internalCleanup()
		if cleanupErr != nil {
			return "", fmt.Errorf("failed to start ffmpeg (cleanup err: %v): %w", cleanupErr, err)
		}
		return "", fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	s.run = run
	s.running = true
	go func() {
		run.err = cmd.Wait()
		processutil.Release(cmd)
		close(run.done)
	}()

	return tempDir, nil
}

// Wait blocks until the FFmpeg process exits and returns the error
func (s *Server) Wait() error {
	s.mu.Lock()
	run := s.run
	s.mu.Unlock()
	if run == nil {
		return fmt.Errorf("server not started")
	}
	<-run.done
	err := run.err
	if err == nil {
		return nil
	}

	if stderr := run.stderrTail(240); stderr != "" {
		return fmt.Errorf("%w: %s", err, stderr)
	}

	return err
}

// Stop terminates the RTMP server
func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return
	}

	select {
	case <-s.run.done: // Exited on its own; its PID may name another process.
	default:
		_ = processutil.Kill(s.run.cmd)
		// Kill reaches the whole tree; the bound covers a tree it could not
		// contain, whose descendant would otherwise pin Wait on its pipes.
		select {
		case <-s.run.done:
		case <-time.After(stopReapTimeout):
		}
	}

	s.running = false
	s.internalCleanup()
}

// Cleanup removes temporary files
func (s *Server) Cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.internalCleanup()
}

func (s *Server) internalCleanup() {
	if s.tempDir != "" {
		_ = os.RemoveAll(s.tempDir)
	}
}

func (s *serverRun) stderrTail(max int) string {
	output := strings.TrimSpace(s.stderr.String())
	if output == "" {
		return ""
	}
	if max <= 0 || len(output) <= max {
		return output
	}
	return output[len(output)-max:]
}
