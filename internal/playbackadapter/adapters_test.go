package playbackadapter

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/devices"
)

func TestScannerMapsPrivateEndpoints(t *testing.T) {
	scanner := Scanner{ScanFunc: func(context.Context, int) ([]devices.Device, error) {
		return []devices.Device{{Name: "TV", Addr: "http://192.0.2.2/device", Type: devices.DeviceTypeDLNA, IsAudioOnly: true}}, nil
	}}
	found, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(found) != 1 || found[0].Name != "TV" || found[0].Endpoint != "http://192.0.2.2/device" || !found[0].AudioOnly {
		t.Fatalf("devices = %#v", found)
	}
	if found[0].ID != "" {
		t.Fatalf("scanner assigned stable ID: %q", found[0].ID)
	}
}

func TestCallbackBridgeValidatedDelivery(t *testing.T) {
	bridge := NewCallbackBridge()
	defer bridge.Close()
	if err := bridge.Configure(9, "session", net.ParseIP("192.0.2.10"), "video/mp4", nil); err != nil {
		t.Fatalf("configure: %v", err)
	}
	body := `<propertyset><property><LastChange><Event><InstanceID val="0"><TransportState val="PLAYING"/></InstanceID></Event></LastChange></property></propertyset>`
	req := httptest.NewRequest("NOTIFY", "/callback", strings.NewReader(body))
	req.RemoteAddr = "192.0.2.10:1234"
	req.Header.Set("SID", "uuid:session")
	req.Header.Set("NT", "upnp:event")
	req.Header.Set("NTS", "upnp:propchange")
	req.Header.Set("SEQ", "1")
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	rec := httptest.NewRecorder()
	bridge.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	select {
	case event := <-bridge.Events():
		if event.Generation != 9 || event.TransportState != "PLAYING" {
			t.Fatalf("event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("event timeout")
	}
}

func TestParseClock(t *testing.T) {
	got, err := parseClock("01:02:03.75")
	if err != nil || got != 3723 {
		t.Fatalf("parse = %d, %v", got, err)
	}
	if _, err := parseClock("bad"); err == nil {
		t.Fatal("invalid time accepted")
	}
}
