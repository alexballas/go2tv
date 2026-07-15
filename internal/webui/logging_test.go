package webui

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"go2tv.app/go2tv/v2/internal/controller"
	"go2tv.app/go2tv/v2/internal/library"
)

type recordingLogger struct {
	mu       sync.Mutex
	info     []string
	warnings []string
	errors   []string
}

func (l *recordingLogger) Debug(string) {}
func (l *recordingLogger) Info(message string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.info = append(l.info, message)
}
func (l *recordingLogger) Warning(message string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warnings = append(l.warnings, message)
}
func (l *recordingLogger) Error(message string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errors = append(l.errors, message)
}

func TestWebUIActionLogging(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "media.mp4"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	lib, err := library.Open(library.Config{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	log := &recordingLogger{}
	control := controller.New(controller.Config{})
	defer control.Close()
	handler, err := New(Config{Controller: control, Library: lib, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()

	result := handler.command(context.Background(), envelope{Type: "player.transcode", ID: "enable", Payload: []byte(`{"enabled":true}`)})
	if !result.OK() || len(log.info) != 1 || log.info[0] != "Transcoding enabled" {
		t.Fatalf("result=%#v info=%v", result, log.info)
	}
	result = handler.command(context.Background(), envelope{Type: "player.seek", ID: "seek", Payload: []byte(`{"seconds":12}`)})
	if result.OK() || len(log.warnings) != 1 || log.warnings[0] != "WebUI action failed: player seek (nothing playing)" {
		t.Fatalf("result=%#v warnings=%v", result, log.warnings)
	}
	result = handler.command(context.Background(), envelope{Type: "player.transcode", ID: "stale", Payload: []byte(`{"enabled":false,"expected_revision":0}`)})
	if result.Code != controller.CodeConflict || len(log.warnings) != 1 {
		t.Fatalf("conflict result=%#v warnings=%v", result, log.warnings)
	}
}
