//go:build !(android || ios)

package gui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"go2tv.app/screencast/capture"
)

const (
	// dlnaScreencastMediaType is the DLNA media type used for the live
	// MPEG-TS stream. The first path segment ("video") matches the Sink
	// protocol list advertised by most renderers (see parseProtocolInfo).
	dlnaScreencastMediaType     = "video/vnd.dlna.mpeg-tts"
	dlnaScreencastDefaultTimeout = 60 * time.Second
	dlnaScreencastMaxFrameRate   = 60
	dlnaScreencastHighResCapFPS  = 30
	dlnaScreencastMaxWidth       = 1280
	dlnaScreencastMaxHeight      = 720
	dlnaScreencastStartupBytes   = 188 * 8
	dlnaScreencastBytesPerPixel  = 4
)

// dlnaScreencastSession captures the desktop and produces a continuous
// MPEG-TS stream (H.264 video + AAC audio) exposed through Stream().
// DLNA/UPnP renderers consume it as a live video/mpeg stream.
type dlnaScreencastSession struct {
	stream     io.ReadCloser
	videoInput io.ReadCloser
	capture    *capture.Stream
	audioSrc   io.ReadCloser
	ownAudio   bool
	cmd        *exec.Cmd
	audioL     net.Listener
	done       chan error
	stderr     *lockedBuffer
	closeOnce  sync.Once
	closeErr   error
}

// Stream returns the live MPEG-TS output. Closing it is a no-op on purpose:
// closing the ffmpeg stdout pipe would SIGPIPE the encoder when a TV
// disconnects. The session owns the ffmpeg process lifetime.
func (s *dlnaScreencastSession) Stream() io.ReadCloser {
	if s == nil {
		return nil
	}
	return s.stream
}

func (s *dlnaScreencastSession) Done() <-chan error {
	if s == nil {
		return nil
	}
	return s.done
}

func (s *dlnaScreencastSession) StderrTail(n int) string {
	if s == nil || s.stderr == nil {
		return ""
	}
	return s.stderr.Tail(n)
}

func (s *dlnaScreencastSession) Close() error {
	if s == nil {
		return nil
	}

	s.closeOnce.Do(func() {
		if s.cmd != nil && s.cmd.Process != nil {
			if err := s.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				s.closeErr = errors.Join(s.closeErr, err)
			}
		}

		if s.audioL != nil {
			s.closeErr = errors.Join(s.closeErr, s.audioL.Close())
		}

		if s.videoInput != nil {
			s.closeErr = errors.Join(s.closeErr, s.videoInput.Close())
		}

		if s.ownAudio && s.audioSrc != nil {
			s.closeErr = errors.Join(s.closeErr, s.audioSrc.Close())
		}
	})

	return s.closeErr
}

