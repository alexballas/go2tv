// Package mediasource exposes registered progressive media sources to playback
// integrations. Readers return only media bytes that have arrived and verified.
package mediasource

import (
	"net/http"

	internal "go2tv.app/go2tv/v2/internal/mediasource"
)

// Source opens independent, cancellable readers and provides a seekable input URL.
type Source = internal.Source

// Register binds a logical path to a source. Call the returned cleanup after
// cancelling active requests.
func Register(path string, source Source) func() {
	return internal.Register(path, source)
}

// Lookup returns the progressive source registered for a logical path.
func Lookup(path string) (Source, bool) {
	return internal.Lookup(path)
}

// Input returns a registered source's URL, or the original path.
func Input(path string) string {
	return internal.Input(path)
}

// Serve serves byte ranges using an independent reader for each request.
func Serve(w http.ResponseWriter, r *http.Request, name string, source Source) {
	internal.Serve(w, r, name, source)
}
