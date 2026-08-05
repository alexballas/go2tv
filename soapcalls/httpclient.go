package soapcalls

import (
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/hashicorp/go-retryablehttp"
)

const (
	soapHTTPClientTimeout         = 20 * time.Second
	soapHTTPDialTimeout           = 5 * time.Second
	soapHTTPKeepAlive             = 30 * time.Second
	soapHTTPTLSHandshakeTimeout   = 5 * time.Second
	soapHTTPResponseHeaderTimeout = 10 * time.Second
	soapHTTPExpectContinueTimeout = time.Second
	soapHTTPIdleConnTimeout       = 90 * time.Second
)

var soapHTTPTransport = &http.Transport{
	Proxy: http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{
		Timeout:   soapHTTPDialTimeout,
		KeepAlive: soapHTTPKeepAlive,
	}).DialContext,
	TLSHandshakeTimeout:   soapHTTPTLSHandshakeTimeout,
	ResponseHeaderTimeout: soapHTTPResponseHeaderTimeout,
	ExpectContinueTimeout: soapHTTPExpectContinueTimeout,
	IdleConnTimeout:       soapHTTPIdleConnTimeout,
}

func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   soapHTTPClientTimeout,
		Transport: soapHTTPTransport,
	}
}

func (p *TVPayload) httpClient(endpoint *url.URL) *http.Client {
	if p != nil && endpoint != nil {
		if pinned := net.ParseIP(p.PinnedIP); pinned != nil {
			return rendererHTTPClient(endpoint, pinned)
		}
	}
	return newHTTPClient()
}

func (p *TVPayload) retryableHTTPClient(endpoint *url.URL, retryMax int) *http.Client {
	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = retryMax
	retryClient.Logger = nil
	retryClient.HTTPClient = p.httpClient(endpoint)
	return retryClient.StandardClient()
}
