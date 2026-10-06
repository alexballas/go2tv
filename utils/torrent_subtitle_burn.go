package utils

import (
	"bytes"
	"cmp"
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
	"path/filepath"
	"slices"
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
	if selected := opts.EmbeddedSubtitle; selected != nil && opts.SubsPath == "" {
		if selected.Track < 0 || selected.Path == "" {
			return burn, fmt.Errorf("invalid embedded subtitle source")
		}
		if ffmpegFilterAvailable(opts.FFmpegPath, "subtitles") {
			burn.filter = fmt.Sprintf("subtitles='%s':si=%d", escapeFFmpegPath(selected.Path), selected.Track)
		}
		return burn, nil
	}
	if opts.SubsPath != "" || opts.TorrentSource == nil {
		fontsDir := ""
		if styledSubtitlePath(opts.SubsPath) && ffmpegFilterAvailable(opts.FFmpegPath, "subtitles") {
			dir := opts.SubtitleFontsDir
			if dir == "" {
				dir = filepath.Dir(opts.SubsPath)
			}
			var err error
			fontsDir, err = copySubtitleFonts(ctx, dir)
			if err != nil {
				return burn, fmt.Errorf("prepare subtitle fonts: %w", err)
			}
			if fontsDir != "" {
				burn.cleanup = func() { _ = os.RemoveAll(fontsDir) }
			}
		}
		var err error
		burn.filter, err = subtitleBurnFilter(opts.FFmpegPath, opts.SubsPath, opts.SubtitleSize, fontsDir)
		if err != nil {
			burn.cleanup()
		}
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
	assets, err := os.MkdirTemp("", "go2tv-torrent-captions-*")
	if err != nil {
		return burn, err
	}
	if err := parser.ExtractFonts(ctx, assets); err != nil {
		_ = os.RemoveAll(assets)
		return burn, fmt.Errorf("extract subtitle fonts: %w", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = os.RemoveAll(assets)
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
		if err := renderTorrentCaptions(requestCtx, written, opts, origin, assets); err != nil && requestCtx.Err() == nil {
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
		_ = os.RemoveAll(assets)
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

func renderTorrentCaptions(ctx context.Context, w io.Writer, opts *TranscodeOptions, origin float64, fontsDir string) error {
	parser := mkvsubs.New(opts.TorrentSource)
	if _, err := parser.PrepareBurn(ctx); err != nil {
		return err
	}
	metadata := parser.BurnMetadata()
	ext := ".srt"
	width, height := 640, 360
	if metadata.Styled() {
		ext = ".ass"
		width, height = subtitleCanvasSize(metadata.Width, metadata.Height)
	}
	file, err := os.CreateTemp("", "go2tv-torrent-sub-*"+ext)
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
	if metadata.Styled() {
		filter = fmt.Sprintf("subtitles='%s':fontsdir='%s':alpha=1", escapeFFmpegPath(path), escapeFFmpegPath(fontsDir))
	}
	for start := origin + float64(opts.SeekSeconds); ; start += torrentCaptionWindow {
		if err := ctx.Err(); err != nil {
			return err
		}
		cues, err := parser.Window(ctx, start, start+torrentCaptionWindow)
		if err != nil {
			return err
		}
		var captions bytes.Buffer
		windowFilter := filter
		if metadata.Styled() {
			if err := writeASSWindow(&captions, metadata, cues); err != nil {
				return err
			}
			// Evaluate the original event timeline, even for a caption that
			// started before this window/seek. Clipping its start would restart
			// fades, karaoke, transforms and movement every ten seconds.
			windowFilter = fmt.Sprintf("setpts=PTS+%.9f/TB,%s,setpts=PTS-STARTPTS", start, filter)
		} else {
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
		}
		if err := os.WriteFile(path, captions.Bytes(), 0600); err != nil {
			return err
		}
		command := exec.CommandContext(ctx, opts.FFmpegPath,
			"-nostdin", "-v", "error", "-f", "lavfi", "-i",
			fmt.Sprintf("color=c=black@0:size=%dx%d:rate=%d:duration=%d,format=rgba", width, height, torrentCaptionFPS, torrentCaptionWindow),
			"-vf", windowFilter, "-c:v", "png", "-threads", "1", "-compression_level", "1", "-f", "image2pipe", "pipe:1")
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

func subtitleCanvasSize(width, height int) (int, int) {
	if width <= 0 || height <= 0 {
		return 640, 360
	}
	scale := min(1.0, 1920/float64(width), 1080/float64(height))
	return max(2, int(float64(width)*scale)/2*2), max(2, int(float64(height)*scale)/2*2)
}

func writeASSWindow(w io.Writer, metadata mkvsubs.BurnMetadata, cues []mkvsubs.Cue) error {
	header := metadata.Header
	if i := strings.Index(strings.ToLower(header), "[events]"); i >= 0 {
		header = header[:i]
	}
	ssa := metadata.Codec == "S_TEXT/SSA" || (strings.Contains(strings.ToLower(header), "[v4 styles]") && !strings.Contains(strings.ToLower(header), "[v4+ styles]"))
	first := "Layer"
	if ssa {
		first = "Marked"
	}
	if _, err := fmt.Fprintf(w, "%s\n[Events]\nFormat: %s, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n", header, first); err != nil {
		return err
	}
	// ASS collision handling depends on file read order, not packet timestamp.
	type event struct {
		cue    mkvsubs.Cue
		fields []string
		order  int
	}
	events := make([]event, 0, len(cues))
	for _, cue := range cues {
		fields := strings.SplitN(cue.Text, ",", 9)
		if len(fields) != 9 {
			return fmt.Errorf("invalid ASS subtitle packet")
		}
		order, err := strconv.Atoi(fields[0])
		if err != nil || order < 0 {
			return fmt.Errorf("invalid ASS subtitle read order: %q", fields[0])
		}
		events = append(events, event{cue, fields, order})
	}
	slices.SortStableFunc(events, func(a, b event) int { return cmp.Compare(a.order, b.order) })
	for _, event := range events {
		cue, fields := event.cue, event.fields
		layer := fields[1]
		if ssa {
			layer = "Marked=0"
		}
		if _, err := fmt.Fprintf(w, "Dialogue: %s,%s,%s,%s\n", layer, assTimestamp(cue.Start), assTimestamp(cue.End), strings.Join(fields[2:], ",")); err != nil {
			return err
		}
	}
	return nil
}

func assTimestamp(seconds float64) string {
	centiseconds := int64(seconds*100 + 0.5)
	return fmt.Sprintf("%d:%02d:%02d.%02d", centiseconds/360000, centiseconds/6000%60, centiseconds/100%60, centiseconds%100)
}
