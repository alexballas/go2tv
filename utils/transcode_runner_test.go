package utils

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

type failedTranscodeWriter struct{ err error }

func (w failedTranscodeWriter) Write([]byte) (int, error) { return 0, w.err }

func TestTranscodeReapsProcessAfterOutputFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	disconnected := errors.New("receiver disconnected")
	tt := []struct {
		name string
		err  error
		want error
	}{
		{name: "closed receiver", err: disconnected, want: disconnected},
		{name: "short write", want: io.ErrShortWrite},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var command exec.Cmd
			_, err := runFFmpegTranscode(ctx, &command, nil, "", failedTranscodeWriter{err: tc.err}, []string{"sh", "-c", "exec yes encoded"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("transcode error = %v, want %v", err, tc.want)
			}
			if command.ProcessState == nil {
				t.Fatal("encoder was not reaped after its output pipe failed")
			}
		})
	}
}
