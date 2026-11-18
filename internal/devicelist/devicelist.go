// Package devicelist prints one-shot device scans for scripts (go2tv -l -json).
package devicelist

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"time"

	"go2tv.app/go2tv/v2/devices"
)

// Entry is one device in the JSON list; url is what -t accepts.
type Entry struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Type      string `json:"type"`
	AudioOnly bool   `json:"audio_only"`
}

const (
	// scanRounds resends discovery once: a single lost multicast packet
	// would otherwise drop a device from a one-shot list.
	scanRounds  = 2
	dlnaDelay   = 2
	scanTimeout = 20 * time.Second
)

var (
	scan   = devices.ScanAllDevices
	hidden = devices.HiddenSSDPLocations
)

// WriteJSON runs scanRounds bounded discovery rounds back to back, merges them
// by URL and writes one JSON array to w. It returns the union of SSDP
// description URLs strict discovery hid across the rounds. No devices after a
// clean scan is [], not an error; cancellation, a deadline, or failure of every
// round with nothing found is an error.
func WriteJSON(ctx context.Context, w io.Writer) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()

	byURL := make(map[string]Entry)
	hiddenSet := make(map[string]struct{})
	var (
		scanErr error
		clean   bool
	)
	for range scanRounds {
		if ctx.Err() != nil {
			break
		}
		found, err := scan(ctx, dlnaDelay)
		switch {
		case err == nil, errors.Is(err, devices.ErrNoDeviceAvailable):
			clean = true
		default:
			scanErr = err
		}
		for _, loc := range hidden() {
			hiddenSet[loc] = struct{}{}
		}
		for _, d := range found {
			if _, ok := byURL[d.Addr]; !ok {
				byURL[d.Addr] = Entry{Name: d.Name, URL: d.Addr, Type: d.Type, AudioOnly: d.IsAudioOnly}
			}
		}
	}
	hiddenURLs := slices.Sorted(maps.Keys(hiddenSet))
	if len(byURL) == 0 {
		switch {
		case ctx.Err() != nil:
			return hiddenURLs, fmt.Errorf("WriteJSON: %w", ctx.Err())
		case !clean:
			return hiddenURLs, fmt.Errorf("WriteJSON: %w", scanErr)
		}
	}

	entries := slices.SortedFunc(maps.Values(byURL), func(a, b Entry) int {
		return cmp.Or(cmp.Compare(a.Type, b.Type), cmp.Compare(a.Name, b.Name), cmp.Compare(a.URL, b.URL))
	})
	if entries == nil {
		entries = []Entry{}
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(entries); err != nil {
		return hiddenURLs, fmt.Errorf("WriteJSON: %w", err)
	}
	return hiddenURLs, nil
}

// WriteHiddenNotice tells the user, once, how many SSDP description URLs
// strict discovery ignored that relaxed discovery would fetch.
func WriteHiddenNotice(w io.Writer, hiddenURLs []string) {
	if n := len(hiddenURLs); n > 0 && !devices.RelaxedDiscovery() {
		fmt.Fprintf(w, "%d SSDP description URLs ignored by strict discovery; see -relaxed-discovery.\n", n)
	}
}
