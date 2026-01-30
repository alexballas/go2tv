package soapcalls

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestDMRextractor(t *testing.T) {
	raw := `<?xml version="1.0"?>
	<root>
	<device>
	<serviceList>
	<service>
	<serviceType>urn:schemas-upnp-org:service:RenderingControl:1</serviceType>
	<serviceId>urn:upnp-org:serviceId:RenderingControl</serviceId>
	<controlURL>/upnp/control/RenderingControl1</controlURL>
	<eventSubURL>/upnp/event/RenderingControl1</eventSubURL>
	<SCPDURL>/RenderingControl_1.xml</SCPDURL>
	</service>
	<service>
	<serviceType>urn:schemas-upnp-org:service:ConnectionManager:1</serviceType>
	<serviceId>urn:upnp-org:serviceId:ConnectionManager</serviceId>
	<controlURL>/upnp/control/ConnectionManager1</controlURL>
	<eventSubURL>/upnp/event/ConnectionManager1</eventSubURL>
	<SCPDURL>/ConnectionManager_1.xml</SCPDURL>
	</service>
	<service>
	<serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType>
	<serviceId>urn:upnp-org:serviceId:AVTransport</serviceId>
	<controlURL>/upnp/control/AVTransport1</controlURL>
	<eventSubURL>/upnp/event/AVTransport1</eventSubURL>
	<SCPDURL>/AVTransport_1.xml</SCPDURL>
	</service>
	</serviceList>
	</device>
	</root>`

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(raw))
	}))

	defer testServer.Close()

	_, err := DMRextractor(context.Background(), testServer.URL)
	if err != nil {
		t.Fatalf("Failed to call DMRextractor due to %s", err.Error())
	}
}

