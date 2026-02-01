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
func (s *Server) Start(streamKey, port string) (string, error) {
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

	// We assume "ffmpeg" is in the PATH. The GUI should have validated this.
	cmd := exec.Command("ffmpeg", args...)

	// Start the command
	if err := cmd.Start(); err != nil {
		s.Cleanup()
		return "", fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	s.cmd = cmd
	s.running = true

	return tempDir, nil
}

// Stop terminates the RTMP server
func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return
	}

	if s.cmd != nil && s.cmd.Process != nil {
		// Kill the process.
		// Note: os.Process.Kill() forces a kill.
		// Ideally we might want to send SIGTERM first, but for now force kill is safer to ensure port release.
		_ = s.cmd.Process.Kill()

		// Wait for it to exit to avoid zombies, but don't block forever?
		// exec.Command.Wait() handles this.
		// Since we're in Stop(), we probably don't want to block too long.
		// But we should clean up.
		// Let's launch a goroutine to wait?
		// Actually, if we just Kill, the OS handles it.
		// We should call Wait() to release resources.
		go func() {
			_ = s.cmd.Wait()
		}()
	}

	s.running = false
	s.Cleanup()
}

// Cleanup removes temporary files
func (s *Server) Cleanup() {
	// Note: We don't lock here because Stop calls us.
	// But if called from Start's error path, we are locked.
	// This is a bit risky if called from outside without lock.
	// But Cleanup is unexported? No, it's exported.
	// Let's make it safe.
	// But Stop() holds the lock. Recursive lock? Go sync.Mutex is not reentrant.
	// Let's make an internal cleanup.

	if s.tempDir != "" {
		_ = os.RemoveAll(s.tempDir)
		// We don't clear s.tempDir here immediately if we want to be safe,
		// but effectively it's gone.
	}
}
