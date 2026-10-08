//go:build !(android || ios)

package gui

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/alexballas/refyne/v2/data/binding"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/utils"
)

func TestDLNASkipStopsRendererAfterGaplessPlayback(t *testing.T) {
	tt := []struct {
		name    string
		gapless bool
	}{
		{name: "initial playback"},
		{name: "gapless playback", gapless: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			current, next := filepath.Join(dir, "current.mp4"), filepath.Join(dir, "next.mp4")
			if err := os.WriteFile(next, []byte("\x00\x00\x00\x18ftypisom\x00\x00\x00\x00isommp42"), 0600); err != nil {
				t.Fatal(err)
			}
			screen, _ := newDLNAQueueStopTestScreen(t, current, next)
			screen.Transcode = false
			screen.ffmpegPath = filepath.Join(dir, "missing-ffmpeg")
			if tc.gapless {
				// queueNext assigns the server session context to promoted payloads.
				screen.tvdata.SetContext(screen.serverStopCTX)
			}
			actions := make(chan string, 8)
			renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					t.Error(err)
					return
				}
				if r.Method == "SUBSCRIBE" {
					w.Header().Set("SID", "uuid:skip")
					w.Header().Set("TIMEOUT", "Second-300")
				}
				for _, action := range []string{"Stop", "SetAVTransportURI", "Play"} {
					if strings.Contains(r.Header.Get("SOAPAction"), "#"+action+"\"") {
						actions <- action
					}
				}
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(renderer.Close)
			t.Cleanup(func() { stopActionSync(screen) })
			screen.tvdata.ControlURL = renderer.URL
			target := playbackTarget{
				device:     devType{addr: renderer.URL, deviceType: devices.DeviceTypeDLNA},
				controlURL: renderer.URL,
				eventURL:   renderer.URL,
			}
			skipToMediaPathOnTargetAction(screen, next, target)
			for _, want := range []string{"Stop", "SetAVTransportURI", "Play"} {
				select {
				case got := <-actions:
					if got != want {
						t.Fatalf("renderer received %s before %s", got, want)
					}
				case <-time.After(5 * time.Second):
					t.Fatalf("skip never sent %s to renderer", want)
				}
			}
			deadline := time.Now().Add(5 * time.Second)
			for screen.playbackStartupPending() {
				if time.Now().After(deadline) {
					t.Fatal("skip playback startup did not finish")
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}

func TestStopCancelsGaplessSubtitlePreparation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("blocking shell fixture requires POSIX")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe unavailable")
	}
	media := createEmbeddedSubtitleTestMedia(t, ffmpeg, t.TempDir(), "webvtt")
	tt := []struct {
		name       string
		blockProbe bool
	}{
		{name: "during discovery", blockProbe: true},
		{name: "during extraction"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			gate := filepath.Join(dir, "gate")
			if output, err := exec.Command("mkfifo", gate).CombinedOutput(); err != nil {
				t.Fatalf("create extraction gate: %v: %s", err, output)
			}
			started := filepath.Join(dir, "started")
			t.Setenv("GO2TV_QUEUE_TEST_STARTED", started)
			t.Setenv("GO2TV_QUEUE_TEST_GATE", gate)
			t.Setenv("GO2TV_QUEUE_TEST_FFMPEG", ffmpeg)
			t.Setenv("GO2TV_QUEUE_TEST_FFPROBE", ffprobe)
			t.Setenv("TMPDIR", dir)
			t.Setenv("TMP", dir)
			block := ": > \"$GO2TV_QUEUE_TEST_STARTED\"\nread -r value < \"$GO2TV_QUEUE_TEST_GATE\"\n"
			ffmpegScript := "#!/bin/sh\n"
			ffprobeScript := "#!/bin/sh\n"
			if tc.blockProbe {
				ffprobeScript += block
			} else {
				ffmpegScript += "if [ \"$1\" = \"-nostdin\" ]; then\n" + block + "fi\n"
			}
			ffmpegScript += "exec \"$GO2TV_QUEUE_TEST_FFMPEG\" \"$@\"\n"
			ffprobeScript += "exec \"$GO2TV_QUEUE_TEST_FFPROBE\" \"$@\"\n"
			fakeFFmpeg := filepath.Join(dir, "ffmpeg")
			for path, script := range map[string]string{fakeFFmpeg: ffmpegScript, filepath.Join(dir, "ffprobe"): ffprobeScript} {
				if err := os.WriteFile(path, []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
			}
			current := filepath.Join(dir, "current.mkv")
			screen, bodies := newDLNAQueueStopTestScreen(t, current, media)
			screen.ffmpegPath = fakeFFmpeg
			sessionCtx := screen.serverStopCTX
			server := screen.httpserver
			result := make(chan error, 1)
			go func() {
				next, err := queueNext(screen, false)
				if next != nil && err == nil {
					err = errors.New("queued a stopped session")
				}
				result <- err
			}()
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(started); err == nil {
					break
				}
				select {
				case err := <-result:
					t.Fatalf("preparation finished before gate: %v", err)
				default:
				}
				if time.Now().After(deadline) {
					t.Fatal("subtitle preparation did not reach gate")
				}
				time.Sleep(time.Millisecond)
			}
			if !tc.blockProbe {
				files, err := filepath.Glob(filepath.Join(dir, "go2tv-sub-*.srt"))
				if err != nil || len(files) != 1 {
					t.Fatalf("gate did not block subtitle extraction: files=%v error=%v", files, err)
				}
			}
			stopActionSync(screen)
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("Stop did not cancel preparation: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Stop left subtitle preparation blocked")
			}
			if sessionCtx.Err() == nil || screen.tvdata != nil || screen.httpserver != nil || screen.getScreenState() != "Stopped" {
				t.Fatal("Stop retained the DLNA session")
			}
			response := httptest.NewRecorder()
			server.ServeMediaHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/"+utils.ConvertFilename(media), nil))
			if response.Code != http.StatusNotFound {
				t.Fatalf("canceled preparation published media: %d", response.Code)
			}
			bodies.mu.Lock()
			defer bodies.mu.Unlock()
			for _, body := range bodies.values {
				if strings.Contains(body, "SetNextAVTransportURI") {
					t.Fatal("canceled preparation sent Queue")
				}
			}
			files, err := filepath.Glob(filepath.Join(dir, "go2tv-sub-*.srt"))
			if err != nil || len(files) != 0 || len(screen.tempFiles) != 0 {
				t.Fatalf("canceled preparation leaked subtitles: files=%v owned=%v error=%v", files, screen.tempFiles, err)
			}
		})
	}
}

