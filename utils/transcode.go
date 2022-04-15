package utils

import (
	"io"
	"net/http"
	"os/exec"
)

func ServeTranscodedStream(w http.ResponseWriter, r *http.Request, f io.Reader, ff *exec.Cmd) error {
	if ff.Process != nil {
		_ = ff.Process.Kill()
	}

	cmd := exec.Command(
		"ffmpeg",
		"-re",
		"-i", "pipe:0",
		"-vcodec", "h264",
		"-acodec", "aac",
		"-ac", "2",
		"-vf", "format=yuv420p",
		"-preset", "ultrafast",
		"-f", "flv",
		"pipe:1",
	)

	ff = cmd
	ff.Stdin = f
	ff.Stdout = w

	w.Header().Set("Transfer-Encoding", "chunked")

	return ff.Run()
}
