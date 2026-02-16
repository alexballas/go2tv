package soapcalls

import (
	"net"
	"net/http"
	"time"

	"github.com/hashicorp/go-retryablehttp"
)

const (
	soapHTTPClientTimeout         = 20 * time.Second
	soapHTTPDialTimeout           = 5 * time.Second
	soapHTTPKeepAlive             = 30 * time.Second
	soapHTTPTLSHandshakeTimeout   = 5 * time.Second
	soapHTTPResponseHeaderTimeout = 10 * time.Second
	soapHTTPExpectContinueTimeout = 1 * time.Second
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

func newRetryableHTTPClient(retryMax int) *http.Client {
	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = retryMax
	retryClient.Logger = nil
	retryClient.HTTPClient = newHTTPClient()

	return retryClient.StandardClient()
}
