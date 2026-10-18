//go:build android || ios

package gui

import (
	"testing"

	"github.com/alexballas/refyne/v2/widget"
)

// Mobile hides the status row while playback is active; startup must still
// tell the user something is happening instead of showing an empty bar.
func TestMobilePlaybackStatusShowsLoadingDuringStartup(t *testing.T) {
	tt := []struct {
		name        string
		state       string
		wantStatus  string
		wantVisible bool
	}{
		{name: "starting", state: "Stopped", wantStatus: "Loading media… · Living Room", wantVisible: true},
		{name: "switching while playing", state: "Playing", wantVisible: false},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			s := newMobileTorrentTestScreen(t)
			s.playbackStatus = widget.NewLabel("")
			s.timeline = widget.NewSlider(0, 100)
			s.radioSong.label = widget.NewLabel("")
			s.radioDetail = widget.NewLabel("")
			s.radioRow = widget.NewLabel("")
			s.selectedDevice = devType{name: "Living Room", addr: "http://tv"}
			s.State = tc.state
			s.playbackStarting = true

			s.refreshPlaybackStatus()

			if s.playbackStatus.Visible() != tc.wantVisible {
				t.Fatalf("status visible=%v, want %v", s.playbackStatus.Visible(), tc.wantVisible)
			}
			if tc.wantVisible && s.playbackStatus.Text != tc.wantStatus {
				t.Fatalf("status=%q, want %q", s.playbackStatus.Text, tc.wantStatus)
			}
		})
	}
}
