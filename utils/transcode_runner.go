package utils

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func runFFmpegTranscode(
	ctx context.Context,
	ff *exec.Cmd,
	input any,
	in string,
	w io.Writer,
	args []string,
) (int64, error) {
	cmd := exec.Command(args[0], args[1:]...)
	setSysProcAttr(cmd)

	*ff = *cmd
	if in == "pipe:0" {
		ff.Stdin = input.(io.Reader)
	}

	cw := &countingWriter{w: w}
	var stderr bytes.Buffer
	ff.Stdout = cw
	ff.Stderr = &stderr

	if err := ff.Start(); err != nil {
		return 0, fmt.Errorf("%w: %s", err, tailFFmpegStderr(strings.TrimSpace(stderr.String()), 240))
	}

	done := make(chan error, 1)
	go func() {
		done <- ff.Wait()
	}()

	select {
	case <-ctx.Done():
		if ff.Process != nil {
			_ = ff.Process.Kill()
		}
		<-done
		return cw.n, ctx.Err()
	case err := <-done:
		if err != nil {
			return cw.n, fmt.Errorf("%w: %s", err, tailFFmpegStderr(strings.TrimSpace(stderr.String()), 240))
		}
		return cw.n, nil
	}
}