func TestParseDMRFromXML(t *testing.T) {
	tests := []struct {
		name                 string
		raw                  string
		wantErr              bool
		wantAVTransportURL   string
		wantRenderingURL     string
		wantConnectionMgrURL string
	}{
		{
			name: "Single Device with AVTransport",
			raw: `<?xml version="1.0"?>
<root>
	<device>
		<serviceList>
			<service>
				<serviceType>urn:schemas-upnp-org:service:RenderingControl:1</serviceType>
				<serviceId>urn:upnp-org:serviceId:RenderingControl</serviceId>
				<controlURL>/upnp/control/RenderingControl1</controlURL>
				<eventSubURL>/upnp/event/RenderingControl1</eventSubURL>
				<SCPDURL>/RenderingControl_1.xml</SCPDURL>
			</service>
			<service>
				<serviceType>urn:schemas-upnp-org:service:ConnectionManager:1</serviceType>
				<serviceId>urn:upnp-org:serviceId:ConnectionManager</serviceId>
				<controlURL>/upnp/control/ConnectionManager1</controlURL>
				<eventSubURL>/upnp/event/ConnectionManager1</eventSubURL>
				<SCPDURL>/ConnectionManager_1.xml</SCPDURL>
			</service>
			<service>
				<serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType>
				<serviceId>urn:upnp-org:serviceId:AVTransport</serviceId>
				<controlURL>/upnp/control/AVTransport1</controlURL>
				<eventSubURL>/upnp/event/AVTransport1</eventSubURL>
				<SCPDURL>/AVTransport_1.xml</SCPDURL>
			</service>
		</serviceList>
	</device>
</root>`,
			wantErr:              false,
			wantAVTransportURL:   "http://example.com:8080/upnp/control/AVTransport1",
			wantRenderingURL:     "http://example.com:8080/upnp/control/RenderingControl1",
			wantConnectionMgrURL: "http://example.com:8080/upnp/control/ConnectionManager1",
		},
		{
			name: "Embedded MediaRenderer (Denon AVR style)",
			raw: `<?xml version="1.0" encoding="utf-8"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
<specVersion>
	<major>1</major>
	<minor>0</minor>
</specVersion>
<device>
	<deviceType>urn:schemas-denon-com:device:AiosDevice:1</deviceType>
	<friendlyName>Home Theater</friendlyName>
	<manufacturer>Denon</manufacturer>
	<manufacturerURL>http://www.denon.com</manufacturerURL>
	<modelName>Denon AVR-X2700H</modelName>
	<UDN>uuid:6338c852-7882-4074-99ef-75215c43231d</UDN>
	<deviceList>
	<device>
		<deviceType>urn:schemas-upnp-org:device:MediaRenderer:1</deviceType>
		<friendlyName>Home Theater</friendlyName>
		<manufacturer>Denon</manufacturer>
		<UDN>uuid:3ed6cf5c-5f11-4592-b47a-8cd720342891</UDN>
		<serviceList>
		<service>
			<serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType>
			<serviceId>urn:upnp-org:serviceId:AVTransport</serviceId>
			<SCPDURL>/upnp/scpd/renderer_dvc/AVTransport.xml</SCPDURL>
			<controlURL>/upnp/control/renderer_dvc/AVTransport</controlURL>
			<eventSubURL>/upnp/event/renderer_dvc/AVTransport</eventSubURL>
		</service>
		<service>
			<serviceType>urn:schemas-upnp-org:service:ConnectionManager:1</serviceType>
			<serviceId>urn:upnp-org:serviceId:ConnectionManager</serviceId>
			<SCPDURL>/upnp/scpd/renderer_dvc/ConnectionManager.xml</SCPDURL>
			<controlURL>/upnp/control/renderer_dvc/ConnectionManager</controlURL>
			<eventSubURL>/upnp/event/renderer_dvc/ConnectionManager</eventSubURL>
		</service>
		<service>
			<serviceType>urn:schemas-upnp-org:service:RenderingControl:1</serviceType>
			<serviceId>urn:upnp-org:serviceId:RenderingControl</serviceId>
			<SCPDURL>/upnp/scpd/renderer_dvc/RenderingControl.xml</SCPDURL>
			<controlURL>/upnp/control/renderer_dvc/RenderingControl</controlURL>
			<eventSubURL>/upnp/event/renderer_dvc/RenderingControl</eventSubURL>
		</service>
		</serviceList>
	</device>
	<device>
		<deviceType>urn:schemas-denon-com:device:AiosServices:1</deviceType>
		<friendlyName>AiosServices</friendlyName>
		<UDN>uuid:59edd68d-c5b8-40cc-87bb-45b455b1860f</UDN>
		<serviceList>
		<service>
			<serviceType>urn:schemas-denon-com:service:ErrorHandler:1</serviceType>
			<serviceId>urn:denon-com:serviceId:ErrorHandler</serviceId>
			<SCPDURL>/upnp/scpd/AiosServicesDvc/ErrorHandler.xml</SCPDURL>
			<controlURL>/upnp/control/AiosServicesDvc/ErrorHandler</controlURL>
			<eventSubURL>/upnp/event/AiosServicesDvc/ErrorHandler</eventSubURL>
		</service>
		</serviceList>
	</device>
	<device>
		<deviceType>urn:schemas-upnp-org:device:MediaServer:1</deviceType>
		<friendlyName>Home Theater</friendlyName>
		<UDN>uuid:0c1ed84f-37ce-48cb-944f-55c247f74cec</UDN>
		<serviceList>
		<service>
			<serviceType>urn:schemas-upnp-org:service:ContentDirectory:1</serviceType>
			<serviceId>urn:upnp-org:serviceId:ContentDirectory</serviceId>
			<SCPDURL>/upnp/scpd/ams_dvc/ContentDirectory.xml</SCPDURL>
			<controlURL>/upnp/control/ams_dvc/ContentDirectory</controlURL>
			<eventSubURL>/upnp/event/ams_dvc/ContentDirectory</eventSubURL>
		</service>
		<service>
			<serviceType>urn:schemas-upnp-org:service:ConnectionManager:1</serviceType>
			<serviceId>urn:upnp-org:serviceId:ConnectionManager</serviceId>
			<SCPDURL>/upnp/scpd/ams_dvc/ConnectionManager.xml</SCPDURL>
			<controlURL>/upnp/control/ams_dvc/ConnectionManager</controlURL>
			<eventSubURL>/upnp/event/ams_dvc/ConnectionManager</eventSubURL>
		</service>
		</serviceList>
	</device>
	</deviceList>
</device>
</root>`,
			wantErr:              false,
			wantAVTransportURL:   "http://example.com:8080/upnp/control/renderer_dvc/AVTransport",
			wantRenderingURL:     "http://example.com:8080/upnp/control/renderer_dvc/RenderingControl",
			wantConnectionMgrURL: "http://example.com:8080/upnp/control/renderer_dvc/ConnectionManager",
		},
		{
			name: "No AVTransport service",
			raw: `<?xml version="1.0"?>
<root>
	<device>
		<serviceList>
			<service>
				<serviceType>urn:schemas-upnp-org:service:RenderingControl:1</serviceType>
				<serviceId>urn:upnp-org:serviceId:RenderingControl</serviceId>
				<controlURL>/upnp/control/RenderingControl1</controlURL>
				<eventSubURL>/upnp/event/RenderingControl1</eventSubURL>
			</service>
		</serviceList>
	</device>
</root>`,
			wantErr: true,
		},
		{
			name: "Deeply nested device",
			raw: `<?xml version="1.0"?>
<root>
	<device>
		<deviceType>urn:schemas-upnp-org:device:Basic:1</deviceType>
		<deviceList>
			<device>
				<deviceType>urn:schemas-upnp-org:device:Basic:1</deviceType>
				<deviceList>
					<device>
						<deviceType>urn:schemas-upnp-org:device:MediaRenderer:1</deviceType>
						<serviceList>
							<service>
								<serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType>
								<serviceId>urn:upnp-org:serviceId:AVTransport</serviceId>
								<controlURL>/deep/AVTransport</controlURL>
								<eventSubURL>/deep/event/AVTransport</eventSubURL>
							</service>
						</serviceList>
					</device>
				</deviceList>
			</device>
		</deviceList>
	</device>
</root>`,
			wantErr:            false,
			wantAVTransportURL: "http://example.com:8080/deep/AVTransport",
		},
	}

	baseURL, _ := url.Parse("http://example.com:8080")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseDMRFromXML([]byte(tt.raw), baseURL)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseDMRFromXML() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("ParseDMRFromXML() unexpected error: %v", err)
			}

			if tt.wantAVTransportURL != "" && result.AvtransportControlURL != tt.wantAVTransportURL {
				t.Errorf("AvtransportControlURL = %q, want %q", result.AvtransportControlURL, tt.wantAVTransportURL)
			}

			if tt.wantRenderingURL != "" && result.RenderingControlURL != tt.wantRenderingURL {
				t.Errorf("RenderingControlURL = %q, want %q", result.RenderingControlURL, tt.wantRenderingURL)
			}

			if tt.wantConnectionMgrURL != "" && result.ConnectionManagerURL != tt.wantConnectionMgrURL {
				t.Errorf("ConnectionManagerURL = %q, want %q", result.ConnectionManagerURL, tt.wantConnectionMgrURL)
			}
		})
	}
}

