// Package castsubtitles prepares external and progressive embedded captions for
// Chromecast playback using Go2TV's existing subtitle conversion and parser.
package castsubtitles

import (
	"context"
	"net/http"

	"go2tv.app/go2tv/v2/httphandlers"
	internal "go2tv.app/go2tv/v2/internal/castsubtitles"
	"go2tv.app/go2tv/v2/internal/mkvsubs"
	"go2tv.app/go2tv/v2/mediasource"
)

// Options configures subtitle selection, conversion, and playback timing.
type Options = internal.Options

// Captions contains receiver caption URLs and optional subtitle burn inputs.
type Captions = internal.Captions

// Register prepares captions and adds their handlers to a Go2TV HTTP server.
func Register(server *httphandlers.HTTPserver, host, mediaPath, subtitlePath string, opts Options) (Captions, error) {
	return internal.Register(server, host, mediaPath, subtitlePath, opts)
}

// RegisterContext prepares captions with cancellable external conversion.
func RegisterContext(ctx context.Context, server *httphandlers.HTTPserver, host, mediaPath, subtitlePath string, opts Options) (Captions, error) {
	return internal.RegisterContext(ctx, server, host, mediaPath, subtitlePath, opts)
}

// RegisterTorrent registers progressive embedded text captions, or removes stale
// captions when automatic selection is disabled.
func RegisterTorrent(server *httphandlers.HTTPserver, host, path string, automatic bool, offset int, transcoded bool) string {
	return internal.RegisterTorrent(server, host, path, automatic, offset, transcoded)
}

// TorrentSource returns a registered Matroska source when automatic captions
// are enabled. The caller can use it for progressive captions or subtitle burn.
func TorrentSource(path string, automatic bool) mediasource.Source {
	return internal.TorrentSource(path, automatic)
}

// TorrentHandler returns a progressive caption handler for a media source.
// Offset rebases transcoded seeks; transcoded also accounts for media origin.
// A nil source returns a nil handler.
func TorrentHandler(source mediasource.Source, offset int, transcoded bool) http.Handler {
	if source == nil {
		return nil
	}
	return mkvsubs.Handler(mkvsubs.New(source), float64(offset), transcoded)
}
