//go:build !(android || ios)

package servermode

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestVerifiedFFmpegPath(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := verifiedFFmpegPath(executable); err != nil || got != executable {
		t.Fatalf("verified path = %q, %v; want %q", got, err, executable)
	}
	if got, err := verifiedFFmpegPath(t.TempDir()); err == nil || got != "" {
		t.Fatalf("explicit invalid path = %q, %v; want error", got, err)
	}
}

func TestRunNeverReadsPipedStdin(t *testing.T) {
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readEnd.Close()
	defer writeEnd.Close()
	original := os.Stdin
	os.Stdin = readEnd
	defer func() { os.Stdin = original }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := make(chan string, 8)
	result := make(chan error, 1)
	go func() {
		result <- Run(ctx, Config{Listen: "127.0.0.1:0", MediaRoots: []string{t.TempDir()}}, channelWriter(output))
	}()
	select {
	case line := <-output:
		if !strings.Contains(line, "Web server listening:") {
			t.Fatalf("first output = %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server blocked on piped stdin")
	}
	cancel()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

type channelWriter chan<- string

func (w channelWriter) Write(p []byte) (int, error) {
	w <- string(p)
	return len(p), nil
}
