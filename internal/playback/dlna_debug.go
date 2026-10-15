package playback

import (
	"fmt"
	"strings"
	"time"
)

// Temporary DLNA autoplay diagnostics. All records use DLNA_AUTOPLAY so traces
// can be filtered and this instrumentation can be removed after device testing.
type dlnaDebug struct {
	write         func(string, ...any)
	trigger       string
	values        map[string]string
	failures      map[string]int
	lastSample    time.Time
	lastSampleKey string
}

func (d *dlnaDebug) log(event string, fields ...any) {
	if d.write != nil {
		d.write(event, append([]any{"trigger", d.trigger}, fields...)...)
	}
}

func (d *dlnaDebug) change(key, value, event string, fields ...any) {
	if d.write == nil {
		return
	}
	if d.values == nil {
		d.values = make(map[string]string)
	}
	if previous, ok := d.values[key]; ok && previous == value {
		return
	}
	d.values[key] = value
	d.log(event, fields...)
}

func (d *dlnaDebug) sample(now time.Time, key string, fields ...any) {
	if d.write == nil {
		return
	}
	if key != d.lastSampleKey || d.lastSample.IsZero() || now.Sub(d.lastSample) >= 5*time.Second {
		d.log("end_sample", fields...)
		d.lastSample, d.lastSampleKey = now, key
	}
}

func (d *dlnaDebug) result(operation string, started time.Time, err error) {
	if d.write == nil {
		return
	}
	if d.failures == nil {
		d.failures = make(map[string]int)
	}
	elapsed := time.Since(started).Milliseconds()
	if err != nil {
		d.failures[operation]++
		count := d.failures[operation]
		if count == 1 || count%10 == 0 {
			d.log("poll_failed", "operation", operation, "consecutive_failures", count, "elapsed_ms", elapsed, "error", err)
		}
		return
	}
	if count := d.failures[operation]; count != 0 {
		d.log("poll_recovered", "operation", operation, "previous_failures", count, "elapsed_ms", elapsed)
		d.failures[operation] = 0
	}
	if elapsed >= 500 {
		d.log("poll_slow", "operation", operation, "elapsed_ms", elapsed)
	}
}

// DLNADebugMessage formats temporary structured diagnostics for string loggers.
func DLNADebugMessage(event string, fields ...any) string {
	var out strings.Builder
	fmt.Fprintf(&out, "DLNA_AUTOPLAY %s", event)
	for i := 0; i+1 < len(fields); i += 2 {
		fmt.Fprintf(&out, " %v=%q", fields[i], fmt.Sprint(fields[i+1]))
	}
	return out.String()
}
