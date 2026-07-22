//go:build !(android || ios)

package gui

import (
	"testing"

	"go2tv.app/go2tv/v2/soapcalls"
)

func TestSliderAllowsActiveTranscodeWhileRendererTransitions(t *testing.T) {
	tests := []struct {
		name    string
		screen  *FyneScreen
		allowed bool
	}{
		{name: "playing direct", screen: &FyneScreen{State: "Playing"}, allowed: true},
		{name: "paused direct", screen: &FyneScreen{State: "Paused"}, allowed: true},
		{
			name: "transitioning transcode",
			screen: &FyneScreen{tvdata: &soapcalls.TVPayload{
				ControlURL: "http://renderer/control",
				Transcode:  true,
			}},
			allowed: true,
		},
		{
			name: "stopped direct",
			screen: &FyneScreen{tvdata: &soapcalls.TVPayload{
				ControlURL: "http://renderer/control",
			}},
		},
		{
			name:   "stopped transcode",
			screen: &FyneScreen{tvdata: &soapcalls.TVPayload{Transcode: true}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slider := &tappedSlider{screen: tt.screen}
			if got := slider.canSeek(); got != tt.allowed {
				t.Fatalf("canSeek() = %v, want %v", got, tt.allowed)
			}
		})
	}
}

func TestDLNASeekTimelineUsesTranscodeSourceDuration(t *testing.T) {
	total, end, err := dlnaSeekTimeline(&soapcalls.TVPayload{
		Transcode:     true,
		MediaDuration: 6176.17,
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 6176 || end != "01:42:56" {
		t.Fatalf("timeline = %d %q", total, end)
	}
}

func TestDLNAProgressUsesFullSourceTimelineAfterRestart(t *testing.T) {
	tvdata := &soapcalls.TVPayload{
		Transcode:     true,
		MediaDuration: 6176.17,
		FFmpegSeek:    3673,
	}

	current, total, end, err := dlnaProgressTimeline(tvdata, []string{"NOT_IMPLEMENTED", "00:00:07.528"})
	if err != nil {
		t.Fatal(err)
	}
	if current != 3681 || total != 6176 || end != "01:42:56" {
		t.Fatalf("progress = current %d, total %d, end %q", current, total, end)
	}
}

func TestDLNAProgressUsesSeekOffsetWithRendererDuration(t *testing.T) {
	tvdata := &soapcalls.TVPayload{
		Transcode:  true,
		FFmpegSeek: 3673,
	}

	current, total, end, err := dlnaProgressTimeline(tvdata, []string{"01:42:56", "00:00:07.528"})
	if err != nil {
		t.Fatal(err)
	}
	if current != 3681 || total != 6176 || end != "01:42:56" {
		t.Fatalf("progress = current %d, total %d, end %q", current, total, end)
	}
}
