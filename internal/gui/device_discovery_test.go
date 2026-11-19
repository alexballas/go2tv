package gui

import (
	"net"
	"slices"
	"testing"

	"go2tv.app/go2tv/v2/devices"
)

func TestReachableRelayedDeviceDropsFromListInStrictMode(t *testing.T) {
	rendererAddress := func() string {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		t.Cleanup(func() { _ = listener.Close() })
		return "http://" + listener.Addr().String() + "/device.xml"
	}
	mode := devices.RelaxedDiscovery()
	t.Cleanup(func() { devices.SetRelaxedDiscovery(mode) })

	direct := devType{addr: rendererAddress(), deviceType: devices.DeviceTypeDLNA}
	relayed := devType{addr: rendererAddress(), deviceType: devices.DeviceTypeDLNA, requiresRelaxedDiscovery: true}

	tt := []struct {
		name    string
		relaxed bool
		want    []devType
	}{
		{name: "relaxed", relaxed: true, want: []devType{relayed, direct}},
		{name: "strict", want: []devType{direct}},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			devices.SetRelaxedDiscovery(tc.relaxed)
			if got := retainReachableDevices(nil, []devType{relayed, direct}); !slices.Equal(got, tc.want) {
				t.Fatalf("device list = %v, want %v", got, tc.want)
			}
		})
	}
}
