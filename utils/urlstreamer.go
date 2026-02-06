package utils

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

var (
	ErrBadStatus = errors.New("streamURL bad status code")
)

func streamURLResponse(ctx context.Context, s string) (*http.Response, error) {
	_, err := url.ParseRequestURI(s)
	if err != nil {
		return nil, fmt.Errorf("streamURL failed to parse url: %w", err)
	}

	client := &http.Client{}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s, nil)
	if err != nil {
		return nil, fmt.Errorf("streamURL failed to call NewRequest: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("streamURL failed to client.Do: %w", err)
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
	if !shouldSniffContentType(mediaType) {
		return resp.Body, mediaType, nil
	}

	head := make([]byte, 261)
	n, err := io.ReadFull(resp.Body, head)
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

	return struct {
		io.Reader
		io.Closer
	}{
		Reader: io.MultiReader(bytes.NewReader(head[:n]), resp.Body),
		Closer: resp.Body,
	}, mediaType, nil
}
