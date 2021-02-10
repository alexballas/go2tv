package iptools

import (
	"net"
	"net/url"
	"strings"
)

// URLtoListenIPandPort for a given internal URL, find the correct IP/Interface to listen to.
func URLtoListenIPandPort(u string) (string, error) {
	parsedURL, err := url.Parse(u)
	if err != nil {
		return "", err
	}
	conn, err := net.Dial("udp", parsedURL.Host)
	if err != nil {
		return "", err
	}
	res := strings.Split(conn.LocalAddr().String(), ":")[0] + ":3500"
	return res, nil
}