func TestDMRextractorEmbeddedDevice(t *testing.T) {
	// Test full HTTP flow with embedded device XML
	raw := `<?xml version="1.0" encoding="utf-8"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
<device>
	<deviceType>urn:schemas-denon-com:device:AiosDevice:1</deviceType>
	<friendlyName>Home Theater</friendlyName>
	<deviceList>
	<device>
		<deviceType>urn:schemas-upnp-org:device:MediaRenderer:1</deviceType>
		<friendlyName>Home Theater</friendlyName>
		<serviceList>
		<service>
			<serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType>
			<serviceId>urn:upnp-org:serviceId:AVTransport</serviceId>
			<controlURL>/upnp/control/renderer_dvc/AVTransport</controlURL>
			<eventSubURL>/upnp/event/renderer_dvc/AVTransport</eventSubURL>
		</service>
		<service>
			<serviceType>urn:schemas-upnp-org:service:RenderingControl:1</serviceType>
			<serviceId>urn:upnp-org:serviceId:RenderingControl</serviceId>
			<controlURL>/upnp/control/renderer_dvc/RenderingControl</controlURL>
			<eventSubURL>/upnp/event/renderer_dvc/RenderingControl</eventSubURL>
		</service>
		</serviceList>
	</device>
	</deviceList>
</device>
</root>`

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(raw))
	}))
	defer testServer.Close()

	result, err := DMRextractor(context.Background(), testServer.URL)
	if err != nil {
		t.Fatalf("DMRextractor() with embedded device failed: %v", err)
	}

	// Verify embedded device services were found
	if result.AvtransportControlURL == "" {
		t.Error("AvtransportControlURL should not be empty for embedded device")
	}

	if result.RenderingControlURL == "" {
		t.Error("RenderingControlURL should not be empty for embedded device")
	}

	// Verify the URLs contain the embedded device path
	expectedPath := "/upnp/control/renderer_dvc/AVTransport"
	if !containsPath(result.AvtransportControlURL, expectedPath) {
		t.Errorf("AvtransportControlURL should contain %q, got %q", expectedPath, result.AvtransportControlURL)
	}
}

