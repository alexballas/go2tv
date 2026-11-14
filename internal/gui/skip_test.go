//go:build !(android || ios)

package gui

import (
	"testing"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/data/binding"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/soapcalls"
)

func TestSkipButtonsFollowSeekAndPreviewTarget(t *testing.T) {
	screen, _ := newMediaCardTestScreen(t)
	screen.SlideBar = newTappableSlider(screen)
	screen.CurrentPos, screen.EndPos = binding.NewString(), binding.NewString()
	back, forward := widget.NewButton("", nil), widget.NewButton("", nil)
	screen.skips.buttons = []fyne.Disableable{back, forward}
	t.Cleanup(func() {
		if screen.skips.timer != nil {
			screen.skips.timer.Stop()
		}
	})

	screen.SlideBar.Enable()
	if !back.Disabled() || !forward.Disabled() {
		t.Fatal("skip enabled without a seekable session")
	}
	skipAction(screen, 30)
	if screen.skips.pending {
		t.Fatal("skip accepted without a seekable session")
	}

	screen.State, screen.activeDevice = "Playing", devType{addr: "renderer"}
	screen.tvdata = &soapcalls.TVPayload{ControlURL: "http://renderer/control", Transcode: true, MediaDuration: 1000}
	screen.SlideBar.Enable()
	if back.Disabled() || forward.Disabled() {
		t.Fatal("skip disabled for a seekable session")
	}

	tt := []struct {
		name   string
		start  float64 // slider percent
		deltas []int
		want   int
		label  string
	}{
		{"presses add up", 10, []int{30, 30, -10}, 150, "00:02:30"},
		{"back stops at start", 0.5, []int{-10}, 0, "00:00:00"},
		{"forward stops before end", 99.5, []int{30}, 999, "00:16:39"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			screen.skips.pending = false
			screen.SlideBar.SetValue(tc.start)
			for _, delta := range tc.deltas {
				skipAction(screen, delta)
			}
			label, _ := screen.CurrentPos.Get()
			if screen.skips.target != tc.want || label != tc.label {
				t.Fatalf("target = %d (%s), want %d (%s)", screen.skips.target, label, tc.want, tc.label)
			}
		})
	}
}
