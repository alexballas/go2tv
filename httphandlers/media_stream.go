package httphandlers

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"go2tv.app/go2tv/v2/soapcalls"
)

const mediaStreamLogInterval = 30 * time.Second

func serveMediaStream(w http.ResponseWriter, r *http.Request, tv *soapcalls.TVPayload, mediaType string, reader io.Reader) {
	stream := newMediaStreamWriter(w, r, mediaType)
	if tv != nil && (tv.LogOutput != nil || tv.Logger != nil) {
		stream.log = tv.Log()
	}
	if stream.controller == nil && stream.log == nil {
		_, _ = io.Copy(w, reader)
		return
	}
	_, err := io.Copy(stream, reader)
	stream.logEnd(r, err)
}

type mediaStreamWriter struct {
	writer     io.Writer
	controller *http.ResponseController
	log        *slog.Logger
	path       string
	started    time.Time
	lastLog    time.Time
	bytes      int64
	failure    string
	transcoded bool
}

func newMediaStreamWriter(w http.ResponseWriter, r *http.Request, mediaType string) *mediaStreamWriter {
	now := time.Now()
	stream := &mediaStreamWriter{writer: w, path: r.URL.Path, started: now, lastLog: now}
	if strings.HasPrefix(mediaType, "audio/") {
		stream.controller = http.NewResponseController(w)
	}
	return stream
}

func (w *mediaStreamWriter) logEnd(r *http.Request, err error) {
	if w.log == nil {
		return
	}
	reason := "upstream_eof"
	if w.transcoded {
		reason = "encoder_eof"
	}
	switch {
	case err != nil && r.Context().Err() != nil:
		reason = "receiver_canceled"
	case w.failure != "":
		reason = w.failure
	case err != nil:
		reason = "upstream_read_error"
		if w.transcoded {
			reason = "transcode_error"
		}
	}
	w.log.Debug("media stream ended", "Method", "DLNAMediaHTTP", "Action", "StreamEnd", "Path", w.path,
		"Bytes", w.bytes, "Elapsed", time.Since(w.started).String(), "Transcode", w.transcoded, "Reason", reason, "Error", err)
}

func (w *mediaStreamWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.bytes += int64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.failure = "receiver_write_error"
		return n, err
	}
	// Non-seekable audio must leave the HTTP buffer promptly. Bursts of
	// buffered AAC can also expose receiver parser bugs on partial frames.
	if n > 0 && w.controller != nil {
		if err := w.controller.Flush(); err != nil {
			if !errors.Is(err, http.ErrNotSupported) {
				w.failure = "receiver_flush_error"
				return n, err
			}
			w.controller = nil
		}
	}
	if w.log != nil && time.Since(w.lastLog) >= mediaStreamLogInterval {
		w.log.Debug("media stream progress", "Method", "DLNAMediaHTTP", "Action", "StreamProgress", "Path", w.path,
			"Bytes", w.bytes, "Elapsed", time.Since(w.started).String(), "Transcode", w.transcoded)
		w.lastLog = time.Now()
	}
	return n, nil
}
