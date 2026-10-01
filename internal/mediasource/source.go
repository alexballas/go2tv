// Package mediasource connects progressive media to the existing file playback paths.
package mediasource

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"
)

// Source opens independent, cancellable readers and exposes a seekable URL to
// FFmpeg. A source must never return bytes that have not arrived and verified.
type Source interface {
	Open(context.Context) (io.ReadSeekCloser, error)
	URL() string
	MIME() string
	Size() int64
}

var sources sync.Map

// Register binds a logical file path for the lifetime of a playback source.
// Its cleanup must run after active requests have been cancelled.
func Register(path string, source Source) func() {
	sources.Store(path, source)
	return func() { sources.CompareAndDelete(path, source) }
}

func Lookup(path string) (Source, bool) {
	source, ok := sources.Load(path)
	if !ok {
		return nil, false
	}
	return source.(Source), true
}

// Input returns a URL for progressive sources, preserving FFmpeg input seeks.
func Input(path string) string {
	if source, ok := Lookup(path); ok {
		return source.URL()
	}
	return path
}

// Serve serves byte ranges without sniffing (which would block HEAD on a
// missing first piece). Each request owns its read offset and cancellation.
func Serve(w http.ResponseWriter, r *http.Request, name string, source Source) {
	w.Header().Set("Content-Type", source.MIME())
	reader, err := source.Open(r.Context())
	if err != nil {
		http.Error(w, "media unavailable", http.StatusServiceUnavailable)
		return
	}
	defer reader.Close()
	http.ServeContent(w, r, name, time.Time{}, reader)
}
