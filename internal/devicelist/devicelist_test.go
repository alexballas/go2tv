package devicelist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"go2tv.app/go2tv/v2/devices"
)

// go2tv -l -json is a scripting contract: one JSON array on stdout, merged
// across rounds so one lost multicast reply does not drop a device, and an
// empty network is [] rather than an error.
func TestWriteJSON(t *testing.T) {
	tv := devices.Device{Name: `Living "Room" TV`, Addr: "http://192.168.1.20:9197/dmr", Type: devices.DeviceTypeDLNA}
	speaker := devices.Device{Name: "Kitchen", Addr: "http://192.168.1.30:8009", Type: devices.DeviceTypeChromecast, IsAudioOnly: true}
	errNetwork := errors.New("network down")

	type round struct {
		found  []devices.Device
		err    error
		hidden []string
	}
	tt := []struct {
		name       string
		rounds     []round
		cancelled  bool
		want       []Entry
		wantErr    bool
		wantHidden []string
	}{
		{
			name:   "second round fills a lost reply",
			rounds: []round{{found: []devices.Device{tv}}, {found: []devices.Device{tv, speaker}}},
			want: []Entry{
				{Name: "Kitchen", URL: speaker.Addr, Type: devices.DeviceTypeChromecast, AudioOnly: true},
				{Name: tv.Name, URL: tv.Addr, Type: devices.DeviceTypeDLNA},
			},
		},
		{
			name:   "no devices",
			rounds: []round{{err: devices.ErrNoDeviceAvailable}, {err: devices.ErrNoDeviceAvailable}},
			want:   []Entry{},
		},
		{
			name:   "scan error but a round found devices",
			rounds: []round{{err: errNetwork}, {found: []devices.Device{tv}}},
			want:   []Entry{{Name: tv.Name, URL: tv.Addr, Type: devices.DeviceTypeDLNA}},
		},
		{
			name:    "scan error and nothing found",
			rounds:  []round{{err: errNetwork}, {err: errNetwork}},
			wantErr: true,
		},
		{
			name:   "clean empty round beats a failed one",
			rounds: []round{{err: errNetwork}, {err: devices.ErrNoDeviceAvailable}},
			want:   []Entry{},
		},
		{
			name:      "cancelled with nothing found",
			rounds:    []round{{err: devices.ErrNoDeviceAvailable}, {err: devices.ErrNoDeviceAvailable}},
			cancelled: true,
			wantErr:   true,
		},
		{
			// The stderr notice must not lose a relay seen only in round one.
			name: "hidden URLs are the union across rounds",
			rounds: []round{
				{found: []devices.Device{tv}, hidden: []string{"http://192.168.1.50:49152/d.xml"}},
				{found: []devices.Device{tv}, hidden: []string{"http://192.168.1.60:1400/d.xml"}},
			},
			want:       []Entry{{Name: tv.Name, URL: tv.Addr, Type: devices.DeviceTypeDLNA}},
			wantHidden: []string{"http://192.168.1.50:49152/d.xml", "http://192.168.1.60:1400/d.xml"},
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			origScan, origHidden := scan, hidden
			t.Cleanup(func() { scan, hidden = origScan, origHidden })
			calls := 0
			scan = func(context.Context, int) ([]devices.Device, error) {
				r := tc.rounds[calls]
				calls++
				return r.found, r.err
			}
			hidden = func() []string { return tc.rounds[calls-1].hidden }

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelled {
				cancel()
			}
			var out bytes.Buffer
			gotHidden, err := WriteJSON(ctx, &out)
			if !tc.cancelled && calls != scanRounds {
				t.Fatalf("scan rounds = %d, want %d", calls, scanRounds)
			}
			if !slices.Equal(gotHidden, tc.wantHidden) {
				t.Fatalf("hidden = %q, want %q", gotHidden, tc.wantHidden)
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if strings.Count(out.String(), "\n") != 1 {
				t.Fatalf("output is not a single line: %q", out.String())
			}
			var got []Entry
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("output is not JSON: %v (%q)", err, out.String())
			}
			if got == nil || !slices.Equal(got, tc.want) {
				t.Fatalf("entries = %+v, want %+v", got, tc.want)
			}
		})
	}
}
