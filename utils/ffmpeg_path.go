package utils

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
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
		if err == nil {
			sibling := filepath.Join(filepath.Dir(resolvedFFmpeg), "ffprobe")
			if isExecutableFile(sibling) {
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

	for _, candidate := range executablePathCandidates(path) {
		if isExecutableFile(candidate) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("%s: %w", path, exec.ErrNotFound)
}

func resolveToolPath(name string) (string, error) {
	if path := bundledToolPath(name); path != "" {
		return path, nil
	}

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

func bundledToolPath(name string) string {
	exePath, err := os.Executable()
	if err != nil {
		return ""
	}

	exeDir := filepath.Dir(exePath)
	candidates := []string{
		filepath.Join(exeDir, name),
		filepath.Join(exeDir, "..", "Resources", name),
		filepath.Join(exeDir, "..", "Resources", "bin", name),
	}

	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		for _, pathCandidate := range executablePathCandidates(candidate) {
			if isExecutableFile(pathCandidate) {
				return pathCandidate
			}
		}
	}

	return ""
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
		if isExecutableFile(candidate) {
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
		if isExecutableFile(line) {
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
	if slices.Contains(entries, dir) {
		return
	}

	if pathEnv == "" {
		_ = os.Setenv("PATH", dir)
		return
	}

	_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+pathEnv)
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}

	if runtime.GOOS == "windows" {
		return hasWindowsExecutableExt(path)
	}

	return info.Mode()&0o111 != 0
}

func executablePathCandidates(path string) []string {
	candidates := []string{path}

	if runtime.GOOS != "windows" || filepath.Ext(path) != "" {
		return candidates
	}

	for _, ext := range windowsExecutableExts() {
		candidates = append(candidates, path+ext)
	}

	return candidates
}

func hasWindowsExecutableExt(path string) bool {
	ext := strings.ToUpper(filepath.Ext(path))
	if ext == "" {
		return false
	}

	return slices.Contains(windowsExecutableExts(), ext)
}

func windowsExecutableExts() []string {
	pathExt := os.Getenv("PATHEXT")
	if pathExt == "" {
		pathExt = ".COM;.EXE;.BAT;.CMD"
	}

	parts := strings.Split(pathExt, ";")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.ToUpper(strings.TrimSpace(part))
		if part == "" {
			continue
		}
		out = append(out, part)
	}

	return out
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