func startDLNAScreencast(ffmpegPath string, logOutput io.Writer) (*dlnaScreencastSession, error) {
	includeAudio := true
	if v := strings.TrimSpace(os.Getenv("GO2TV_DLNA_SCREENCAST_AUDIO")); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			includeAudio = b
		}
	}

	captureStream, err := capture.Open(&capture.Options{
		StreamIndex:  0,
		IncludeAudio: includeAudio,
	})
	if err != nil {
		return nil, fmt.Errorf("dlna screencast capture open: %w", err)
	}

	cleanup := func() {
		_ = captureStream.Close()
	}

	fps := dlnaScreencastTargetFPS(captureStream)
	fpsArg := strconv.FormatUint(uint64(fps), 10)
	gopArg := strconv.FormatUint(uint64(fps), 10)

	videoInput := io.ReadCloser(captureStream)
	if runtime.GOOS == "linux" {
		pacer, err := newDLNAFramePacer(captureStream, fps)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("dlna screencast pacer: %w", err)
		}
		videoInput = pacer
	}

	var (
		audioSource io.ReadCloser
		ownAudio    bool
	)
	if includeAudio {
		audioSource = captureStream.Audio
		if audioSource == nil {
			audioSource = newDLNASilenceReader(48000, 2, 16, 20*time.Millisecond)
			ownAudio = true
		}
	}

	audioURL := ""
	var audioL net.Listener
	if audioSource != nil {
		audioL, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			if ownAudio {
				_ = audioSource.Close()
			}
			_ = videoInput.Close()
			cleanup()
			return nil, fmt.Errorf("dlna screencast audio listener: %w", err)
		}

		go func(l net.Listener, audio io.ReadCloser) {
			defer l.Close()
			conn, acceptErr := l.Accept()
			if acceptErr != nil {
				return
			}
			defer conn.Close()
			_, _ = io.Copy(conn, audio)
		}(audioL, audioSource)

		audioURL = "tcp://" + audioL.Addr().String()
	}

	videoFilter := fmt.Sprintf(
		"fps=%s,scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease,scale=trunc(iw/2)*2:trunc(ih/2)*2",
		fpsArg, dlnaScreencastMaxWidth, dlnaScreencastMaxHeight,
	)

	args := []string{
		"-hide_banner",
		"-fflags", "nobuffer",
		"-flags", "low_delay",
		"-probesize", "32",
		"-analyzeduration", "0",
		"-thread_queue_size", "2048",
		"-f", "rawvideo",
		"-pix_fmt", strings.ToLower(captureStream.PixelFormat),
		"-s", fmt.Sprintf("%dx%d", captureStream.Width, captureStream.Height),
		"-r", fpsArg,
		"-i", "pipe:0",
	}
	if audioURL != "" {
		args = append(args,
			"-thread_queue_size", "8192",
			"-probesize", "32",
			"-analyzeduration", "0",
			"-f", "s16le",
			"-ar", "48000",
			"-ac", "2",
			"-i", audioURL,
			"-map", "0:v:0",
			"-map", "1:a:0",
		)
	} else {
		args = append(args,
			"-map", "0:v:0",
			"-an",
		)
	}
	args = append(args,
		"-vf", videoFilter,
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-b:v", "4000k",
		"-maxrate", "5000k",
		"-bufsize", "10000k",
		"-pix_fmt", "yuv420p",
		"-g", gopArg,
		"-keyint_min", gopArg,
		"-sc_threshold", "0",
	)
	if audioURL != "" {
		args = append(args,
			"-af", "aresample=async=1:min_hard_comp=0.100:first_pts=0",
			"-c:a", "aac",
			"-ar", "48000",
			"-ac", "2",
		)
	}
	args = append(args,
		"-f", "mpegts",
		"-muxdelay", "0",
		"pipe:1",
	)

	pr, pw, err := os.Pipe()
	if err != nil {
		if audioL != nil {
			_ = audioL.Close()
		}
		if ownAudio && audioSource != nil {
			_ = audioSource.Close()
		}
		_ = videoInput.Close()
		cleanup()
		return nil, fmt.Errorf("dlna screencast pipe: %w", err)
	}

	stderrBuf := &lockedBuffer{}
	stderrWriter := io.Writer(stderrBuf)
	if logOutput != nil {
		stderrWriter = io.MultiWriter(logOutput, stderrBuf)
	}

	cmd := exec.Command(ffmpegPath, args...)
	cmd.Stdin = videoInput
	cmd.Stdout = pw
	cmd.Stderr = stderrWriter

	if logOutput != nil {
		_, _ = fmt.Fprintf(logOutput, "dlna screencast ffmpeg: %s %s\n", ffmpegPath, strings.Join(args, " "))
	}

	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()
		if audioL != nil {
			_ = audioL.Close()
		}
		if ownAudio && audioSource != nil {
			_ = audioSource.Close()
		}
		_ = videoInput.Close()
		cleanup()
		return nil, fmt.Errorf("dlna screencast ffmpeg start: %w", err)
	}
	// The child owns the write end now.
	_ = pw.Close()

	probe := newTSStreamProbe()
	go probe.run(pr)

	done := make(chan error, 1)
	s := &dlnaScreencastSession{
		stream:     &dlnaTSPrefixReader{probe: probe, src: pr},
		videoInput: videoInput,
		capture:    captureStream,
		audioSrc:   audioSource,
		ownAudio:   ownAudio,
		cmd:        cmd,
		audioL:     audioL,
		done:       done,
		stderr:     stderrBuf,
	}

	go func(c *exec.Cmd, pr *os.File) {
		done <- c.Wait()
		close(done)
		_ = pr.Close()
		_ = s.Close()
	}(cmd, pr)

	if err := waitForTSStream(probe, done, s, dlnaScreencastDefaultTimeout); err != nil {
		_ = s.Close()
		return nil, err
	}

	return s, nil
}

// dlnaTSPrefixReader yields the startup bytes collected by the probe before
// switching to the raw ffmpeg stdout pipe.
type dlnaTSPrefixReader struct {
	probe *tsStreamProbe
	src   io.ReadCloser
}

func (r *dlnaTSPrefixReader) Read(p []byte) (int, error) {
	if n := r.probe.ReadFromBuffer(p); n > 0 {
		return n, nil
	}
	return r.src.Read(p)
}

func (r *dlnaTSPrefixReader) Close() error {
	return nil
}

