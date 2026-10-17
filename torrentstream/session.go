// Package torrentstream streams verified torrent media pieces on demand.
package torrentstream

import (
	"context"
	"io"

	internal "go2tv.app/go2tv/v2/internal/torrentstream"
)

// File describes selectable media, with its original torrent index.
type File = internal.File

// Options configures the session's private media cache.
type Options = internal.Options

// Session owns torrent downloading, readers, and its private cache. Select
// returns a logical path registered with mediasource, rather than a local file.
// Close cancels active readers, unregisters paths, and removes the cache.
type Session = internal.Session

// Open accepts a magnet URI or a local .torrent path.
func Open(ctx context.Context, input string) (*Session, error) {
	return internal.Open(ctx, input)
}

// OpenWithOptions accepts a magnet URI or .torrent path with cache options.
func OpenWithOptions(ctx context.Context, input string, options Options) (*Session, error) {
	return internal.OpenWithOptions(ctx, input, options)
}

// OpenReader opens torrent metadata from a reader.
func OpenReader(ctx context.Context, reader io.Reader) (*Session, error) {
	return internal.OpenReader(ctx, reader)
}

// OpenReaderWithOptions opens torrent metadata with cache options.
func OpenReaderWithOptions(ctx context.Context, reader io.Reader, options Options) (*Session, error) {
	return internal.OpenReaderWithOptions(ctx, reader, options)
}

// MediaMIME returns a supported media MIME type, or an empty string.
func MediaMIME(name string) string {
	return internal.MediaMIME(name)
}
