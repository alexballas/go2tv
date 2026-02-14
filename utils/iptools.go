package utils

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// GetOutboundIP gets the preferred outbound IP of this machine
func GetOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	return localAddr.IP.String()
}

// URLtoListenIPandPort for a given internal URL,
// find the correct IP/Interface to listen to.
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
	const maxAttempts = 1000
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		conn, err := net.Listen("tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
		if err != nil {
			if errors.Is(err, syscall.EADDRINUSE) {
				if attempt == maxAttempts {
					break
				}
				port++
				continue
			}

			return "", fmt.Errorf("port pick error: %w", err)
		}
		conn.Close()
		return strconv.Itoa(port), nil
	}

	return "", fmt.Errorf("port pick error. Exceeded maximum attempts")
}

// HostPortIsAlive - We use this function to periodically
// health check the selected device and decide if we want
// to keep the entry in the list or to remove it.
func HostPortIsAlive(h string) bool {
	conn, err := net.DialTimeout("tcp", h, time.Duration(2*time.Second))
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
