package utils

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

var (
	ErrBadStatus = errors.New("streamURL bad status code")
)

// StreamURL returns the response body for the input media URL.
func StreamURL(ctx context.Context, s string) (io.ReadCloser, error) {
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
		return nil, ErrBadStatus
	}

	body := resp.Body

	return body, nil
}
