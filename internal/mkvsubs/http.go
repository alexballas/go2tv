package mkvsubs

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	assOverride = regexp.MustCompile(`\{\\[^{}]*\}`)
	formatTag   = regexp.MustCompile(`(?i)</?(?:b|i|u|s|font)(?:\s[^<>]*)?>`)
)

// overlayText strips ASS overrides and formatting tags from SRT-track text:
// the receiver overlay shows plain text, so {\an8} or <i> would appear
// literally. ASS tracks are cleaned by assText; a literal <i> there is text.
func overlayText(s string) string {
	return strings.TrimSpace(formatTag.ReplaceAllString(assOverride.ReplaceAllString(s, ""), ""))
}

// Handler supplies a bounded caption window in the receiver's timeline. Offset
// accounts for a transcoded seek; transcoded streams also rebase the media origin.
func Handler(p *Parser, offset float64, transcoded bool) http.Handler {
	p.plainText = true
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		start, err := strconv.ParseFloat(r.URL.Query().Get("time"), 64)
		if err != nil || math.IsNaN(start) || math.IsInf(start, 0) || start < 0 || start > 1e9 {
			http.Error(w, "invalid playback time", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		sourceOffset := offset
		if transcoded {
			origin, originErr := p.TimelineOrigin(ctx)
			err = originErr
			sourceOffset += origin
		}
		var cues []Cue
		if err == nil {
			cues, err = p.Window(ctx, start+sourceOffset, start+sourceOffset+30)
		}
		available := !errors.Is(err, ErrNoSubtitles)
		until := start + 30
		var incomplete *IncompleteWindow
		partial := errors.As(err, &incomplete)
		if partial {
			until = incomplete.Until - sourceOffset
		}
		if err != nil && available && !partial {
			http.Error(w, "subtitle pieces unavailable", http.StatusServiceUnavailable)
			return
		}
		if cues == nil {
			cues = []Cue{}
		}
		for i := range cues {
			cues[i].Start -= sourceOffset
			cues[i].End -= sourceOffset
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(struct {
			Available bool    `json:"available"`
			Until     float64 `json:"until"`
			Cues      []Cue   `json:"cues"`
		}{available, until, cues}); err != nil {
			return
		}
	})
}
