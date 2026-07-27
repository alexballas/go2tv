//go:build android || ios

package gui

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexballas/refyne/v2"
)

func mobileCacheDir() (string, error) {
	app := fyne.CurrentApp()
	if app == nil {
		return "", fmt.Errorf("current app unavailable")
	}

	cacheURI := app.Cache().RootURI()
	if cacheURI == nil {
		return "", fmt.Errorf("cache uri unavailable")
	}
	if cacheURI.Scheme() != "file" {
		return "", fmt.Errorf("cache uri scheme %q unsupported", cacheURI.Scheme())
	}

	cacheDir := cacheURI.Path()
	if cacheDir == "" {
		return "", fmt.Errorf("cache path unavailable")
	}

	return cacheDir, nil
}

func createMobileCacheTemp(pattern string) (*os.File, error) {
	cacheDir, err := mobileCacheDir()
	if err != nil {
		return nil, err
	}

	return os.CreateTemp(cacheDir, pattern)
}

func cleanupMobileCacheTempFiles() {
	cacheDir, err := mobileCacheDir()
	if err != nil {
		return
	}

	files, err := filepath.Glob(filepath.Join(cacheDir, "go2tv-*"))
	if err != nil {
		return
	}

	for _, f := range files {
		// RemoveAll, not Remove: older builds left artwork scratch directories
		// behind here, and a plain Remove cannot clear them.
		os.RemoveAll(f)
	}
}
