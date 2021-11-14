package urlstreamer

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// StreamURL - Start the URL media streaming
func StreamURL(ctx context.Context, s string) (*io.PipeReader, error) {
	_, err := url.ParseRequestURI(s)
	if err != nil {
		return nil, fmt.Errorf("failed to parse url: %w", err)
	}

	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()

		client := &http.Client{}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s, nil)
		if err != nil {
			return
		}

		resp, err := client.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()

		io.Copy(pw, resp.Body)
	}()

	return pr, nil
}
