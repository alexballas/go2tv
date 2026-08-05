// Package logging holds the shared slog setup used by the packages that expose
// an optional debug log sink (soapcalls.TVPayload, castprotocol.CastClient and
// utils.TranscodeOptions). Keeping the handler configuration in one place means
// records exported from the GUI's debug view share a single format.
package logging

import (
	"io"
	"log/slog"
)

// Discard drops every record. It is the fallback for callers that have not
// configured a log sink, so logging call sites never need a nil check.
var Discard = NewJSON(io.Discard)

// NewJSON returns a debug-level logger that writes JSON records to w.
func NewJSON(w io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