// tsStreamProbe buffers the first bytes of the MPEG-TS stream so we can
// verify ffmpeg is actually producing output before telling the TV to play.
type tsStreamProbe struct {
	mu         sync.Mutex
	buf        bytes.Buffer
	ready      chan struct{}
	signalSent bool
	readErr    error
}

func newTSStreamProbe() *tsStreamProbe {
	return &tsStreamProbe{ready: make(chan struct{})}
}

func (p *tsStreamProbe) run(pr io.Reader) {
	defer p.signal()

	tmp := make([]byte, 32*1024)
	for {
		n, err := pr.Read(tmp)
		p.mu.Lock()
		if n > 0 {
			p.buf.Write(tmp[:n])
		}
		if err != nil {
			p.readErr = err
		}
		ready := p.buf.Len() >= dlnaScreencastStartupBytes
		p.mu.Unlock()
		if ready || err != nil {
			return
		}
	}
}

func (p *tsStreamProbe) signal() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.signalSent {
		p.signalSent = true
		close(p.ready)
	}
}

// ReadFromBuffer drains the buffered startup bytes (thread-safe).
func (p *tsStreamProbe) ReadFromBuffer(dst []byte) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return copyToConsuming(dst, &p.buf)
}

// Snapshot returns a copy of the buffered bytes (thread-safe).
func (p *tsStreamProbe) Buffered() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.buf.Len()
}

// copyToConsuming copies from buf into dst, consuming what was copied so the
// probe buffer is drained as the stream is consumed.
func copyToConsuming(dst []byte, buf *bytes.Buffer) int {
	if buf.Len() == 0 || len(dst) == 0 {
		return 0
	}
	n := copy(dst, buf.Bytes())
	buf.Next(n)
	return n
}

func waitForTSStream(probe *tsStreamProbe, done <-chan error, s *dlnaScreencastSession, timeout time.Duration) error {
	tail := func() string {
		if s == nil {
			return "no ffmpeg stderr output"
		}
		return s.StderrTail(300)
	}

	select {
	case <-probe.ready:
		if probe.Buffered() >= dlnaScreencastStartupBytes {
			return nil
		}
		// ffmpeg closed the stream before producing enough data.
		select {
		case err, ok := <-done:
			if !ok || err == nil {
				return errors.New("dlna screencast stream not initialized")
			}
			return fmt.Errorf("dlna screencast ffmpeg exited: %w: %s", err, tail())
		case <-time.After(2 * time.Second):
		}
		return fmt.Errorf("dlna screencast stream not initialized: %s", tail())
	case err, ok := <-done:
		if !ok {
			return errors.New("dlna screencast stream not initialized")
		}
		if err != nil {
			return fmt.Errorf("dlna screencast ffmpeg exited: %w: %s", err, tail())
		}
		return errors.New("dlna screencast stream not initialized")
	case <-time.After(timeout):
		return fmt.Errorf("dlna screencast stream not initialized: %s", tail())
	}
}

func dlnaScreencastTargetFPS(stream *capture.Stream) uint32 {
	frameRate := stream.FrameRate
	if frameRate == 0 {
		frameRate = dlnaScreencastMaxFrameRate
	}
	target := frameRate
	if target > dlnaScreencastMaxFrameRate {
		target = dlnaScreencastMaxFrameRate
	}
	if stream.Width*stream.Height > 1920*1080 && target > dlnaScreencastHighResCapFPS {
		target = dlnaScreencastHighResCapFPS
	}
	return target
}

// dlnaFramePacer throttles raw frames from the capture source to the target
// frame rate, dropping stale frames when the consumer falls behind.
type dlnaFramePacer struct {
	src       io.ReadCloser
	pr        *io.PipeReader
	pw        *io.PipeWriter
	closeOnce sync.Once
	closeErr  error
}

func newDLNAFramePacer(stream *capture.Stream, fps uint32) (io.ReadCloser, error) {
	if stream == nil || stream.ReadCloser == nil {
		return nil, errors.New("nil stream")
	}
	if fps == 0 {
		return stream, nil
	}

	frameSize, err := dlnaRawFrameSize(stream.Width, stream.Height, stream.PixelFormat)
	if err != nil {
		return nil, err
	}
	if frameSize == 0 {
		return stream, nil
	}

	pr, pw := io.Pipe()
	p := &dlnaFramePacer{
		src: stream,
		pr:  pr,
		pw:  pw,
	}
	go p.run(frameSize, fps)
	return p, nil
}

