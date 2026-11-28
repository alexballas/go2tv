package interactive

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

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

func TestIdleWatchEnded(t *testing.T) {
	// Steps: "STATE:session" receiver statuses, "load" (LOAD about to go out),
	// "sent"/"failed" (LOAD result), "wait" (past the reload timeout).
	tt := []struct {
		name  string
		steps []string
		want  bool // whether the last status ends playback
	}{
		{name: "idle before playback", steps: []string{"IDLE:0"}, want: false},
		{name: "poll missed the end", steps: []string{"PLAYING:1", "IDLE:0"}, want: true},
		{name: "idle while skip reload pending", steps: []string{"PLAYING:1", "load", "IDLE:0"}, want: false},
		// The receiver reports the replaced session until it handles LOAD.
		{name: "stale session after reload sent", steps: []string{"PLAYING:1", "load", "sent", "BUFFERING:1", "IDLE:0"}, want: false},
		{name: "idle after reloaded session played", steps: []string{"PLAYING:1", "load", "sent", "PLAYING:2", "IDLE:0"}, want: true},
		{name: "reload never started a session", steps: []string{"PLAYING:1", "load", "sent", "wait", "IDLE:0"}, want: true},
		{name: "idle after failed reload", steps: []string{"PLAYING:1", "load", "failed", "IDLE:0"}, want: true},
		// Initial cast onto a receiver still playing an earlier cast.
		{name: "initial load replaces older cast", steps: []string{"load", "PLAYING:5", "sent", "PLAYING:5", "IDLE:0"}, want: false},
		{name: "initial load then natural end", steps: []string{"load", "sent", "PLAYING:1", "IDLE:0"}, want: true},
		{name: "same session id after relaunch", steps: []string{"load", "PLAYING:1", "sent", "wait", "PLAYING:1", "IDLE:0"}, want: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(0, 0)
			w := idleWatch{now: func() time.Time { return now }}
			got := false
			for _, step := range tc.steps {
				switch step {
				case "load":
					w.beginLoad()
				case "sent", "failed":
					w.loadSent(step == "sent")
				case "wait":
					now = now.Add(reloadTimeout + time.Second)
				default:
					state, id, _ := strings.Cut(step, ":")
					session, _ := strconv.Atoi(id)
					got = w.ended(state, session)
				}
			}
			if got != tc.want {
				t.Fatalf("ended = %v, want %v", got, tc.want)
			}
		})
	}
}
