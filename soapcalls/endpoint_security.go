package soapcalls

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	maxRendererDescriptionBody = 2 << 20
	maxSOAPResponseBody        = 1 << 20
)

var errResponseTooLarge = errors.New("renderer response too large")

func validateRendererLocation(ctx context.Context, raw string) (*url.URL, net.IP, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, nil, errors.New("renderer URL invalid")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, nil, errors.New("renderer URL scheme invalid")
	}
	if u.User != nil {
		return nil, nil, errors.New("renderer URL userinfo forbidden")
	}
	addresses, err := net.DefaultResolver.LookupIP(ctx, "ip", u.Hostname())
	if err != nil {
		return nil, nil, fmt.Errorf("resolve renderer: %w", err)
	}
	if len(addresses) == 0 {
		return nil, nil, errors.New("renderer did not resolve")
	}

	// The pinned address is what every later request dials, so a renderer cannot
	// answer discovery from one host and point control traffic at another. We do
	// not additionally require it to sit on one of our own subnets: renderers
	// reached through a router are legitimate, and Android blocks interface
	// enumeration outright, which made such a check reject every device.
	return u, addresses[0], nil
}

func rendererHTTPClient(location *url.URL, pinned net.IP) *http.Client {
	base := soapHTTPTransport.Clone()
	base.Proxy = nil
	base.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		port := location.Port()
		if port == "" {
			if location.Scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
		return (&net.Dialer{Timeout: soapHTTPDialTimeout, KeepAlive: soapHTTPKeepAlive}).DialContext(ctx, network, net.JoinHostPort(pinned.String(), port))
	}
	return &http.Client{
		Timeout:   soapHTTPClientTimeout,
		Transport: base,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) == 0 || !strings.EqualFold(req.URL.Hostname(), via[0].URL.Hostname()) {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}

func validateServiceEndpoints(ctx context.Context, pinned net.IP, extracted *DMRextracted) error {
	values := []string{extracted.AvtransportControlURL, extracted.AvtransportEventSubURL, extracted.RenderingControlURL, extracted.ConnectionManagerURL}
	for _, raw := range values {
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.User != nil || u.Scheme != "http" && u.Scheme != "https" {
			return errors.New("service endpoint invalid")
		}
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip", u.Hostname())
		if err != nil {
			return fmt.Errorf("resolve service endpoint: %w", err)
		}
		matched := false
		for _, ip := range ips {
			if ip.Equal(pinned) {
				matched = true
				break
			}
		}
		if !matched {
			return errors.New("service endpoint host differs from discovery host")
		}
		if port := u.Port(); port != "" {
			if _, err := strconv.Atoi(port); err != nil {
				return errors.New("service endpoint port invalid")
			}
		}
	}
	return nil
}

func readCapped(body io.Reader, limit int64) ([]byte, error) {
	reader := io.LimitReader(body, limit+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errResponseTooLarge
	}
	return data, nil
}
