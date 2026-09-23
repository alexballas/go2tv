//go:build linux && !android

package mpris

import (
	"fmt"
	"math"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestPlayerDBusContract(t *testing.T) {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		t.Skip("session bus unavailable")
	}
	var plays, pauses, nexts atomic.Int32
	var seek atomic.Int64
	var volume atomic.Uint64
	service, err := Start(Controls{
		Play: func() { plays.Add(1) }, Pause: func() { pauses.Add(1) },
		Next: func() { nexts.Add(1) }, Seek: func(position int64) { seek.Store(position) },
		SetVolume: func(value float64) { volume.Store(math.Float64bits(value)) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	client, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	obj := client.Object(service.Name(), Path)
	if v, err := obj.GetProperty(RootIface + ".DesktopEntry"); err != nil || v.Value() != "app.go2tv.go2tv" {
		t.Fatalf("desktop entry = %v, %v", v.Value(), err)
	}
	var xml string
	if err := obj.Call("org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&xml); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{RootIface, PlayerIface, "Seeked", "SetPosition"} {
		if !strings.Contains(xml, name) {
			t.Errorf("introspection lacks %s", name)
		}
	}
	service.Update(Snapshot{
		Status: "Playing", Path: "/media/one.mp3", Name: "One", Artist: "Singer", Album: "Record",
		ArtURL: "file:///tmp/cover.jpg", URL: "file:///media/one.mp3", LengthUS: 120_000_000,
		PositionUS: 10_000_000, CanPlay: true, CanPause: true, CanSeek: true, CanNext: true,
		LoopStatus: "None", Volume: 1,
	})
	property, err := obj.GetProperty(PlayerIface + ".Metadata")
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := property.Value().(map[string]dbus.Variant)
	if !ok {
		t.Fatalf("metadata type = %T", property.Value())
	}
	if meta["xesam:title"].Value() != "One" || meta["mpris:artUrl"].Value() != "file:///tmp/cover.jpg" {
		t.Fatalf("metadata = %v", meta)
	}
	if got := meta["xesam:artist"].Value().([]string); len(got) != 1 || got[0] != "Singer" {
		t.Fatalf("artist = %v", got)
	}
	if got := meta["mpris:length"].Value(); got != int64(120_000_000) {
		t.Fatalf("length = %v", got)
	}
	id := meta["mpris:trackid"].Value().(dbus.ObjectPath)
	for _, method := range []string{"Pause", "Next", "Seek"} {
		args := []any{}
		if method == "Seek" {
			args = append(args, int64(5_000_000))
		}
		if err := obj.Call(PlayerIface+"."+method, 0, args...).Err; err != nil {
			t.Fatal(err)
		}
	}
	if pauses.Load() != 1 || nexts.Load() != 1 || seek.Load() != 15_000_000 {
		t.Fatalf("controls: pause=%d next=%d seek=%d", pauses.Load(), nexts.Load(), seek.Load())
	}
	if err := obj.Call(PlayerIface+".SetPosition", 0, dbus.ObjectPath("/go2tv/track/stale"), int64(30_000_000)).Err; err != nil {
		t.Fatal(err)
	}
	if seek.Load() != 15_000_000 {
		t.Fatal("stale track changed position")
	}
	if err := obj.Call(PlayerIface+".SetPosition", 0, id, int64(30_000_000)).Err; err != nil {
		t.Fatal(err)
	}
	if seek.Load() != 30_000_000 {
		t.Fatal("current track did not seek")
	}
	if err := obj.SetProperty(PlayerIface+".Volume", dbus.MakeVariant(-0.5)); err != nil {
		t.Fatal(err)
	}
	property, err = obj.GetProperty(PlayerIface + ".Volume")
	if err != nil || property.Value() != float64(0) || math.Float64frombits(volume.Load()) != 0 {
		t.Fatalf("clamped volume = %v, callback %v, err %v", property.Value(), math.Float64frombits(volume.Load()), err)
	}
	service.Update(Snapshot{Status: "Stopped", LoopStatus: "None", Volume: 0})
	property, err = obj.GetProperty(PlayerIface + ".Metadata")
	if err != nil {
		t.Fatal(err)
	}
	if got := property.Value().(map[string]dbus.Variant); len(got) != 0 {
		t.Fatalf("stopped metadata = %v", got)
	}
	if err := obj.Call(PlayerIface+".Play", 0).Err; err != nil {
		t.Fatal(err)
	}
	if plays.Load() != 0 {
		t.Fatal("played without selected media")
	}
}

func TestFlatpakUsesAllowedMPRISName(t *testing.T) {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		t.Skip("session bus unavailable")
	}
	t.Setenv("FLATPAK_ID", AppID)
	service, err := Start(Controls{})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	want := "org.mpris.MediaPlayer2." + AppID
	if service.Name() != want {
		t.Fatalf("MPRIS name = %q, want %q", service.Name(), want)
	}
	second, err := Start(Controls{})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	want = want + ".instance" + fmt.Sprint(os.Getpid())
	if second.Name() != want {
		t.Fatalf("second MPRIS name = %q, want %q", second.Name(), want)
	}
}
