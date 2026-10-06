package utils

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"go2tv.app/go2tv/v2/internal/mkvsubs"
)

const (
	torrentCaptionWindow = 10
	torrentCaptionFPS    = 25
)

type subtitleBurn struct {
	filter  string
	overlay string
	origin  float64
	cleanup func()
}

// prepareSubtitleBurn keeps external captions on the existing file-filter path.
// Embedded torrent captions are rendered in bounded windows: FFmpeg's subtitles
// filter otherwise reads the entire track before producing its first frame.
func prepareSubtitleBurn(ctx context.Context, opts *TranscodeOptions) (*subtitleBurn, error) {
	burn := &subtitleBurn{cleanup: func() {}}
	if opts.SubsPath != "" || opts.TorrentSource == nil {
		var err error
		burn.filter, err = subtitleBurnFilter(opts.FFmpegPath, opts.SubsPath, opts.SubtitleSize)
		return burn, err
	}
	for _, filter := range []string{"subtitles", "overlay", "scale2ref"} {
		if !ffmpegFilterAvailable(opts.FFmpegPath, filter) {
			return burn, nil
		}
	}
	parser := mkvsubs.New(opts.TorrentSource)
	origin, err := parser.PrepareBurn(ctx)
	if errors.Is(err, mkvsubs.ErrNoSubtitles) {
		return burn, nil
	}
	if err != nil {
		return burn, fmt.Errorf("prepare torrent subtitles: %w", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return burn, fmt.Errorf("listen for torrent captions: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	var requests sync.WaitGroup
	var requestMu sync.Mutex
	closing := false
	server := &http.Server{ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
		// Large socket buffers could let compressed blank frames pull many
		// future windows before the main encoder consumes the current one.
		if tcp, ok := conn.(*net.TCPConn); ok {
			if err := tcp.SetWriteBuffer(4 << 10); err != nil {
				opts.LogError("prepareSubtitleBurn", "limit caption buffer", err)
			}
		}
		return ctx
	}, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMu.Lock()
		if closing {
			requestMu.Unlock()
			return
		}
		requests.Add(1)
		requestMu.Unlock()
		defer requests.Done()
		requestCtx, cancelRequest := context.WithCancel(r.Context())
		stop := context.AfterFunc(ctx, cancelRequest)
		defer stop()
		defer cancelRequest()
		// A fresh parser and zero-based image sequence make startup retries
		// deterministic. Only the current window reaches the renderer.
		written := &countingWriter{w: w}
		if err := renderTorrentCaptions(requestCtx, written, opts, origin); err != nil && requestCtx.Err() == nil {
			opts.LogError("renderTorrentCaptions", "torrent subtitle rendering failed", err)
			if written.n == 0 {
				// image2pipe needs dimensions even when caption preparation
				// fails. One transparent frame lets eof_action=pass preserve
				// the base video instead of aborting before its first frame.
				if err := png.Encode(w, image.NewNRGBA(image.Rect(0, 0, 640, 360))); err != nil {
					opts.LogError("renderTorrentCaptions", "caption fallback failed", err)
				}
			}
		}
	})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(listener)
	}()
	burn.overlay = "http://" + listener.Addr().String() + "/captions"
	burn.origin = origin + float64(opts.SeekSeconds)
	burn.cleanup = func() {
		requestMu.Lock()
		closing = true
		requestMu.Unlock()
		cancel()
		_ = server.Close()
		<-done
		requests.Wait()
	}
	return burn, nil
}

func (b *subtitleBurn) enabled() string {
	if b.overlay != "" {
		return "overlay"
	}
	return b.filter
}

// videoArgs adds the progressive PNG input only for embedded torrent captions.
// Preserve input PTS, including nonzero media origins and transcoded seeks.
func (b *subtitleBurn) videoArgs(base, tail string) []string {
	graph := fmt.Sprintf("[0:v]%s[base];[1:v]setpts=PTS+%s/TB[sub];[sub][base]scale2ref[scaled][video];[video][scaled]overlay=eof_action=pass", base, strconv.FormatFloat(b.origin, 'f', -1, 64))
	graph = joinVideoFilters(graph, tail) + "[out]"
	return []string{
		"-f", "image2pipe", "-framerate", strconv.Itoa(torrentCaptionFPS),
		"-c:v", "png", "-probesize", "32", "-analyzeduration", "0",
		"-i", b.overlay, "-filter_complex", graph,
		"-map", "[out]", "-map", "0:a:0?",
	}
}

func renderTorrentCaptions(ctx context.Context, w io.Writer, opts *TranscodeOptions, origin float64) error {
	parser := mkvsubs.New(opts.TorrentSource)
	if _, err := parser.PrepareBurn(ctx); err != nil {
		return err
	}
	file, err := os.CreateTemp("", "go2tv-torrent-sub-*.srt")
	if err != nil {
		return err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	defer os.Remove(path)
	// Parser validates UTF-8. Keep the same styling as external burn-in.
	filter := fmt.Sprintf("subtitles='%s'%s:alpha=1", escapeFFmpegPath(path), subtitleBurnStyle(opts.SubtitleSize))
	for start := origin + float64(opts.SeekSeconds); ; start += torrentCaptionWindow {
		if err := ctx.Err(); err != nil {
			return err
		}
		cues, err := parser.Window(ctx, start, start+torrentCaptionWindow)
		if err != nil {
			return err
		}
		var captions bytes.Buffer
		for i, cue := range cues {
			from := max(0, cue.Start-start)
			until := min(torrentCaptionWindow, cue.End-start)
			fmt.Fprintf(&captions, "%d\n%s --> %s\n%s\n\n", i+1,
				strings.ReplaceAll(webVTTTimestamp(int64(from*1000)), ".", ","),
				strings.ReplaceAll(webVTTTimestamp(int64(until*1000)), ".", ","), cue.Text)
		}
		if len(cues) == 0 {
			// A future, invisible cue lets libass open an empty caption window.
			captions.WriteString("1\n00:00:11,000 --> 00:00:12,000\n_\n")
		}
		if err := os.WriteFile(path, captions.Bytes(), 0600); err != nil {
			return err
		}
		command := exec.CommandContext(ctx, opts.FFmpegPath,
			"-nostdin", "-v", "error", "-f", "lavfi", "-i",
			fmt.Sprintf("color=c=black@0:size=640x360:rate=%d:duration=%d,format=rgba", torrentCaptionFPS, torrentCaptionWindow),
			"-vf", filter, "-c:v", "png", "-threads", "1", "-compression_level", "1", "-f", "image2pipe", "pipe:1")
		setSysProcAttr(command)
		command.Stdout = w
		var stderr bytes.Buffer
		command.Stderr = &stderr
		if err := command.Run(); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("render torrent captions: %w: %s", err, tailFFmpegStderr(stderr.String(), 240))
		}
	}
}
