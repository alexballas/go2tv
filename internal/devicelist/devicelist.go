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

var scan = devices.ScanAllDevices

// WriteJSON runs scanRounds bounded discovery rounds back to back, merges them
// by URL and writes one JSON array to w. No devices is [], not an error.
func WriteJSON(ctx context.Context, w io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()

	byURL := make(map[string]Entry)
	var scanErr error
	for range scanRounds {
		found, err := scan(ctx, dlnaDelay)
		if err != nil && !errors.Is(err, devices.ErrNoDeviceAvailable) {
			scanErr = err
		}
		for _, d := range found {
			if _, ok := byURL[d.Addr]; !ok {
				byURL[d.Addr] = Entry{Name: d.Name, URL: d.Addr, Type: d.Type, AudioOnly: d.IsAudioOnly}
			}
		}
	}
	if len(byURL) == 0 && scanErr != nil {
		return fmt.Errorf("WriteJSON: %w", scanErr)
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
		return fmt.Errorf("WriteJSON: %w", err)
	}
	return nil
}

// WriteHiddenNotice tells the user, once, that the last strict scan ignored
// DLNA replies whose description address differs from the sender.
func WriteHiddenNotice(w io.Writer) {
	if n := devices.HiddenSSDPLocations(); n > 0 && !devices.RelaxedDiscovery() {
		fmt.Fprintf(w, "%d DLNA replies ignored (description address differs from sender); see -relaxed-discovery.\n", n)
	}
}
