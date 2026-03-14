package utils

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const shellLookupTimeout = 2 * time.Second

var (
	defaultFFmpegOnce sync.Once
	defaultFFmpegPath string
	defaultFFmpegErr  error
)

// ResolveFFmpegPath returns an executable ffmpeg path.
// If preferred is non-empty, only that command/path is resolved.
func ResolveFFmpegPath(preferred string) (string, error) {
	preferred = strings.TrimSpace(preferred)
	if preferred != "" {
		return resolveCommandPath(preferred)
	}

	defaultFFmpegOnce.Do(func() {
		defaultFFmpegPath, defaultFFmpegErr = resolveToolPath("ffmpeg")
	})

	return defaultFFmpegPath, defaultFFmpegErr
}

// ResolveFFprobePath returns an executable ffprobe path.
// If ffmpegPath resolves to an absolute binary, its sibling ffprobe is preferred.
func ResolveFFprobePath(ffmpegPath string) (string, error) {
	if ffmpegPath != "" {
		resolvedFFmpeg, err := resolveCommandPath(ffmpegPath)
		if err == nil && filepath.Base(resolvedFFmpeg) != resolvedFFmpeg {
			sibling := filepath.Join(filepath.Dir(resolvedFFmpeg), "ffprobe")
			if isExistingFile(sibling) {
				return sibling, nil
			}
		}
	}

	return resolveToolPath("ffprobe")
}

func resolveCommandPath(command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", exec.ErrNotFound
	}

	if filepath.Base(command) != command {
		return resolvePathCandidate(command)
	}

	return resolveToolPath(command)
}

func resolvePathCandidate(path string) (string, error) {
	if !filepath.IsAbs(path) {
		absPath, err := filepath.Abs(path)
		if err == nil {
			path = absPath
		}
	}

	if isExistingFile(path) {
		return path, nil
	}

	return "", fmt.Errorf("%s: %w", path, exec.ErrNotFound)
}

func resolveToolPath(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err == nil {
		return path, nil
	}

	if path := commonDarwinToolPath(name); path != "" {
		return path, nil
	}

	if runtime.GOOS == "darwin" {
		path, lookupErr := loginShellToolPath(name)
		if lookupErr == nil {
			prependPathDir(filepath.Dir(path))
			return path, nil
		}
	}

	return "", fmt.Errorf("%s: %w", name, exec.ErrNotFound)
}

func commonDarwinToolPath(name string) string {
	if runtime.GOOS != "darwin" {
		return ""
	}

	candidates := []string{
		filepath.Join("/opt/homebrew/bin", name),
		filepath.Join("/usr/local/bin", name),
		filepath.Join("/opt/local/bin", name),
	}

	for _, candidate := range candidates {
		if isExistingFile(candidate) {
			return candidate
		}
	}

	return ""
}

func loginShellToolPath(name string) (string, error) {
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		shell = "/bin/zsh"
	}

	ctx, cancel := context.WithTimeout(context.Background(), shellLookupTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, shell, "-lc", "command -v -- "+shellQuote(name))
	setSysProcAttr(cmd)

	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !filepath.IsAbs(line) {
			continue
		}
		if isExistingFile(line) {
			return line, nil
		}
	}

	return "", fmt.Errorf("%s: %w", name, exec.ErrNotFound)
}

func prependPathDir(dir string) {
	if dir == "" {
		return
	}

	pathEnv := os.Getenv("PATH")
	entries := filepath.SplitList(pathEnv)
	for _, entry := range entries {
		if entry == dir {
			return
		}
	}

	if pathEnv == "" {
		_ = os.Setenv("PATH", dir)
		return
	}

	_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+pathEnv)
}

func isExistingFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}

	if runtime.GOOS == "windows" {
		return true
	}

	return info.Mode()&0o111 != 0
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
