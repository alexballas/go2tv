package utils

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// URLtoListenIPandPort for a given internal URL, find the correct IP/Interface to listen to.
func URLtoListenIPandPort(u string) (string, error) {
	parsedURL, err := url.Parse(u)
	if err != nil {
		return "", fmt.Errorf("URLtoListenIPandPort parse error: %w", err)
	}

	callURL := parsedURL.Host
	if parsedURL.Port() == "" {
		switch parsedURL.Scheme {
		case "http":
			callURL = callURL + ":80"
		case "https":
			callURL = callURL + ":443"
		}
	}

	conn, err := net.Dial("udp", callURL)
	if err != nil {
		return "", fmt.Errorf("URLtoListenIPandPort UDP call error: %w", err)
	}

	ipToListen := strings.Split(conn.LocalAddr().String(), ":")[0]
	portToListen, err := checkAndPickPort(ipToListen, 3500)
	if err != nil {
		return "", fmt.Errorf("URLtoListenIPandPort port error: %w", err)
	}

	res := net.JoinHostPort(ipToListen, portToListen)

	return res, nil
}

func checkAndPickPort(ip string, port int) (string, error) {
	var numberOfchecks int
CHECK:
	numberOfchecks++
	conn, err := net.Listen("tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
	if err != nil {
		if strings.Contains(err.Error(), "address already in use") {
			if numberOfchecks == 1000 {
				return "", fmt.Errorf("port pick error. Checked 1000 ports: %w", err)
			}
			port++
			goto CHECK
		} else {
			return "", fmt.Errorf("port pick error: %w", err)
		}
	}
	conn.Close()
	return strconv.Itoa(port), nil
}

func HostPortIsAlive(h string) bool {
	conn, err := net.DialTimeout("tcp", h, time.Duration(2*time.Second))
	if err != nil {
		return false
	}
	defer conn.Close()
	return true
}
