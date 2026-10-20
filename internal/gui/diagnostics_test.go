//go:build !(android || ios)

package gui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteDiagnosticsIncludesCrashAndDebugLogs(t *testing.T) {
	crashDir := t.TempDir()
	crashPath := filepath.Join(crashDir, "20260402-120000.crash")
	if err := os.WriteFile(crashPath, []byte("panic: boom\nstack"), 0o600); err != nil {
		t.Fatalf("WriteFile crash: %v", err)
	}
	crashTime := time.Date(2026, time.April, 2, 15, 0, 0, 0, time.FixedZone("UTC+3", 3*60*60))
	if err := os.Chtimes(crashPath, crashTime, crashTime); err != nil {
		t.Fatalf("Chtimes crash: %v", err)
	}

	s := &FyneScreen{
		version:          "test",
		Debug:            newDebugWriter(runtimeDebugRingSize),
		DiscoveryDebug:   newDebugWriter(discoveryDebugRingSize),
		PendingCrashPath: crashPath,
	}
	_, _ = s.Debug.Write([]byte("line one\n"))
	_, _ = s.Debug.Write([]byte("line two\n"))
	_, _ = s.DiscoveryDebug.Write([]byte("discovery one\n"))

	var buf bytes.Buffer
	if err := writeDiagnostics(&buf, s); err != nil {
		t.Fatalf("writeDiagnostics: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"Go2TV Diagnostics", "=== Crash Report ===\nCrash time: 2026-04-02T12:00:00Z\npanic: boom", "=== Runtime Log Ring ===", "line one", "line two", "=== Discovery Log Ring ===", "discovery one"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in diagnostics:\n%s", want, out)
		}
	}
}
