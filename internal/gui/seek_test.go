//go:build !(android || ios)

package gui

import (
	"testing"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/data/binding"

	"go2tv.app/go2tv/v2/devices"
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

func TestChromecastReceiverResumePosition(t *testing.T) {
	tt := []struct {
		name       string
		transcoded bool
		position   int
		want       int
	}{
		{name: "native resume seeks receiver", position: 3202, want: 3202},
		{name: "transcoded resume already seeks source", transcoded: true, position: 3202},
		{name: "native short seek", position: 5, want: 5},
		{name: "transcoded short seek", transcoded: true, position: 5},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			if got := chromecastReceiverStartTime(tc.position, tc.transcoded); got != tc.want {
				t.Fatalf("receiver start time = %d, want %d", got, tc.want)
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
		{name: "playing without session", screen: &FyneScreen{State: "Playing"}},
		{name: "paused without session", screen: &FyneScreen{State: "Paused"}},
		{
			name: "transitioning transcode",
			screen: &FyneScreen{activeDevice: devType{addr: "renderer"}, tvdata: &soapcalls.TVPayload{
				ControlURL:    "http://renderer/control",
				Transcode:     true,
				MediaDuration: 120,
			}},
			allowed: true,
		},
		{
			name: "live transcode without source duration",
			screen: &FyneScreen{State: "Playing", tvdata: &soapcalls.TVPayload{
				ControlURL: "http://renderer/control",
				Transcode:  true,
			}},
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

func TestDLNASliderAvailabilityRecoversAcrossMediaAndDurationChanges(t *testing.T) {
	screen, _ := newMediaCardTestScreen(t)
	screen.SlideBar = newTappableSlider(screen)
	screen.CurrentPos, screen.EndPos = binding.NewString(), binding.NewString()
	local := &soapcalls.TVPayload{ControlURL: "http://renderer/control", Seekable: true}
	stream := &soapcalls.TVPayload{ControlURL: "http://renderer/control"}
	transcode := &soapcalls.TVPayload{ControlURL: "http://renderer/control", Transcode: true, MediaDuration: 120}
	tt := []struct {
		name     string
		state    string
		payload  *soapcalls.TVPayload
		sample   *soapcalls.TVPayload
		duration float64
		enabled  bool
	}{
		{name: "idle"},
		{name: "playing before duration", state: "Playing", payload: local},
		{name: "duration arrives", state: "Playing", payload: local, sample: local, duration: 120, enabled: true},
		{name: "paused remains seekable", state: "Paused", payload: local, enabled: true},
		{name: "duration unavailable", state: "Playing", payload: local, sample: local},
		{name: "duration recovers", state: "Playing", payload: local, sample: local, duration: 120, enabled: true},
		{name: "switch to radio", state: "Playing", payload: stream},
		{name: "ignore previous media sample", state: "Playing", payload: stream, sample: local, duration: 120},
		{name: "nonseekable URL with duration", state: "Playing", payload: stream, sample: stream, duration: 120},
		{name: "seekable media resumes", state: "Playing", payload: local, sample: local, duration: 120, enabled: true},
		{name: "stop", state: "Stopped", payload: local},
		{name: "transcode source duration available", state: "Playing", payload: transcode, enabled: true},
		{name: "transcode stopped", state: "Stopped", payload: transcode},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			screen.State, screen.tvdata = tc.state, tc.payload
			if tc.sample != nil {
				screen.SlideBar.setDLNADuration(tc.sample, tc.duration)
			}
			screen.refreshPlaybackReadiness()
			// Settings/tab refreshes must not enable an unavailable timeline.
			screen.SlideBar.Enable()
			if screen.SlideBar.Disabled() == tc.enabled {
				t.Fatalf("slider disabled = %v, want %v", screen.SlideBar.Disabled(), !tc.enabled)
			}
			if !tc.enabled {
				screen.SlideBar.SetValue(25)
				screen.SlideBar.Resize(fyne.NewSize(200, 20))
				screen.SlideBar.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(150, 10)}})
				screen.SlideBar.Tapped(&fyne.PointEvent{Position: fyne.NewPos(150, 10)})
				screen.SlideBar.DragEnd()
				if screen.SlideBar.Value != 25 || screen.sliderActive {
					t.Fatal("unavailable slider accepted input")
				}
			}
		})
	}
}

func TestChromecastSliderAvailabilityUsesCurrentSession(t *testing.T) {
	screen, _ := newMediaCardTestScreen(t)
	screen.SlideBar = newTappableSlider(screen)
	client := newConnectedCastClientForTest(t, "http://living-room:8009")
	screen.chromecastClient = client
	screen.activeDevice = devType{addr: "http://living-room:8009", deviceType: devices.DeviceTypeChromecast}
	screen.State = "Playing"
	actionID := screen.nextChromecastActionID()
	tt := []struct {
		name      string
		duration  float64
		seekable  bool
		newAction bool
		stale     bool
		enabled   bool
	}{
		{name: "live radio"},
		{name: "live stream reporting duration", duration: 120},
		{name: "buffered media", duration: 120, seekable: true, enabled: true},
		{name: "duration lost", seekable: true},
		{name: "duration recovers", duration: 120, seekable: true, enabled: true},
		{name: "new media cannot reuse old duration", duration: 120, seekable: true, newAction: true, stale: true},
		{name: "new media duration arrives", duration: 240, seekable: true, enabled: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			sampleAction := actionID
			if tc.newAction {
				actionID = screen.nextChromecastActionID()
			}
			if !tc.stale {
				sampleAction = actionID
			}
			screen.SlideBar.setChromecastDuration(client, sampleAction, tc.duration, tc.seekable)
			screen.SlideBar.Enable()
			if screen.SlideBar.Disabled() == tc.enabled {
				t.Fatalf("slider disabled = %v, want %v", screen.SlideBar.Disabled(), !tc.enabled)
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
