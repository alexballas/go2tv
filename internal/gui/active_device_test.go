//go:build !(android || ios)

package gui

import (
	"testing"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"
	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/soapcalls"
)

func TestTraversalPlaybackTargetPrefersActiveDevice(t *testing.T) {
	tests := []struct {
		name   string
		screen *FyneScreen
		want   playbackTarget
	}{
		{
			name: "playing chromecast over selected dlna",
			screen: &FyneScreen{
				State:              "Playing",
				selectedDevice:     devType{name: "Selected DLNA", addr: "http://selected-dlna", deviceType: devices.DeviceTypeDLNA},
				selectedDeviceType: devices.DeviceTypeDLNA,
				controlURL:         "http://selected-dlna/control",
				activeDevice:       devType{name: "Active Cast", addr: "http://active-cast:8009", deviceType: devices.DeviceTypeChromecast},
			},
			want: playbackTarget{
				device: devType{name: "Active Cast", addr: "http://active-cast:8009", deviceType: devices.DeviceTypeChromecast},
			},
		},
		{
			name: "paused dlna over selected chromecast",
			screen: &FyneScreen{
				State:              "Paused",
				selectedDevice:     devType{name: "Selected Cast", addr: "http://selected-cast:8009", deviceType: devices.DeviceTypeChromecast},
				selectedDeviceType: devices.DeviceTypeChromecast,
				activeDevice:       devType{name: "Active DLNA", addr: "http://active-dlna", deviceType: devices.DeviceTypeDLNA},
				tvdata: &soapcalls.TVPayload{
					ControlURL:           "http://active-dlna/control",
					EventURL:             "http://active-dlna/event",
					RenderingControlURL:  "http://active-dlna/rendering",
					ConnectionManagerURL: "http://active-dlna/connection",
				},
			},
			want: playbackTarget{
				device:               devType{name: "Active DLNA", addr: "http://active-dlna", deviceType: devices.DeviceTypeDLNA},
				controlURL:           "http://active-dlna/control",
				eventURL:             "http://active-dlna/event",
				renderingControlURL:  "http://active-dlna/rendering",
				connectionManagerURL: "http://active-dlna/connection",
			},
		},
		{
			name: "stopped uses selected device",
			screen: &FyneScreen{
				State:              "Stopped",
				selectedDevice:     devType{name: "Selected Cast", addr: "http://selected-cast:8009", deviceType: devices.DeviceTypeChromecast},
				selectedDeviceType: devices.DeviceTypeChromecast,
				activeDevice:       devType{name: "Old DLNA", addr: "http://old-dlna", deviceType: devices.DeviceTypeDLNA},
			},
			want: playbackTarget{
				device: devType{name: "Selected Cast", addr: "http://selected-cast:8009", deviceType: devices.DeviceTypeChromecast},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := traversalPlaybackTarget(tt.screen)
			if target != tt.want {
				t.Fatalf("target = %+v, want %+v", target, tt.want)
			}
		})
	}
}

func TestAutoPlayPlaybackTargetKeepsActiveDeviceAfterCompletion(t *testing.T) {
	screen := &FyneScreen{
		State:              "Stopped",
		selectedDevice:     devType{name: "Selected DLNA", addr: "http://selected-dlna", deviceType: devices.DeviceTypeDLNA},
		selectedDeviceType: devices.DeviceTypeDLNA,
		activeDevice:       devType{name: "Active Cast", addr: "http://active-cast:8009", deviceType: devices.DeviceTypeChromecast},
	}

	target := autoPlayPlaybackTarget(screen)
	if target.device != screen.activeDevice {
		t.Fatalf("device = %+v, want active %+v", target.device, screen.activeDevice)
	}
}

func TestUpdateActiveDeviceViewUsesActiveDevice(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	screen := &FyneScreen{
		State:             "Playing",
		selectedDevice:    devType{name: "Bedroom TV", addr: "http://bedroom", deviceType: "chromecast"},
		activeDevice:      devType{name: "Living Room TV", addr: "http://living-room", deviceType: "chromecast"},
		ActiveDeviceLabel: widget.NewLabel(""),
		ActiveDeviceCard:  widget.NewCard("Active Device", "", container.NewHBox(widget.NewIcon(theme.MediaPlayIcon()), widget.NewLabel(""))),
	}
	screen.ActiveDeviceCard.Hide()

	fyne.DoAndWait(func() {
		screen.updateActiveDeviceView()
	})

	if got := screen.ActiveDeviceLabel.Text; got != "Living Room TV" {
		t.Fatalf("active device label = %q, want %q", got, "Living Room TV")
	}
	if !screen.ActiveDeviceCard.Visible() {
		t.Fatal("expected active device card visible")
	}
}

func TestClearActiveDeviceHidesActiveDeviceCard(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	screen := &FyneScreen{
		State:             "Playing",
		activeDevice:      devType{name: "Living Room TV", addr: "http://living-room", deviceType: "chromecast"},
		ActiveDeviceLabel: widget.NewLabel(""),
		ActiveDeviceCard: widget.NewCard("Active Device", "",
			container.NewHBox(widget.NewIcon(theme.MediaPlayIcon()), widget.NewLabel(""))),
	}

	fyne.DoAndWait(func() {
		screen.updateActiveDeviceView()
	})

	screen.clearActiveDevice()
	fyne.DoAndWait(func() {})

	if got := screen.getActiveDevice(); got.addr != "" || got.name != "" {
		t.Fatalf("expected active device cleared, got %+v", got)
	}
	if screen.ActiveDeviceCard.Visible() {
		t.Fatal("expected active device card hidden after clear")
	}
}

func TestActiveDeviceViewExplainsRemoteSessionLock(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	label := widget.NewLabel("")
	icon := widget.NewIcon(theme.MediaPlayIcon())
	screen := &FyneScreen{
		State:             "Stopped",
		ActiveDeviceLabel: label,
		ActiveDeviceIcon:  icon,
		ActiveDeviceCard:  widget.NewCard("Active Device", "", container.NewHBox(icon, label)),
	}
	screen.ActiveDeviceCard.Hide()
	releaseLease, err := screen.renderGate.acquireRemoteLease()
	if err != nil {
		t.Fatal(err)
	}

	fyne.DoAndWait(func() {
		screen.updateActiveDeviceView()
	})

	if got := label.Text; got != "Remote Web Session active: cast controls disabled" {
		t.Fatalf("notice = %q", got)
	}
	if label.Importance != widget.WarningImportance {
		t.Fatalf("notice importance = %v, want warning", label.Importance)
	}
	if icon.Resource != theme.WarningIcon() {
		t.Fatal("expected warning icon")
	}
	if !screen.ActiveDeviceCard.Visible() {
		t.Fatal("expected remote-session notice visible")
	}

	releaseLease()
	fyne.DoAndWait(func() {
		screen.updateActiveDeviceView()
	})
	if screen.ActiveDeviceCard.Visible() {
		t.Fatal("expected notice hidden after remote session stops")
	}
}
