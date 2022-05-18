package utils

import (
	"net"
	"strconv"
	"strings"
	"testing"
)

func TestURLtoListenIPandPort(t *testing.T) {
	tt := []struct {
		name         string
		input        string
		wantFromPort int
		wantToPort   int
	}{
		{
			`Test #1`,
			`http://192.168.88.244:9197/dmr`,
			3500,
			4500,
		},
		{
			`Test #2`,
			`http://192.168.2.211/dmr`,
			3500,
			4500,
		},
		{
			`Test #3`,
			`https://192.168.1.2/dmr`,
			3500,
			4500,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			out, err := URLtoListenIPandPort(tc.input)
			if err != nil {
				t.Fatalf("%s: Failed to call URLtoListenIPandPort due to %s", tc.name, err.Error())
			}
			outSplit := strings.Split(out, ":")

			if len(outSplit) < 2 {
				t.Fatalf("%s: Not in ip:port format: %s", tc.name, err.Error())
			}

			outInt, _ := strconv.Atoi(outSplit[1])

			if outInt < tc.wantFromPort || outInt > tc.wantToPort {
				t.Fatalf("%s: got: %s, wanted port between: %d - %d.", tc.name, out, tc.wantFromPort, tc.wantToPort)
			}
		})
	}
}
func TestCheckAndPickPort(t *testing.T) {
	tt := []struct {
		name      string
		inputHost string
		inputPort int
	}{
		{
			`Test #1`,
			"127.0.0.1",
			3000,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			_, err := checkAndPickPort(tc.inputHost, tc.inputPort)
			if err != nil {
				t.Fatalf("%s: Failed to call TestCheckAndPickPort due to %s", tc.name, err.Error())
			}
		})
	}
}

func TestHostPortIsAlive(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("HostPortIsAlive: failed to start server")
	}
	go func() {
		defer ln.Close()
		_, _ = ln.Accept()
	}()

	if !HostPortIsAlive(ln.Addr().String()) {
		t.Fatalf("HostPortIsAlive: expected true")
	}
}
