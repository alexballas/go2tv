//go:build !(android || ios)

package gui

import (
	"reflect"
	"testing"
	"unsafe"

	"go2tv.app/go2tv/v2/castprotocol"
	"go2tv.app/go2tv/v2/devices"
)

func newConnectedCastClientForTest(t *testing.T, deviceAddr string) *castprotocol.CastClient {
	t.Helper()

	client, err := castprotocol.NewCastClient(deviceAddr)
	if err != nil {
		t.Fatalf("NewCastClient() error = %v", err)
	}

	client.Close(false)
	clientConnectedFieldSet(t, client, true)
	return client
}

func clientConnectedFieldSet(t *testing.T, client *castprotocol.CastClient, connected bool) {
	t.Helper()

	clientValue := reflectValueElem(t, client)
	field := clientValue.FieldByName("connected")
	if !field.IsValid() {
		t.Fatal("connected field not found")
	}
	if !field.CanSet() {
		field = reflectNewAtField(field)
	}
	field.SetBool(connected)
}

func reflectValueElem(t *testing.T, value any) reflect.Value {
	t.Helper()

	v := reflect.ValueOf(value)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		t.Fatal("expected non-nil pointer")
	}
	return v.Elem()
}

func reflectNewAtField(field reflect.Value) reflect.Value {
	return reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
}

func TestChromecastSessionClientUsesActiveDevice(t *testing.T) {
	client := newConnectedCastClientForTest(t, "http://living-room:8009")

	screen := &FyneScreen{
		chromecastClient: client,
		activeDevice:     devType{name: "Living Room", addr: "http://living-room:8009", deviceType: devices.DeviceTypeChromecast},
	}

	if got := screen.chromecastSessionClient(); got != client {
		t.Fatal("expected session client for active Chromecast device")
	}

	screen.activeDevice = devType{name: "Bedroom", addr: "http://bedroom:8009", deviceType: devices.DeviceTypeChromecast}
	if got := screen.chromecastSessionClient(); got != nil {
		t.Fatal("expected nil for mismatched active Chromecast device")
	}

	screen.activeDevice = devType{name: "Bedroom DLNA", addr: "http://bedroom-dlna", deviceType: devices.DeviceTypeDLNA}
	if got := screen.chromecastSessionClient(); got != nil {
		t.Fatal("expected nil for non-Chromecast active device")
	}
}

func TestActiveChromecastPlaybackClientRequiresPlayingOrPaused(t *testing.T) {
	client := newConnectedCastClientForTest(t, "http://living-room:8009")

	screen := &FyneScreen{
		chromecastClient: client,
		activeDevice:     devType{name: "Living Room", addr: "http://living-room:8009", deviceType: devices.DeviceTypeChromecast},
		State:            "Playing",
	}

	if got := screen.activeChromecastPlaybackClient(); got != client {
		t.Fatal("expected playback client while playing")
	}

	screen.State = "Paused"
	if got := screen.activeChromecastPlaybackClient(); got != client {
		t.Fatal("expected playback client while paused")
	}

	screen.State = "Stopped"
	if got := screen.activeChromecastPlaybackClient(); got != nil {
		t.Fatal("expected no playback client while stopped")
	}

	screen.State = "Waiting"
	if got := screen.activeChromecastPlaybackClient(); got != nil {
		t.Fatal("expected no playback client while waiting")
	}
}

func TestReusableChromecastClientForSelectedDeviceRequiresSameDevice(t *testing.T) {
	client := newConnectedCastClientForTest(t, "http://living-room:8009")

	screen := &FyneScreen{
		chromecastClient: client,
		selectedDevice:   devType{name: "Living Room", addr: "http://living-room:8009", deviceType: devices.DeviceTypeChromecast},
	}

	if got := screen.reusableChromecastClientForSelectedDevice(); got != client {
		t.Fatal("expected reusable client for selected Chromecast device")
	}

	screen.selectedDevice = devType{name: "Bedroom", addr: "http://bedroom:8009", deviceType: devices.DeviceTypeChromecast}
	if got := screen.reusableChromecastClientForSelectedDevice(); got != nil {
		t.Fatal("expected no reusable client for different selected Chromecast device")
	}

	screen.selectedDevice = devType{name: "Bedroom DLNA", addr: "http://bedroom-dlna", deviceType: devices.DeviceTypeDLNA}
	if got := screen.reusableChromecastClientForSelectedDevice(); got != nil {
		t.Fatal("expected no reusable client for non-Chromecast selection")
	}
}

func TestChromecastSelectionKeepsStartupClient(t *testing.T) {
	client := newConnectedCastClientForTest(t, "http://living-room:8009")
	screen := &FyneScreen{
		chromecastClient: client,
		activeDevice:     devType{name: "Living Room", addr: "http://living-room:8009", deviceType: devices.DeviceTypeChromecast},
		State:            "Stopped",
	}

	if screen.shouldCloseChromecastClientOnSelectionChange() {
		t.Fatal("selection would close a Chromecast client while its cast is starting")
	}

	screen.clearActiveDevice()
	if !screen.shouldCloseChromecastClientOnSelectionChange() {
		t.Fatal("selection should close an idle client after playback ends")
	}
}
