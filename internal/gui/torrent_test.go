package gui

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
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

// Resolve markers from the executable, not the test's mutable environment.
// Cleanup can remove the release marker before a background probe sees it.
const torrentTestProbeGate = `probe_dir=${0%/*}
[ -d "$probe_dir" ] || exit 0
: > "$probe_dir/entered" || exit 0
while [ ! -e "$probe_dir/release" ]; do
    [ -d "$probe_dir" ] || exit 0
    sleep 0.01 || exit 1
done
`

// A probe waiting for missing pieces must be interruptible without ever
// creating a renderer session after Cancel download completes.
func torrentTestBlockedProbe(t *testing.T) (ffmpeg, entered string, release func()) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake probe requires a shell")
	}
	dir := t.TempDir()
	entered, released := filepath.Join(dir, "entered"), filepath.Join(dir, "release")
	ffmpeg = filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" + torrentTestProbeGate + `printf '%s' '{"format":{"duration":"60"}}'` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "ffprobe"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	release = sync.OnceFunc(func() {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return
		}
		if err := os.WriteFile(released, nil, 0o600); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(release)
	return ffmpeg, entered, release
}

func TestBlockedProbeExitsAfterFixtureCleanup(t *testing.T) {
	tt := []struct {
		name, binary string
		remove       bool
	}{
		{name: "ffprobe released", binary: "ffprobe"},
		{name: "ffprobe directory removed", binary: "ffprobe", remove: true},
		{name: "ffmpeg released", binary: "ffmpeg"},
		{name: "ffmpeg directory removed", binary: "ffmpeg", remove: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			ffmpeg, entered, release := torrentTestBlockedProbe(t)
			dir := filepath.Dir(ffmpeg)
			if tc.binary == "ffmpeg" {
				if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\n"+torrentTestProbeGate), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cmd := exec.CommandContext(ctx, filepath.Join(dir, tc.binary))
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			var waitErr error
			go func() {
				waitErr = cmd.Wait()
				close(done)
			}()
			defer func() {
				cancel()
				<-done
			}()
			waitForTorrentTestProbe(t, entered)
			if tc.remove {
				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
			} else {
				release()
			}
			select {
			case <-done:
				if waitErr != nil {
					t.Fatalf("probe exit: %v", waitErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("probe survived fixture cleanup")
			}
		})
	}
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

type serialUIDriver struct {
	fyne.Driver
	mu     sync.Mutex
	active atomic.Uint64
}

func (d *serialUIDriver) DoFromGoroutine(run func(), _ bool) {
	// Nested fyne.Do calls already run inside the serialized UI callback.
	var stack [64]byte
	n := runtime.Stack(stack[:], false)
	id, err := strconv.ParseUint(string(bytes.Fields(stack[:n])[1]), 10, 64)
	if err != nil {
		panic(err)
	}
	if d.active.Load() == id {
		run()
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.active.Store(id)
	defer d.active.Store(0)
	run()
}

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
