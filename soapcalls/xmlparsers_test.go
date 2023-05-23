package soapcalls

import (
	"context"
	"net/http"
	"net/http/httptest"
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
