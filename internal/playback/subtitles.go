package playback

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go2tv.app/go2tv/v2/utils"
)

type SubtitleRoute struct {
	URL      string
	BurnPath string
	cleanup  func(context.Context) error
}

func (r SubtitleRoute) Cleanup(ctx context.Context) error {
	if r.cleanup == nil {
		return nil
	}
	return r.cleanup(ctx)
}

// PrepareChromecastSubtitle converts SRT, directly hosts VTT, or selects burn-in.
func PrepareChromecastSubtitle(ctx context.Context, server MediaServer, random Random, path string, transcoded bool) (SubtitleRoute, error) {
	if path == "" {
		return SubtitleRoute{}, nil
	}
	if transcoded {
		return SubtitleRoute{BurnPath: path}, nil
	}
	if server == nil {
		return SubtitleRoute{}, errors.New("subtitle media server unavailable")
	}
	if random == nil {
		random = rand.Reader
	}
	ext := strings.ToLower(filepath.Ext(path))
	var data []byte
	var err error
	switch ext {
	case ".srt":
		data, err = utils.ConvertSRTtoWebVTT(path)
	case ".vtt":
		data, err = os.ReadFile(path)
	default:
		return SubtitleRoute{}, fmt.Errorf("unsupported subtitle type %q", ext)
	}
	if err != nil {
		return SubtitleRoute{}, fmt.Errorf("read subtitles: %w", err)
	}
	id, err := opaqueID(random)
	if err != nil {
		return SubtitleRoute{}, fmt.Errorf("subtitle route: %w", err)
	}
	id += ".vtt"
	route, err := server.Add(ctx, RouteRequest{ID: id, MediaType: "text/vtt; charset=utf-8", Contents: data})
	if err != nil {
		return SubtitleRoute{}, fmt.Errorf("host subtitles: %w", err)
	}
	return SubtitleRoute{URL: route.URL, cleanup: func(cleanupCtx context.Context) error {
		return server.Remove(cleanupCtx, route.ID)
	}}, nil
}
