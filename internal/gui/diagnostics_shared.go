package gui

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

func diagnosticsFileName() string {
	return "go2tv-diagnostics.txt"
}

func diagnosticsAvailable(s *FyneScreen) bool {
	if s == nil {
		return false
	}

	return hasDebugLogs(s.Debug) || hasDebugLogs(s.DiscoveryDebug) || latestCrashPath(s) != ""
}

func latestCrashPath(s *FyneScreen) string {
	if s == nil {
		return ""
	}

	if s.PendingCrashPath != "" {
		return s.PendingCrashPath
	}

	if s.Crash == nil {
		return ""
	}

	return s.Crash.LatestCrashPath()
}

func writeDiagnostics(w io.Writer, s *FyneScreen) error {
	if _, err := fmt.Fprintf(
		w, "Go2TV Diagnostics\nGenerated: %s\nVersion: %s\nGOOS/GOARCH: %s/%s\nGo version: %s\n\n",
		time.Now().UTC().Format(time.RFC3339),
		s.version,
		runtime.GOOS,
		runtime.GOARCH,
		runtime.Version(),
	); err != nil {
		return err
	}

	if info, ok := debug.ReadBuildInfo(); ok {
		if _, err := io.WriteString(w, "=== Build Info ===\n"); err != nil {
			return err
		}

		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs", "vcs.revision", "vcs.time", "vcs.modified", "GOOS", "GOARCH", "-compiler", "-buildmode":
				if _, err := fmt.Fprintf(w, "%s: %s\n", setting.Key, setting.Value); err != nil {
					return err
				}
			}
		}

		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
	}

	crashPath := latestCrashPath(s)
	if _, err := io.WriteString(w, "=== Crash Report ===\n"); err != nil {
		return err
	}
	if crashPath == "" {
		if _, err := io.WriteString(w, "No crash report found.\n\n"); err != nil {
			return err
		}
	} else {
		b, err := os.ReadFile(crashPath)
		if err != nil {
			return fmt.Errorf("read crash report: %w", err)
		}

		info, err := os.Stat(crashPath)
		if err != nil {
			return fmt.Errorf("stat crash report: %w", err)
		}
		if _, err := fmt.Fprintf(w, "Crash time: %s\n", info.ModTime().UTC().Format(time.RFC3339)); err != nil {
			return err
		}

		if _, err := w.Write(b); err != nil {
			return err
		}
		if !strings.HasSuffix(string(b), "\n") {
			if _, err := io.WriteString(w, "\n"); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
	}

	if _, err := io.WriteString(w, "=== Runtime Log Ring ===\n"); err != nil {
		return err
	}
	if !hasDebugLogs(s.Debug) {
		if _, err := io.WriteString(w, "Runtime logs are empty.\n\n"); err != nil {
			return err
		}
	} else {
		if err := writeDebugLogs(w, s.Debug); err != nil {
			return err
		}
	}

	if _, err := io.WriteString(w, "\n=== Discovery Log Ring ===\n"); err != nil {
		return err
	}
	if !hasDebugLogs(s.DiscoveryDebug) {
		_, err := io.WriteString(w, "Discovery logs are empty.\n")
		return err
	}

	return writeDebugLogs(w, s.DiscoveryDebug)
}
