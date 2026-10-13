// Package radiometadata observes ICY titles independently of renderer audio.
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
	interval, err := strconv.Atoi(response.Header.Get("icy-metaint"))
	if err != nil || interval <= 0 || interval > MaxInterval {
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

func readTitles(ctx context.Context, cancel context.CancelCauseFunc, reader io.Reader, interval int, timeout time.Duration, onTitle func(string)) error {
	if interval <= 0 || interval > MaxInterval {
		return errors.New("ICY: invalid metadata interval")
	}
	reader = idleReader{Reader: reader, cancel: cancel, timeout: timeout}
	var length [1]byte
	block := make([]byte, 255*16)
	lastTitle := ""
	for {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		_, err := io.CopyN(io.Discard, reader, int64(interval))
		if err == nil {
			_, err = io.ReadFull(reader, length[:])
		}
		n := int(length[0]) * 16
		if err == nil && n > 0 {
			_, err = io.ReadFull(reader, block[:n])
		}
		if err != nil {
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			return fmt.Errorf("ICY metadata: %w", err)
		}
		if title, found := streamTitle(string(block[:n])); found && title != lastTitle {
			lastTitle = title
			if onTitle != nil {
				onTitle(title)
			}
		}
	}
}

func streamTitle(block string) (string, bool) {
	marker := "streamtitle="
	start := strings.Index(strings.ToLower(block), marker)
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