func newDLNAQueueStopTestScreen(t *testing.T, current, media string) (*FyneScreen, *queueSOAPBodies) {
	t.Helper()
	base, bodies := newQueueArtworkTestScreen(t, []string{current, media})
	screen, _ := newMediaCardTestScreen(t)
	screen.tvdata, screen.httpserver = base.tvdata, base.httpserver
	screen.mediafile, screen.SessionQueue = current, base.SessionQueue
	screen.Transcode = true
	screen.State = "Playing"
	screen.playbackStatus = newPlaybackStatusLabel("")
	screen.Stop = widget.NewButton("Stop", nil)
	screen.SlideBar = &tappedSlider{Slider: widget.NewSlider(0, 100)}
	screen.CurrentPos, screen.EndPos = binding.NewString(), binding.NewString()
	// Established playback has detached its startup context.
	screen.tvdata.SetContext(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	screen.serverStopCTX, screen.cancelServerStop = ctx, cancel
	return screen, bodies
}

func TestStopCancelsGaplessQueueRequest(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("TMP", dir)
	media := createEmbeddedSubtitleTestMedia(t, ffmpeg, dir, "srt")
	screen, _ := newDLNAQueueStopTestScreen(t, filepath.Join(dir, "current.mkv"), media)
	screen.ffmpegPath = ffmpeg
	started := make(chan struct{})
	renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
			return
		}
		if strings.Contains(r.Header.Get("SOAPAction"), "SetNextAVTransportURI") {
			close(started)
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(renderer.Close)
	screen.tvdata.ControlURL = renderer.URL
	server := screen.httpserver
	result := make(chan error, 1)
	go func() {
		next, err := queueNext(screen, false)
		if next != nil && err == nil {
			err = errors.New("queued a stopped session")
		}
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("Queue did not reach renderer")
	}
	stopActionSync(screen)
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Stop did not cancel Queue: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop left Queue blocked")
	}
	response := httptest.NewRecorder()
	server.ServeMediaHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/"+utils.ConvertFilename(media), nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("canceled Queue retained media handler: %d", response.Code)
	}
	files, err := filepath.Glob(filepath.Join(dir, "go2tv-sub-*.srt"))
	if err != nil || len(files) != 0 || len(screen.tempFiles) != 0 {
		t.Fatalf("canceled Queue leaked subtitles: files=%v owned=%v error=%v", files, screen.tempFiles, err)
	}
}
