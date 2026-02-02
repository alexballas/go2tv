package rtmp

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// Server manages the RTMP server process (ffmpeg)
type Server struct {
	cmd     *exec.Cmd
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

	// Create temp directory for HLS segments
	tempDir := filepath.Join(os.TempDir(), fmt.Sprintf("go2tv-rtmp-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	s.tempDir = tempDir

	args, err := BuildCLICommand(streamKey, port, tempDir)
	if err != nil {
		s.Cleanup()
		return "", fmt.Errorf("failed to build command: %w", err)
	}

	cmd := exec.Command(ffmpegPath, args...)
	setSysProcAttr(cmd)

	// Start the command
	if err := cmd.Start(); err != nil {
		s.Cleanup()
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
	return s.cmd.Wait()
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