func dlnaRawFrameSize(width, height uint32, pixelFormat string) (int, error) {
	if width == 0 || height == 0 {
		return 0, errors.New("invalid raw frame size")
	}
	switch pixelFormat {
	case "", capture.PixelFormatBGRA:
		size := uint64(width) * uint64(height) * dlnaScreencastBytesPerPixel
		if size == 0 || size > uint64(^uint(0)>>1) {
			return 0, fmt.Errorf("raw frame too large: %dx%d", width, height)
		}
		return int(size), nil
	default:
		return 0, fmt.Errorf("unsupported raw pixel format %q", pixelFormat)
	}
}

func (p *dlnaFramePacer) Read(buf []byte) (int, error) {
	return p.pr.Read(buf)
}

func (p *dlnaFramePacer) Close() error {
	p.closeOnce.Do(func() {
		p.closeErr = errors.Join(p.src.Close(), p.pw.Close(), p.pr.Close())
	})
	return p.closeErr
}

func (p *dlnaFramePacer) run(frameSize int, fps uint32) {
	frameCh := make(chan []byte, 1)
	srcErrCh := make(chan error, 1)

	go func() {
		for {
			frame := make([]byte, frameSize)
			if _, err := io.ReadFull(p.src, frame); err != nil {
				srcErrCh <- err
				close(frameCh)
				return
			}

			select {
			case frameCh <- frame:
			default:
				select {
				case <-frameCh:
				default:
				}
				frameCh <- frame
			}
		}
	}()

	frameInterval := time.Second / time.Duration(fps)
	if frameInterval <= 0 {
		frameInterval = time.Second / time.Duration(dlnaScreencastMaxFrameRate)
	}

	var (
		latest []byte
		srcErr error
	)

	waitForFirst := true
	for waitForFirst {
		select {
		case frame, ok := <-frameCh:
			if !ok {
				_ = p.pw.CloseWithError(srcErr)
				return
			}
			latest = frame
			waitForFirst = false
		case srcErr = <-srcErrCh:
			_ = p.pw.CloseWithError(srcErr)
			return
		}
	}

	if _, err := p.pw.Write(latest); err != nil {
		_ = p.pw.CloseWithError(err)
		return
	}

	ticker := time.NewTicker(frameInterval)
	defer ticker.Stop()

	for {
		select {
		case srcErr = <-srcErrCh:
			_ = p.pw.CloseWithError(srcErr)
			return
		case frame, ok := <-frameCh:
			if ok {
				latest = frame
			}
		case <-ticker.C:
			select {
			case frame, ok := <-frameCh:
				if ok {
					latest = frame
				}
			default:
			}
			if _, err := p.pw.Write(latest); err != nil {
				_ = p.pw.CloseWithError(err)
				return
			}
		}
	}
}

// dlnaSilenceReader yields a constant stream of PCM silence so the encoder
// always has an audio track even when the system cannot capture audio.
type dlnaSilenceReader struct {
	bytesPerSecond int
	chunkBytes     int
	closed         chan struct{}
	closeOnce      sync.Once
}

func newDLNASilenceReader(sampleRate, channels, bitsPerSample int, chunkDuration time.Duration) io.ReadCloser {
	bytesPerSecond := sampleRate * channels * (bitsPerSample / 8)
	if bytesPerSecond <= 0 {
		bytesPerSecond = 48000 * 2 * 2
	}
	chunkBytes := int((int64(bytesPerSecond) * chunkDuration.Milliseconds()) / 1000)
	if chunkBytes <= 0 {
		chunkBytes = 3840
	}
	return &dlnaSilenceReader{
		bytesPerSecond: bytesPerSecond,
		chunkBytes:     chunkBytes,
		closed:         make(chan struct{}),
	}
}

func (r *dlnaSilenceReader) Read(p []byte) (int, error) {
	select {
	case <-r.closed:
		return 0, io.EOF
	default:
	}
	if len(p) == 0 {
		return 0, nil
	}

	n := r.chunkBytes
	if n > len(p) {
		n = len(p)
	}
	if n <= 0 {
		n = len(p)
	}
	clear(p[:n])

	wait := time.Duration(int64(n) * int64(time.Second) / int64(r.bytesPerSecond))
	if wait <= 0 {
		return n, nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-r.closed:
		return 0, io.EOF
	case <-timer.C:
		return n, nil
	}
}

func (r *dlnaSilenceReader) Close() error {
	r.closeOnce.Do(func() {
		close(r.closed)
	})
	return nil
}

// lockedBuffer is a concurrency-safe stderr capture with a tail window.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) Tail(n int) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := strings.TrimSpace(b.buf.String())
	if s == "" {
		return "no ffmpeg stderr output"
	}
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
