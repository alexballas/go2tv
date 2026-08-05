package crashlog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	pendingFilename = "active.pending"
	maxCrashReports = 5
	crashExt        = ".crash"
)

type Session struct {
	dir           string
	pendingPath   string
	previousCrash string
	closeOnce     sync.Once
}

func Init(appName string) (*Session, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		// Android sets TMPDIR (app cache dir) but not HOME/XDG_CACHE_HOME,
		// so fall back to the temp dir instead of disabling crash logging.
		cacheDir = os.TempDir()
		if cacheDir == "" {
			return nil, fmt.Errorf("user cache dir: %w", err)
		}
	}

	return initInDir(filepath.Join(cacheDir, appName, "crashes"))
}

func (s *Session) PreviousCrashPath() string {
	if s == nil {
		return ""
	}

	return s.previousCrash
}

func (s *Session) LatestCrashPath() string {
	if s == nil {
		return ""
	}

	reports, err := crashReports(s.dir)
	if err != nil || len(reports) == 0 {
		return ""
	}

	return reports[0]
}

func (s *Session) CloseClean() error {
	if s == nil {
		return nil
	}

	var closeErr error
	s.closeOnce.Do(func() {
		closeErr = debug.SetCrashOutput(nil, debug.CrashOptions{})

		removeErr := os.Remove(s.pendingPath)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}

		closeErr = errors.Join(closeErr, removeErr)
	})

	return closeErr
}

func initInDir(dir string) (*Session, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create crash dir: %w", err)
	}

	previousCrash, err := rotatePendingReport(dir)
	if err != nil {
		return nil, err
	}

	pendingPath := filepath.Join(dir, pendingFilename)
	f, err := os.OpenFile(pendingPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open pending crash file: %w", err)
	}

	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		_ = f.Close()
		_ = os.Remove(pendingPath)
		return nil, fmt.Errorf("set crash output: %w", err)
	}

	if err := f.Close(); err != nil {
		_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
		_ = os.Remove(pendingPath)
		return nil, fmt.Errorf("close pending crash file: %w", err)
	}

	debug.SetTraceback("all")

	if err := trimReports(dir, maxCrashReports); err != nil {
		return nil, err
	}

	return &Session{
		dir:           dir,
		pendingPath:   pendingPath,
		previousCrash: previousCrash,
	}, nil
}

func rotatePendingReport(dir string) (string, error) {
	pendingPath := filepath.Join(dir, pendingFilename)
	info, err := os.Stat(pendingPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}

		return "", fmt.Errorf("stat pending crash file: %w", err)
	}

	if info.Size() == 0 {
		if err := os.Remove(pendingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("remove empty pending crash file: %w", err)
		}

		return "", nil
	}

	reportPath, err := nextReportPath(dir, info.ModTime())
	if err != nil {
		return "", err
	}

	if err := os.Rename(pendingPath, reportPath); err != nil {
		return "", fmt.Errorf("rotate pending crash file: %w", err)
	}

	return reportPath, nil
}

func nextReportPath(dir string, when time.Time) (string, error) {
	stamp := when.UTC().Format("20060102-150405")
	base := filepath.Join(dir, stamp)
	path := base + crashExt

	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return path, nil
	} else if err != nil {
		return "", fmt.Errorf("stat crash report: %w", err)
	}

	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s-%d%s", base, i, crashExt)
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", fmt.Errorf("stat crash report: %w", err)
		}
	}
}

func trimReports(dir string, keep int) error {
	reports, err := crashReports(dir)
	if err != nil {
		return err
	}

	if keep < 0 {
		keep = 0
	}

	if len(reports) <= keep {
		return nil
	}

	for _, report := range reports[keep:] {
		if err := os.Remove(report); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove old crash report: %w", err)
		}
	}

	return nil
}

func crashReports(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read crash dir: %w", err)
	}

	reports := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), crashExt) {
			continue
		}

		reports = append(reports, filepath.Join(dir, entry.Name()))
	}

	sort.Slice(reports, func(i, j int) bool {
		return filepath.Base(reports[i]) > filepath.Base(reports[j])
	})

	return reports, nil
}