func containsPath(fullURL, expectedPath string) bool {
	parsed, err := url.Parse(fullURL)
	if err != nil {
		return false
	}
	return parsed.Path == expectedPath
}

func TestParseAllDMRFromXML(t *testing.T) {
	// Test with multiple MediaRenderers in a single root device
	raw := `<?xml version="1.0" encoding="utf-8"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
<device>
	<deviceType>urn:schemas-example-com:device:MultiZone:1</deviceType>
	<friendlyName>Multi-Zone Receiver</friendlyName>
	<UDN>uuid:root-device-123</UDN>
	<deviceList>
	<device>
		<deviceType>urn:schemas-upnp-org:device:MediaRenderer:1</deviceType>
		<friendlyName>Zone 1</friendlyName>
		<UDN>uuid:zone1-456</UDN>
		<serviceList>
		<service>
			<serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType>
			<serviceId>urn:upnp-org:serviceId:AVTransport</serviceId>
			<controlURL>/zone1/AVTransport</controlURL>
			<eventSubURL>/zone1/event/AVTransport</eventSubURL>
		</service>
		<service>
			<serviceType>urn:schemas-upnp-org:service:RenderingControl:1</serviceType>
			<serviceId>urn:upnp-org:serviceId:RenderingControl</serviceId>
			<controlURL>/zone1/RenderingControl</controlURL>
			<eventSubURL>/zone1/event/RenderingControl</eventSubURL>
		</service>
		</serviceList>
	</device>
	<device>
		<deviceType>urn:schemas-upnp-org:device:MediaRenderer:1</deviceType>
		<friendlyName>Zone 2</friendlyName>
		<UDN>uuid:zone2-789</UDN>
		<serviceList>
		<service>
			<serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType>
			<serviceId>urn:upnp-org:serviceId:AVTransport</serviceId>
			<controlURL>/zone2/AVTransport</controlURL>
			<eventSubURL>/zone2/event/AVTransport</eventSubURL>
		</service>
		<service>
			<serviceType>urn:schemas-upnp-org:service:RenderingControl:1</serviceType>
			<serviceId>urn:upnp-org:serviceId:RenderingControl</serviceId>
			<controlURL>/zone2/RenderingControl</controlURL>
			<eventSubURL>/zone2/event/RenderingControl</eventSubURL>
		</service>
		</serviceList>
	</device>
	<device>
		<deviceType>urn:schemas-upnp-org:device:MediaServer:1</deviceType>
		<friendlyName>Content Server</friendlyName>
		<UDN>uuid:server-000</UDN>
		<serviceList>
		<service>
			<serviceType>urn:schemas-upnp-org:service:ContentDirectory:1</serviceType>
			<serviceId>urn:upnp-org:serviceId:ContentDirectory</serviceId>
			<controlURL>/server/ContentDirectory</controlURL>
			<eventSubURL>/server/event/ContentDirectory</eventSubURL>
		</service>
		</serviceList>
	</device>
	</deviceList>
</device>
</root>`

	baseURL, _ := url.Parse("http://192.168.1.100:8080")

	results, err := ParseAllDMRFromXML([]byte(raw), baseURL)
	if err != nil {
		t.Fatalf("ParseAllDMRFromXML() unexpected error: %v", err)
	}

	// Should find exactly 2 MediaRenderers (Zone 1 and Zone 2), not the MediaServer
	if len(results) != 2 {
		t.Errorf("ParseAllDMRFromXML() returned %d devices, want 2", len(results))
	}

	// Verify each device has correct friendly name and URLs
	foundZone1 := false
	foundZone2 := false

	for _, dev := range results {
		switch dev.FriendlyName {
		case "Zone 1":
			foundZone1 = true
			if dev.UDN != "uuid:zone1-456" {
				t.Errorf("Zone 1 UDN = %q, want %q", dev.UDN, "uuid:zone1-456")
			}
			if !containsPath(dev.AvtransportControlURL, "/zone1/AVTransport") {
				t.Errorf("Zone 1 AVTransport URL incorrect: %q", dev.AvtransportControlURL)
			}
		case "Zone 2":
			foundZone2 = true
			if dev.UDN != "uuid:zone2-789" {
				t.Errorf("Zone 2 UDN = %q, want %q", dev.UDN, "uuid:zone2-789")
			}
			if !containsPath(dev.AvtransportControlURL, "/zone2/AVTransport") {
				t.Errorf("Zone 2 AVTransport URL incorrect: %q", dev.AvtransportControlURL)
			}
		default:
			t.Errorf("Unexpected device: %q", dev.FriendlyName)
		}
	}

	if !foundZone1 {
		t.Error("Zone 1 not found in results")
	}
	if !foundZone2 {
		t.Error("Zone 2 not found in results")
	}
}

