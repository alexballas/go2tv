// Package radiometadata extracts ICY track titles from radio streams.
package radiometadata

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

const (
	MaxInterval   = 256 << 10
	maxTitleRunes = 256
	readTimeout   = 45 * time.Second
)

var client = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout:   5 * time.Second,
	ResponseHeaderTimeout: 5 * time.Second,
	IdleConnTimeout:       30 * time.Second,
}}

// StreamTitles requests ICY metadata on a separate, cancelable connection.
// Audio is discarded here; playback connections remain entirely untouched.
// Stations without a valid icy-metaint header return without reading audio.
func StreamTitles(ctx context.Context, streamURL string, onTitle func(string)) error {
	parsed, err := url.Parse(streamURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("ICY: invalid stream URL")
	}
	requestCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(context.Canceled)
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, streamURL, nil)
	if err != nil {
		return fmt.Errorf("ICY request: %w", err)
	}
	request.Header = http.Header{"Icy-MetaData": {"1"}, "User-Agent": {"Go2TV"}}
	acquireTimer := time.AfterFunc(10*time.Second, func() { cancel(context.DeadlineExceeded) })
	response, err := client.Do(request)
	acquireTimer.Stop()
	if err != nil {
		if requestCtx.Err() != nil {
			return context.Cause(requestCtx)
		}
		return fmt.Errorf("ICY stream: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("ICY status: %d", response.StatusCode)
	}
	interval, ok := Interval(response.Header.Get("icy-metaint"))
	if !ok {
		return nil
	}
	return readTitles(requestCtx, cancel, response.Body, interval, readTimeout, onTitle)
}

// Timeout only stalled reads: a large metadata interval can take longer than
// readTimeout to arrive even while low-bitrate audio flows continuously.
type idleReader struct {
	io.Reader
	cancel  context.CancelCauseFunc
	timeout time.Duration
}

func (r idleReader) Read(p []byte) (int, error) {
	done := make(chan struct{})
	timer := time.AfterFunc(r.timeout, func() {
		r.cancel(context.DeadlineExceeded)
		close(done)
	})
	n, err := r.Reader.Read(p)
	if !timer.Stop() {
		<-done
	}
	return n, err
}

// Interval parses an icy-metaint header within the supported range.
func Interval(header string) (int, bool) {
	interval, err := strconv.Atoi(strings.TrimSpace(header))
	return interval, err == nil && interval > 0 && interval <= MaxInterval
}

// Reader strips ICY metadata blocks from relayed audio and reports changed
// titles, so renderers receive plain audio from a single upstream connection.
type Reader struct {
	reader    io.Reader
	interval  int
	remaining int
	block     []byte
	lastTitle string
	onTitle   func(string)
}

// NewReader expects an interval accepted by Interval.
func NewReader(reader io.Reader, interval int, onTitle func(string)) *Reader {
	return &Reader{reader: reader, interval: interval, remaining: interval, block: make([]byte, 255*16), onTitle: onTitle}
}

func (r *Reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.remaining == 0 {
		if err := r.readMetadata(); err != nil {
			return 0, err
		}
	}
	n, err := r.reader.Read(p[:min(len(p), r.remaining)])
	r.remaining -= n
	return n, err
}

func (r *Reader) readMetadata() error {
	var length [1]byte
	if _, err := io.ReadFull(r.reader, length[:]); err != nil {
		return err
	}
	n := int(length[0]) * 16
	if _, err := io.ReadFull(r.reader, r.block[:n]); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return err
	}
	r.remaining = r.interval
	if title, found := streamTitle(r.block[:n]); found && title != r.lastTitle {
		r.lastTitle = title
		if r.onTitle != nil {
			r.onTitle(title)
		}
	}
	return nil
}

// WriterInterval is the metadata interval go2tv advertises to renderers.
const WriterInterval = 16000

// Writer interleaves ICY metadata into relayed audio so renderers that
// request it show the current track without reloading the stream.
// Unchanged titles are sent as empty blocks, as Icecast does.
type Writer struct {
	writer    io.Writer
	interval  int
	remaining int
	title     func() string
	sent      string
}

// NewWriter expects an interval accepted by Interval.
func NewWriter(writer io.Writer, interval int, title func() string) *Writer {
	return &Writer{writer: writer, interval: interval, remaining: interval, title: title}
}

// Write reports audio bytes only; metadata blocks are not counted.
func (w *Writer) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		if w.remaining == 0 {
			if err := w.writeMetadata(); err != nil {
				return written, err
			}
			w.remaining = w.interval
		}
		n, err := w.writer.Write(p[:min(len(p), w.remaining)])
		written += n
		w.remaining -= n
		p = p[n:]
		if err != nil {
			return written, err
		}
	}
	return written, nil
}

func (w *Writer) writeMetadata() error {
	title := w.title()
	if title == w.sent {
		_, err := w.writer.Write([]byte{0})
		return err
	}
	if _, err := w.writer.Write(metadataBlock(title)); err != nil {
		return err
	}
	w.sent = title
	return nil
}

func metadataBlock(title string) []byte {
	// ICY has no escaping; the closing "';" would end the title early.
	title = strings.ReplaceAll(strings.ToValidUTF8(title, "�"), "';", "' ;")
	const maxLength = 255*16 - len("StreamTitle='';")
	for len(title) > maxLength {
		_, size := utf8.DecodeLastRuneInString(title)
		title = title[:len(title)-size]
	}
	content := "StreamTitle='" + title + "';"
	blocks := (len(content) + 15) / 16
	block := make([]byte, 1+blocks*16)
	block[0] = byte(blocks)
	copy(block[1:], content)
	return block
}

func readTitles(ctx context.Context, cancel context.CancelCauseFunc, reader io.Reader, interval int, timeout time.Duration, onTitle func(string)) error {
	if _, ok := Interval(strconv.Itoa(interval)); !ok {
		return errors.New("ICY: invalid metadata interval")
	}
	icy := NewReader(idleReader{Reader: reader, cancel: cancel, timeout: timeout}, interval, onTitle)
	buffer := make([]byte, 32<<10)
	for {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		if _, err := icy.Read(buffer); err != nil {
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			return fmt.Errorf("ICY metadata: %w", err)
		}
	}
}

func streamTitle(raw []byte) (string, bool) {
	block := string(raw)
	// Many Shoutcast servers send legacy Windows-1252/Latin-1 titles.
	if !utf8.Valid(raw) {
		if decoded, err := charmap.Windows1252.NewDecoder().Bytes(raw); err == nil {
			block = string(decoded)
		}
	}
	marker := "streamtitle="
	start := -1
	for i := 0; i+len(marker) <= len(block); i++ {
		if strings.EqualFold(block[i:i+len(marker)], marker) {
			start = i
			break
		}
	}
	if start < 0 {
		return "", false
	}
	value := strings.TrimLeft(block[start+len(marker):], " ")
	if len(value) < 2 || (value[0] != '\'' && value[0] != '"') {
		return "", false
	}
	end := strings.Index(value[1:], string(value[0])+";")
	if end < 0 {
		return "", false
	}
	title := strings.ToValidUTF8(value[1:end+1], "�")
	title = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, title))
	runes := []rune(title)
	if len(runes) > maxTitleRunes {
		title = string(runes[:maxTitleRunes])
	}
	return title, true
}
