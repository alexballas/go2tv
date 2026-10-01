package gui

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"go2tv.app/go2tv/v2/internal/torrentstream"
)

func newTestTorrentSession(t *testing.T) (*torrentstream.Session, string) {
	t.Helper()
	info, err := bencode.Marshal(metainfo.Info{
		Name: "old.mp4", Length: 1, PieceLength: 16384, Pieces: make([]byte, 20),
	})
	if err != nil {
		t.Fatal(err)
	}
	var document bytes.Buffer
	if err := (&metainfo.MetaInfo{InfoBytes: info}).Write(&document); err != nil {
		t.Fatal(err)
	}
	session, err := torrentstream.OpenReaderWithOptions(context.Background(), &document, torrentstream.Options{CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	path, err := session.Select(0)
	if err != nil {
		t.Fatal(err)
	}
	return session, path
}

func torrentTestReplacementImage(t *testing.T) string {
	t.Helper()
	var contents bytes.Buffer
	if err := png.Encode(&contents, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "replacement.png")
	if err := os.WriteFile(path, contents.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A probe waiting for missing pieces must be interruptible without ever
// creating a renderer session after Cancel download completes.
func torrentTestBlockedProbe(t *testing.T) (ffmpeg, entered string, release func()) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake probe requires a shell")
	}
	dir := t.TempDir()
	entered, released := filepath.Join(dir, "entered"), filepath.Join(dir, "release")
	t.Setenv("GO2TV_TEST_PROBE_ENTERED", entered)
	t.Setenv("GO2TV_TEST_PROBE_RELEASE", released)
	ffmpeg = filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
: > "$GO2TV_TEST_PROBE_ENTERED"
while [ ! -e "$GO2TV_TEST_PROBE_RELEASE" ]; do sleep 0.01; done
printf '%s' '{"format":{"duration":"60"}}'
`
	if err := os.WriteFile(filepath.Join(dir, "ffprobe"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	release = sync.OnceFunc(func() {
		if err := os.WriteFile(released, nil, 0o600); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(release)
	return ffmpeg, entered, release
}

func waitForTorrentTestProbe(t *testing.T, entered string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(entered); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("startup did not enter probe")
		}
		time.Sleep(time.Millisecond)
	}
}

type queuedUIRequest struct {
	run  func()
	done chan struct{}
}

type queuedUIDriver struct {
	fyne.Driver
	requests chan queuedUIRequest
}

type queuedUIApp struct {
	fyne.App
	driver fyne.Driver
}

func (a *queuedUIApp) Driver() fyne.Driver { return a.driver }

func (d *queuedUIDriver) DoFromGoroutine(run func(), wait bool) {
	request := queuedUIRequest{run: run}
	if wait {
		request.done = make(chan struct{})
	}
	d.requests <- request
	if wait {
		<-request.done
	}
}

func runQueuedUI(request queuedUIRequest) {
	request.run()
	if request.done != nil {
		close(request.done)
	}
}
