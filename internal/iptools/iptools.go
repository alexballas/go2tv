package iptools

import (
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/pkg/errors"
)

// URLtoListenIPandPort for a given internal URL, find the correct IP/Interface to listen to.
func URLtoListenIPandPort(u string) (string, error) {
	parsedURL, err := url.Parse(u)
	if err != nil {
		return "", errors.Wrap(err, "URLtoListenIPandPort parse failure")
	}

	conn, err := net.Dial("udp", parsedURL.Host)
	if err != nil {
		return "", errors.Wrap(err, "URLtoListenIPandPort UDP call error")
	}

	ipToListen := strings.Split(conn.LocalAddr().String(), ":")[0]
	portToListen, err := checkAndPickPort(ipToListen, 3500)
	if err != nil {
		return "", errors.Wrap(err, "URLtoListenIPandPort port error")
	}

	res := ipToListen + ":" + portToListen
	return res, nil
}

func checkAndPickPort(ip string, port int) (string, error) {
	var numberOfchecks int
CHECK:
	numberOfchecks++
	conn, err := net.Listen("tcp", ip+":"+strconv.Itoa(port))
	if err != nil {
		if strings.Contains(err.Error(), "address already in use") {
			if numberOfchecks == 1000 {
				return "", errors.Wrap(err, "Port pick error. Checked 1000 ports")
			}
			port++
			goto CHECK
		} else {
			return "", errors.Wrap(err, "Port pick error")
		}
	}
	conn.Close()
	return strconv.Itoa(port), nil
}
