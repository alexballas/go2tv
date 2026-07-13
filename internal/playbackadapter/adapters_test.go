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

func TestDLNAVolumeReadsRenderer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`<root><device><serviceList>
<service><serviceType>urn:schemas-upnp-org:service:RenderingControl:1</serviceType><serviceId>urn:upnp-org:serviceId:RenderingControl</serviceId><controlURL>/rendering</controlURL></service>
<service><serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType><serviceId>urn:upnp-org:serviceId:AVTransport</serviceId><controlURL>/transport</controlURL><eventSubURL>/events</eventSubURL></service>
</serviceList></device></root>`))
			return
		}
		if r.URL.Path != "/rendering" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if !strings.Contains(r.Header.Get("SOAPAction"), "#GetVolume") {
			t.Errorf("SOAPAction = %q", r.Header.Get("SOAPAction"))
		}
		w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
		_, _ = w.Write([]byte(`<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:GetVolumeResponse xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1"><CurrentVolume>37</CurrentVolume></u:GetVolumeResponse></s:Body></s:Envelope>`))
	}))
	defer server.Close()
	transport, err := NewDLNA(context.Background(), DLNAConfig{Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	volume, err := transport.Volume(context.Background())
	if err != nil || volume != 37 {
		t.Fatalf("volume = %d, err = %v", volume, err)
	}
}
