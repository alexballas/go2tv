package utils

import (
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
				t.Errorf("%s: Failed to call URLtoListenIPandPort due to %s", tc.name, err.Error())
				return
			}
			outSplit := strings.Split(out, ":")

			if len(outSplit) < 2 {
				t.Errorf("%s: Not in ip:port format: %s", tc.name, err.Error())
				return
			}

			outInt, _ := strconv.Atoi(outSplit[1])

			if outInt < tc.wantFromPort || outInt > tc.wantToPort {
				t.Errorf("%s: got: %s, wanted port between: %d - %d.", tc.name, out, tc.wantFromPort, tc.wantToPort)
				return
			}
		})
	}
}
