package playback

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// A Play response acknowledges a command, not playback. Failed startup must
// release the session as an error rather than silently skipping an unseen item.
type dlnaStartup struct {
	since         time.Time
	attemptAt     time.Time
	stoppedSince  time.Time
	transitioning bool
	retries       int
	done          bool
}

type dlnaStartupResult struct {
	failure *MonitorEvent
	retry   bool
	// reload re-sends SetAVTransportURI: the renderer still reports another track.
	reload bool
}

func (s *dlnaStartup) check(now time.Time, state string, confirmed bool, observedURI, currentURI string, started, retryable bool) dlnaStartupResult {
	if s.done {
		return dlnaStartupResult{}
	}
	if started {
		s.done = true
		return dlnaStartupResult{}
	}
	if state == "PAUSED_PLAYBACK" {
		// Pause before the first PLAYING observation must not expire startup.
		s.since, s.attemptAt, s.stoppedSince = time.Time{}, time.Time{}, time.Time{}
		return dlnaStartupResult{}
	}
	if s.since.IsZero() {
		s.since = now
	}
	if s.attemptAt.IsZero() {
		s.attemptAt = s.since
	}
	if confirmed && state == "TRANSITIONING" {
		s.transitioning = true
	}
	matching := sameTrackURI(observedURI, currentURI)
	// Samsung renderers can acknowledge Play right after an EOF and then sit in
	// STOPPED, still reporting the previous track. STOPPED is evidence either way.
	stopped := confirmed && state == "STOPPED"
	if stopped {
		if s.stoppedSince.IsZero() {
			s.stoppedSince = now
		}
	} else {
		s.stoppedSince = time.Time{}
	}
	stuck := !s.stoppedSince.IsZero() && now.Sub(s.attemptAt) >= dlnaStartupGrace && now.Sub(s.stoppedSince) >= dlnaStartupStoppedDelay &&
		(retryable || s.transitioning || !matching)
	if stuck && retryable && s.retries < dlnaStartupRetries {
		s.retries++
		s.attemptAt, s.stoppedSince = now, time.Time{}
		return dlnaStartupResult{retry: true, reload: !matching}
	}
	reason := ""
	switch {
	case now.Sub(s.since) >= dlnaStartupTimeout:
		reason = "renderer did not confirm playback before startup timeout"
	case stuck:
		reason = "renderer stopped before playback started"
	}
	if reason == "" {
		return dlnaStartupResult{}
	}
	return dlnaStartupResult{failure: &MonitorEvent{Terminal: TerminalError, Recovery: reason, DLNAStopped: stopped && matching, Err: fmt.Errorf("DLNA playback did not start: %s", reason)}}
}

// sameKnownURI is sameTrackURI for identity proofs: both URIs must be present
// and well formed, so placeholders can never confirm a gapless handoff.
func sameKnownURI(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	ua, errA := url.Parse(strings.TrimSpace(a))
	ub, errB := url.Parse(strings.TrimSpace(b))
	return errA == nil && errB == nil && ua.Host != "" && ub.Host != "" && sameTrackURI(a, b)
}

// sameTrackURI tolerates renderers that omit TrackURI, report a placeholder or
// re-escape it. Only two well-formed, different resources count as a mismatch.
func sameTrackURI(observed, current string) bool {
	if observed == "" || current == "" || observed == current {
		return true
	}
	o, oErr := url.Parse(strings.TrimSpace(observed))
	c, cErr := url.Parse(strings.TrimSpace(current))
	if oErr != nil || cErr != nil || o.Host == "" || c.Host == "" {
		return true
	}
	return strings.EqualFold(o.Host, c.Host) && o.Path == c.Path
}
