package crashlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRotatePendingReportsRecoversExitedProcesses(t *testing.T) {
	tt := []struct {
		name      string
		file      string
		contents  string
		wantCrash bool
	}{
		{name: "crash output becomes report", file: pendingName(4242), contents: "panic", wantCrash: true},
		{name: "clean leftover is dropped", file: pendingName(4242)},
		{name: "pre-2.9 shared file still recovered", file: "active.pending", contents: "panic", wantCrash: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			pendingPath := filepath.Join(dir, tc.file)
			if err := os.WriteFile(pendingPath, []byte(tc.contents), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			got, err := rotatePendingReports(dir)
			if err != nil {
				t.Fatalf("rotatePendingReports: %v", err)
			}
			if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
				t.Fatalf("pending file still exists: %v", err)
			}
			if !tc.wantCrash {
				if got != "" {
					t.Fatalf("previous crash = %q, want none", got)
				}
				return
			}
			b, err := os.ReadFile(got)
			if err != nil {
				t.Fatalf("ReadFile rotated file %q: %v", got, err)
			}
			if string(b) != tc.contents {
				t.Fatalf("rotated contents = %q, want %q", b, tc.contents)
			}
		})
	}
}

// A second instance (managed server child, CLI) starting while another runs
// must leave the live owner's crash output alone, then recover it once the
// owner has exited (W290-03).
func TestRotatePendingReportsSkipsLiveOwner(t *testing.T) {
	dir := t.TempDir()
	livePath := filepath.Join(dir, pendingName(4242))
	owner, err := os.OpenFile(livePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer owner.Close()
	if err := lockPending(owner); err != nil {
		t.Fatalf("lockPending: %v", err)
	}
	if _, err := owner.WriteString("panic"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}

	got, err := rotatePendingReports(dir)
	if err != nil {
		t.Fatalf("rotatePendingReports with live owner: %v", err)
	}
	if got != "" {
		t.Fatalf("previous crash = %q, want live owner skipped", got)
	}
	if _, err := os.Stat(livePath); err != nil {
		t.Fatalf("live pending file disturbed: %v", err)
	}

	if err := owner.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got, err = rotatePendingReports(dir)
	if err != nil {
		t.Fatalf("rotatePendingReports after owner exit: %v", err)
	}
	if b, err := os.ReadFile(got); err != nil || string(b) != "panic" {
		t.Fatalf("rotated report %q = %q, %v; want owner's crash output", got, b, err)
	}
}

func TestTrimReportsKeepsNewestFive(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, time.April, 2, 12, 0, 0, 0, time.UTC)

	for i := range 7 {
		path := filepath.Join(dir, now.Add(time.Duration(i)*time.Second).Format("20060102-150405")+crashExt)
		if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	if err := trimReports(dir, 5); err != nil {
		t.Fatalf("trimReports: %v", err)
	}

	reports, err := crashReports(dir)
	if err != nil {
		t.Fatalf("crashReports: %v", err)
	}

	if len(reports) != 5 {
		t.Fatalf("report count = %d", len(reports))
	}

	if strings.Contains(filepath.Base(reports[len(reports)-1]), "120001") {
		t.Fatalf("oldest report was not trimmed: %v", reports)
	}
}

func TestTrimReportsNoopWhenBelowKeep(t *testing.T) {
	dir := t.TempDir()
	for i := range 2 {
		path := filepath.Join(dir, time.Date(2026, time.April, 2, 12, 0, i, 0, time.UTC).Format("20060102-150405")+crashExt)
		if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	if err := trimReports(dir, 5); err != nil {
		t.Fatalf("trimReports: %v", err)
	}

	reports, err := crashReports(dir)
	if err != nil {
		t.Fatalf("crashReports: %v", err)
	}

	if len(reports) != 2 {
		t.Fatalf("report count = %d", len(reports))
	}
}

func TestCloseCleanIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	pendingPath := filepath.Join(dir, pendingName(4242))
	if err := os.WriteFile(pendingPath, nil, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	session := &Session{pendingPath: pendingPath}
	if err := session.CloseClean(); err != nil {
		t.Fatalf("first CloseClean: %v", err)
	}

	if err := session.CloseClean(); err != nil {
		t.Fatalf("second CloseClean: %v", err)
	}

	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Fatalf("pending file still exists: %v", err)
	}
}

func TestLatestCrashPathReturnsNewestReport(t *testing.T) {
	dir := t.TempDir()
	older := filepath.Join(dir, "20260402-120000.crash")
	newer := filepath.Join(dir, "20260402-120001.crash")
	if err := os.WriteFile(older, []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile older: %v", err)
	}
	if err := os.WriteFile(newer, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile newer: %v", err)
	}

	session := &Session{dir: dir}
	if got := session.LatestCrashPath(); got != newer {
		t.Fatalf("LatestCrashPath = %q, want %q", got, newer)
	}
}
