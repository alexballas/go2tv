package utils

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var ErrBadStatus = errors.New("streamURL bad status code")

const (
	streamHTTPPreparationTimeout    = 20 * time.Second
	streamHTTPDialTimeout           = 5 * time.Second
	streamHTTPKeepAlive             = 30 * time.Second
	streamHTTPTLSHandshakeTimeout   = 5 * time.Second
	streamHTTPResponseHeaderTimeout = 10 * time.Second
	streamHTTPExpectContinueTimeout = time.Second
	streamHTTPIdleConnTimeout       = 90 * time.Second
)

var streamHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   streamHTTPDialTimeout,
			KeepAlive: streamHTTPKeepAlive,
		}).DialContext,
		TLSHandshakeTimeout:   streamHTTPTLSHandshakeTimeout,
		ResponseHeaderTimeout: streamHTTPResponseHeaderTimeout,
		ExpectContinueTimeout: streamHTTPExpectContinueTimeout,
		IdleConnTimeout:       streamHTTPIdleConnTimeout,
	},
}

// Playback bodies keep the caller's cancellation without a total deadline.
// Acquisition, MIME sniffing and image downloads each have a bounded deadline.
type streamURLBody struct {
	io.ReadCloser
	cancel      context.CancelCauseFunc
	stopTimeout func() bool
	closeOnce   sync.Once
	closeErr    error
}

func (b *streamURLBody) startTimeout() {
	done := make(chan struct{})
	timer := time.AfterFunc(streamHTTPPreparationTimeout, func() {
		b.cancel(context.DeadlineExceeded)
		close(done)
	})
	var once sync.Once
	stopped := false
	b.stopTimeout = func() bool {
		once.Do(func() {
			stopped = timer.Stop()
			if !stopped {
				// Finish an already-started callback before handing off playback.
				<-done
			}
		})
		return stopped
	}
}

func (b *streamURLBody) Close() error {
	b.closeOnce.Do(func() {
		b.stopTimeout()
		b.cancel(context.Canceled)
		b.closeErr = b.ReadCloser.Close()
	})
	return b.closeErr
}

func streamURLResponse(ctx context.Context, s string) (*http.Response, error) {
	_, err := url.ParseRequestURI(s)
	if err != nil {
		return nil, fmt.Errorf("streamURL failed to parse url: %w", err)
	}
	if ctx == nil {
		return nil, errors.New("streamURL: nil context")
	}

	requestCtx, cancel := context.WithCancelCause(ctx)
	body := &streamURLBody{cancel: cancel}
	body.startTimeout()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, s, nil)
	if err != nil {
		body.stopTimeout()
		cancel(context.Canceled)
		return nil, fmt.Errorf("streamURL failed to call NewRequest: %w", err)
	}

	resp, err := streamHTTPClient.Do(req)
	acquired := body.stopTimeout()
	if err != nil {
		cancel(context.Canceled)
		return nil, fmt.Errorf("streamURL failed to client.Do: %w", err)
	}
	body.ReadCloser = resp.Body
	resp.Body = body
	if !acquired {
		body.Close()
		return nil, fmt.Errorf("streamURL failed to acquire response: %w", context.DeadlineExceeded)
	}

	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, ErrBadStatus
	}

	return resp, nil
}

func normalizeContentType(v string) string {
	if v == "" {
		return ""
	}

	mt, _, err := mime.ParseMediaType(v)
	if err == nil {
		return strings.ToLower(strings.TrimSpace(mt))
	}

	parts := strings.Split(v, ";")
	return strings.ToLower(strings.TrimSpace(parts[0]))
}

func shouldSniffContentType(mediaType string) bool {
	switch mediaType {
	case "", "/", "application/octet-stream", "binary/octet-stream", "text/plain":
		return true
	default:
		return false
	}
}

// StreamURL returns the response body for the input media URL.
func StreamURL(ctx context.Context, s string) (io.ReadCloser, error) {
	resp, err := streamURLResponse(ctx, s)
	if err != nil {
		return nil, err
	}

	return resp.Body, nil
}

// StreamURLWithMime returns the stream body and inferred media type from
// response headers first, with body sniffing fallback.
func StreamURLWithMime(ctx context.Context, s string) (io.ReadCloser, string, error) {
	resp, err := streamURLResponse(ctx, s)
	if err != nil {
		return nil, "", err
	}

	mediaType := normalizeContentType(resp.Header.Get("Content-Type"))
	body := resp.Body.(*streamURLBody)
	if !shouldSniffContentType(mediaType) {
		if strings.Contains(mediaType, "image") {
			body.startTimeout()
		}
		return resp.Body, mediaType, nil
	}

	head := make([]byte, 261)
	body.startTimeout()
	n, err := io.ReadFull(resp.Body, head)
	if !body.stopTimeout() {
		resp.Body.Close()
		return nil, "", fmt.Errorf("streamURL failed to read body for mime detection: %w", context.DeadlineExceeded)
	}
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		resp.Body.Close()
		return nil, "", fmt.Errorf("streamURL failed to read body for mime detection: %w", err)
	}

	sniffedType := ""
	if n > 0 {
		sniffedType, _ = GetMimeDetailsFromBytes(head[:n])
	}

	if sniffedType != "" && sniffedType != "/" {
		mediaType = sniffedType
	}
	if strings.Contains(mediaType, "image") {
		body.startTimeout()
	}

	return struct {
		io.Reader
		io.Closer
	}{
		Reader: io.MultiReader(bytes.NewReader(head[:n]), resp.Body),
		Closer: resp.Body,
	}, mediaType, nil
}

// PrepareURLMedia fetches URL media once and returns stream/bytes plus MIME type.
func PrepareURLMedia(ctx context.Context, s string) (any, string, error) {
	mediaURL, mediaType, err := StreamURLWithMime(ctx, s)
	if err != nil {
		return nil, "", err
	}

	if strings.Contains(mediaType, "image") {
		defer mediaURL.Close()

		readerToBytes, err := io.ReadAll(mediaURL)
		if err != nil {
			return nil, "", fmt.Errorf("prepareURLMedia failed to read image: %w", err)
		}

		return readerToBytes, mediaType, nil
	}

	return mediaURL, mediaType, nil
}
