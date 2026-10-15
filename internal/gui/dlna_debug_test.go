//go:build !(android || ios)

package gui

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/httphandlers"
)

// Temporary diagnostics regression: real GUI monitor traces must reach the
// existing Export Diagnostics file, not just a test-only logging callback.
func TestDLNADiagnosticsExportMonitorContext(t *testing.T) {
	s := newDLNARecoveryScreen(t)
	s.Debug = newDebugWriter(100)
	s.tvdata.LogOutput = s.Debug
	session := s.prepareDLNAMonitor(s.serverStopCTX, s.tvdata)
	go session.run()
	session.HandleCallbackEvent(context.Background(), httphandlers.CallbackEvent{TransportState: "PLAYING"})
	deadline := time.Now().Add(time.Second)
	for {
		entries := strings.Join(debugLogEntries(s.Debug), "")
		if strings.Contains(entries, `"callback_state":"PLAYING"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("GUI monitor did not write callback diagnostics")
		}
		time.Sleep(time.Millisecond)
	}
	var exported bytes.Buffer
	if err := writeDiagnostics(&exported, s); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"DLNA_AUTOPLAY monitor_started", "DLNA_AUTOPLAY callback", `"monitor_epoch":1`, `"callback_state":"PLAYING"`, session.subscriptionPayload.CallbackURL, session.subscriptionPayload.ControlURL} {
		if !strings.Contains(exported.String(), want) {
			t.Fatalf("export missing %q:\n%s", want, exported.String())
		}
	}
}
