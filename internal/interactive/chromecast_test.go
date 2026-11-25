package interactive

import "testing"

func TestMediaEnded(t *testing.T) {
	tt := []struct {
		name             string
		current, recvDur float32
		offset           int
		sourceDur        float64
		want             bool
	}{
		// Live transcode: receiver duration is the buffer edge just ahead of playback.
		{name: "transcode near buffer edge keeps playing", current: 9, recvDur: 10, sourceDur: 110, want: false},
		{name: "transcode after seek near source end", current: 9, recvDur: 10, offset: 100, sourceDur: 110, want: true},
		{name: "native near end", current: 109, recvDur: 110, want: true},
		{name: "native mid stream", current: 50, recvDur: 110, want: false},
		{name: "unknown duration", current: 50, want: false},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			if got := mediaEnded(tc.current, tc.recvDur, tc.offset, tc.sourceDur); got != tc.want {
				t.Fatalf("mediaEnded = %v, want %v", got, tc.want)
			}
		})
	}
}