func TestLoadDevicesFromLocationMultiple(t *testing.T) {
	// Test full HTTP flow with multiple MediaRenderers
	raw := `<?xml version="1.0"?>
<root>
<device>
	<friendlyName>Dual Zone Amp</friendlyName>
	<deviceList>
	<device>
		<friendlyName>Living Room</friendlyName>
		<UDN>uuid:living-room</UDN>
		<serviceList>
		<service>
			<serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType>
			<serviceId>urn:upnp-org:serviceId:AVTransport</serviceId>
			<controlURL>/living/AVTransport</controlURL>
			<eventSubURL>/living/event</eventSubURL>
		</service>
		</serviceList>
	</device>
	<device>
		<friendlyName>Kitchen</friendlyName>
		<UDN>uuid:kitchen</UDN>
		<serviceList>
		<service>
			<serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType>
			<serviceId>urn:upnp-org:serviceId:AVTransport</serviceId>
			<controlURL>/kitchen/AVTransport</controlURL>
			<eventSubURL>/kitchen/event</eventSubURL>
		</service>
		</serviceList>
	</device>
	</deviceList>
</device>
</root>`

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(raw))
	}))
	defer testServer.Close()

	results, err := LoadDevicesFromLocation(context.Background(), testServer.URL)
	if err != nil {
		t.Fatalf("LoadDevicesFromLocation() failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("LoadDevicesFromLocation() returned %d devices, want 2", len(results))
	}

	// Verify device names
	names := make(map[string]bool)
	for _, dev := range results {
		names[dev.FriendlyName] = true
	}

	if !names["Living Room"] {
		t.Error("Living Room not found")
	}
	if !names["Kitchen"] {
		t.Error("Kitchen not found")
	}
}
