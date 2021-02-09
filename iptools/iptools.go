package iptools

import (
	"log"
	"net"
	"net/url"
	"strings"
)

// URLtoListeIP for a given internal URL, find the correct IP/Interface to listen to.
func URLtoListeIP(u string) string {
	parsedURL, err := url.Parse(u)
	if err != nil {
		log.Fatal(err)
	}
	conn, err := net.Dial("udp", parsedURL.Host)
	if err != nil {
		log.Fatalln("Failed to establish connection")
	}
	return strings.Split(conn.LocalAddr().String(), ":")[0]
}
