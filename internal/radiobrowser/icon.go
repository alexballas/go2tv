package radiobrowser

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	_ "golang.org/x/image/webp"

	"go2tv.app/go2tv/v2/metadata"
)

const (
	MaxIconBytes  = 2 << 20
	maxIconPixels = 4_000_000
	iconTimeout   = 5 * time.Second
)

var iconHTTPClient = &http.Client{
	Timeout: iconTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("radio icon: too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("radio icon: invalid redirect")
		}
		return nil
	},
}

// FetchIcon downloads a station favicon and normalizes it to renderer artwork.
// GIF and WebP logos are converted, since artwork normalization accepts only
// JPEG and PNG input.
func FetchIcon(ctx context.Context, favicon string) (*metadata.ArtworkAsset, error) {
	parsed, err := url.Parse(strings.TrimSpace(favicon))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("radio icon: invalid URL")
	}
	ctx, cancel := context.WithTimeout(ctx, iconTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("radio icon request: %w", err)
	}
	request.Header = http.Header{"User-Agent": {"Go2TV"}}
	response, err := iconHTTPClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("radio icon fetch: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("radio icon status: %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxIconBytes+1))
	if err != nil {
		return nil, fmt.Errorf("radio icon body: %w", err)
	}
	if len(data) > MaxIconBytes {
		return nil, fmt.Errorf("radio icon too large")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("radio icon decode: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || uint64(config.Width)*uint64(config.Height) > maxIconPixels {
		return nil, fmt.Errorf("radio icon dimensions too large")
	}
	switch format {
	case "jpeg", "png":
	case "gif", "webp":
		decoded, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("radio icon decode: %w", err)
		}
		var converted bytes.Buffer
		if err := png.Encode(&converted, decoded); err != nil {
			return nil, fmt.Errorf("radio icon convert: %w", err)
		}
		data = converted.Bytes()
	default:
		return nil, fmt.Errorf("radio icon: unsupported image")
	}
	asset, err := metadata.LoadArtwork(data, parsed.String())
	if err != nil {
		return nil, fmt.Errorf("radio icon normalize: %w", err)
	}
	return asset, nil
}
