package soapcalls

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

const secureRendererXML = `<root><device><serviceList><service>` +
	`<serviceId>urn:upnp-org:serviceId:AVTransport</serviceId>` +
	`<controlURL>/control</controlURL><eventSubURL>/event</eventSubURL>` +
	`</service></serviceList></device></root>`

func TestRendererEndpointValidation(t *testing.T) {
	if _, err := DMRextractor(context.Background(), "ftp://127.0.0.1/renderer"); err == nil {
		t.Fatal("ftp accepted")
	}
	if _, err := DMRextractor(context.Background(), "http://user:pass@127.0.0.1/renderer"); err == nil {
		t.Fatal("userinfo accepted")
	}

	large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxRendererDescriptionBody+1)))
	}))
	defer large.Close()
	_, err := DMRextractor(context.Background(), large.URL)
	if !errors.Is(err, errResponseTooLarge) {
		t.Fatalf("large error %v", err)
	}

	crossService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.ReplaceAll(secureRendererXML, "/control", "http://192.0.2.1/control")))
	}))
	defer crossService.Close()
	if _, err := DMRextractor(context.Background(), crossService.URL); err == nil {
		t.Fatal("cross-host service accepted")
	}
}

// A renderer reached through a router is not on any of our own subnets, and on
// Android interface enumeration is blocked so nothing looks local at all. Neither
// case may be rejected: validation pins the address, it does not require the
// device to share a link with us.
func TestRendererLocationAcceptsRoutedAddress(t *testing.T) {
	u, pinned, err := validateRendererLocation(context.Background(), "http://192.0.2.1:8080/desc.xml")
	if err != nil {
		t.Fatalf("validateRendererLocation() err = %v, want nil", err)
	}

	if u.Host != "192.0.2.1:8080" {
		t.Fatalf("validateRendererLocation() host = %q, want %q", u.Host, "192.0.2.1:8080")
	}

	if !pinned.Equal(net.ParseIP("192.0.2.1")) {
		t.Fatalf("validateRendererLocation() pinned = %v, want 192.0.2.1", pinned)
	}
}

func TestRendererRedirectCannotCrossHost(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(secureRendererXML)) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(target.URL, "127.0.0.1", "localhost", 1), http.StatusFound)
	}))
	defer redirect.Close()
	if _, err := DMRextractor(context.Background(), redirect.URL); err == nil {
		t.Fatal("cross-host redirect followed")
	}
}

func TestRendererClientClosesConnWhenServerIgnoresClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	closed := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			closed <- err
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		if _, err := http.ReadRequest(br); err != nil {
			closed <- err
			return
		}
		// Keep-alive response that ignores the request's Connection: close.
		_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/xml\r\nContent-Length: %d\r\n\r\n%s", len(secureRendererXML), secureRendererXML)
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, err = br.ReadByte()
		closed <- err
	}()

	if _, err := DMRextractor(context.Background(), "http://"+ln.Addr().String()+"/desc.xml"); err != nil {
		t.Fatalf("DMRextractor: %v", err)
	}
	if err := <-closed; !errors.Is(err, io.EOF) {
		t.Fatalf("client left conn open: got %v, want EOF", err)
	}
}

// Discovery loads unsolicited LOCATIONs through LoadDevicesFromPinnedLocation:
// it must never dial anything but the pin and never resolve a name, while still
// accepting the description shapes real renderers use.
func TestLoadDevicesFromPinnedLocation(t *testing.T) {
	const avtService = `<service><serviceId>urn:upnp-org:serviceId:AVTransport</serviceId>` +
		`<controlURL>%s</controlURL><eventSubURL>/event</eventSubURL></service>`
	description := func(urlBase, controlURL string) string {
		return `<root><URLBase>` + urlBase + `</URLBase><device><serviceList>` +
			fmt.Sprintf(avtService, controlURL) + `</serviceList></device></root>`
	}

	tt := []struct {
		name     string
		location func(srvURL string) string
		pin      string
		body     string
		wantErr  bool
		wantHits int
	}{
		{"relative endpoints", func(u string) string { return u + "/desc.xml" }, "127.0.0.1", description("", "/control"), false, 1},
		{"literal URLBase on another port", func(u string) string { return u + "/desc.xml" }, "127.0.0.1", description("http://127.0.0.1:1/", "/control"), false, 1},
		{"hostname control URL", func(u string) string { return u + "/desc.xml" }, "127.0.0.1", description("", "http://localhost:1/control"), true, 1},
		{"location host differs from pin", func(u string) string { return u + "/desc.xml" }, "192.0.2.1", description("", "/control"), true, 0},
		{"hostname location", func(u string) string { return strings.Replace(u, "127.0.0.1", "localhost", 1) + "/desc.xml" }, "127.0.0.1", description("", "/control"), true, 0},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			hits := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			devices, err := LoadDevicesFromPinnedLocation(context.Background(), tc.location(srv.URL), netip.MustParseAddr(tc.pin))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if hits != tc.wantHits {
				t.Fatalf("server hits = %d, want %d", hits, tc.wantHits)
			}
			if !tc.wantErr && (len(devices) != 1 || devices[0].PinnedIP != tc.pin) {
				t.Fatalf("devices = %+v, want one device pinned to %s", devices, tc.pin)
			}
		})
	}
}
