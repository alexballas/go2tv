//go:build !(android || ios)

package gui

import (
	"testing"

	"go2tv.app/go2tv/v2/soapcalls"
)

func TestChromecastProgressTimeline(t *testing.T) {
	tests := []struct {
		name             string
		mediaDuration    float64
		ffmpegSeek       int
		rendererDuration float32
		rendererCurrent  float32
		wantCurrent      float64
		wantDuration     float64
	}{
		{
			name:             "direct",
			rendererDuration: 120,
			rendererCurrent:  30,
			wantCurrent:      30,
			wantDuration:     120,
		},
		{
			name:             "transcoded seek",
			mediaDuration:    6176,
			ffmpegSeek:       3673,
			rendererDuration: 12,
			rendererCurrent:  8,
			wantCurrent:      3681,
			wantDuration:     6176,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current, duration := chromecastProgressTimeline(
				tt.mediaDuration,
				tt.ffmpegSeek,
				tt.rendererDuration,
				tt.rendererCurrent,
			)
			if current != tt.wantCurrent || duration != tt.wantDuration {
				t.Fatalf("timeline = current %.0f, duration %.0f", current, duration)
			}
		})
	}
}

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

func TestDLNAProgressClampsTranscodedTimeline(t *testing.T) {
	tvdata := &soapcalls.TVPayload{
		Transcode:     true,
		MediaDuration: 120,
		FFmpegSeek:    100,
	}

	current, total, end, err := dlnaProgressTimeline(tvdata, []string{"NOT_IMPLEMENTED", "00:00:30"})
	if err != nil {
		t.Fatal(err)
	}
	if current != 120 || total != 120 || end != "00:02:00" {
		t.Fatalf("progress = current %d, total %d, end %q", current, total, end)
	}
}
