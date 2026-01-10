//go:build windows

package utils

import (
	"context"
	"errors"
	"io"
	"os/exec"
)

var errWindowsNotSupported = errors.New("chromecast transcoding not supported on Windows")

// ServeChromecastTranscodedStream is a stub for Windows where Chromecast transcoding
// is not supported.
func ServeChromecastTranscodedStream(
	ctx context.Context,
	w io.Writer,
	input any,
	ff *exec.Cmd,
	opts *TranscodeOptions,
) error {
	return errWindowsNotSupported
}
