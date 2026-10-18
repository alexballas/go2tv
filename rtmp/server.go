package rtmp

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// Server manages the RTMP server process (ffmpeg)
type Server struct {
	cmd     *exec.Cmd
	stderr  bytes.Buffer
	tempDir string
	mu      sync.Mutex
	running bool
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
	setSysProcAttr(cmd)
	s.stderr.Reset()
	cmd.Stderr = &s.stderr

	// Start the command
	if err := cmd.Start(); err != nil {
		s.internalCleanup()
		if cleanupErr != nil {
			return "", fmt.Errorf("failed to start ffmpeg (cleanup err: %v): %w", cleanupErr, err)
		}
		return "", fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	s.cmd = cmd
	s.running = true

	return tempDir, nil
}

// Wait blocks until the FFmpeg process exits and returns the error
func (s *Server) Wait() error {
	if s.cmd == nil {
		return fmt.Errorf("server not started")
	}
	// Wait should not hold the lock for the entire duration as it blocks
	err := s.cmd.Wait()
	if err == nil {
		return nil
	}

	if stderr := s.stderrTail(240); stderr != "" {
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

	killProcess(s.cmd)

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

func (s *Server) stderrTail(max int) string {
	output := strings.TrimSpace(s.stderr.String())
	if output == "" {
		return ""
	}
	if max <= 0 || len(output) <= max {
		return output
	}
	return output[len(output)-max:]
}
