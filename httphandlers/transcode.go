package httphandlers

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"os/exec"

	"go2tv.app/go2tv/v2/utils"
)

// TranscodeStatus distinguishes slow preparation from an encoder failure.
type TranscodeStatus struct {
	Active bool
	Err    error
}

type transcodeState struct {
	requests   int
	generation uint64
	err        error
}

type transcodeServerKey struct{}
type transcodeStateKey struct{}

// TranscodeStatus returns the state of a media URL, or all routes when empty.
// Receiver disconnects and superseded requests are not encoder failures.
func (s *HTTPserver) TranscodeStatus(mediaURL string) TranscodeStatus {
	if s == nil {
		return TranscodeStatus{}
	}
	path := mediaURL
	if parsed, err := url.Parse(mediaURL); err == nil {
		path = parsed.Path
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var status TranscodeStatus
	for route, state := range s.transcodes {
		if path != "" && route != path {
			continue
		}
		status.Active = status.Active || state.requests > 0
		if state.err != nil {
			status.Err = state.err
		}
	}
	return status
}

func (s *HTTPserver) beginTranscode(r *http.Request, supersedes bool) func(error) {
	if s == nil {
		return func(error) {}
	}
	s.mu.Lock()
	state, _ := r.Context().Value(transcodeStateKey{}).(*transcodeState)
	if state == nil || s.transcodes[r.URL.Path] != state {
		s.mu.Unlock()
		return func(error) {}
	}
	// DLNA GETs own independent encoders. A concurrent probe cannot
	// supersede playback or erase its failure. A fresh retry after all
	// requests finish, or a replacing Chromecast LOAD, starts a new batch.
	if state.requests == 0 || supersedes {
		state.generation++
		state.err = nil
	}
	state.requests++
	generation := state.generation
	s.mu.Unlock()
	return func(err error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		state.requests--
		if s.transcodes[r.URL.Path] == state && state.generation == generation && r.Context().Err() == nil && err != nil && state.err == nil {
			state.err = err
		}
	}
}

type videoHTTPWriter struct {
	http.ResponseWriter
	written int64
	err     error
}

func (w *videoHTTPWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	w.written += int64(n)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.err = err
	}
	return n, err
}

func serveVideoTranscode(w http.ResponseWriter, r *http.Request, input any, command *exec.Cmd, opts *utils.TranscodeOptions, chromecast bool) {
	server, _ := r.Context().Value(transcodeServerKey{}).(*HTTPserver)
	// Raw input rejects concurrent requests as busy rather than replacing
	// its encoder, so those probes belong to the existing request batch.
	supersedes := chromecast && (opts == nil || opts.RawInput == nil)
	finish := server.beginTranscode(r, supersedes)
	writer := &videoHTTPWriter{ResponseWriter: w}
	serve := utils.ServeDLNATranscodedStream
	if chromecast {
		serve = utils.ServeChromecastTranscodedStream
	}
	err := serve(r.Context(), writer, input, command, opts)
	if errors.Is(err, utils.ErrTranscodeBusy) {
		finish(nil)
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	if r.Context().Err() != nil || writer.err != nil {
		// A renderer can close a probe or reconnect while FFmpeg is running.
		finish(nil)
		return
	}
	finish(err)
	if err != nil {
		if opts != nil {
			opts.LogError("serveVideoTranscode", "Transcode", err)
		}
		if writer.written == 0 {
			http.Error(w, "video transcode unavailable", http.StatusBadGateway)
		}
	}
}
