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
