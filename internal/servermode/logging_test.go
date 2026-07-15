//go:build !(android || ios)

package servermode

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTerminalOutputDisablesColorForRedirects(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, color := terminalOutput(file); color {
		t.Fatal("file redirect detected as color terminal")
	}
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readEnd.Close()
	defer writeEnd.Close()
	if _, color := terminalOutput(writeEnd); color {
		t.Fatal("pipe redirect detected as color terminal")
	}
}

func TestServerLoggerFiltersOnlyDebug(t *testing.T) {
	var output bytes.Buffer
	log := newServerLogger(&output, false)
	log.now = func() time.Time { return time.Date(2026, 7, 15, 14, 32, 8, 0, time.Local) }
	log.Debug("hidden")
	log.Info("Playback started: example.mp4")
	log.Warning("warning")
	log.Error("error")

	got := output.String()
	if strings.Contains(got, "hidden") {
		t.Fatalf("debug log visible: %q", got)
	}
	for _, want := range []string{
		"[2026-07-15 14:32:08] INFO  Playback started: example.mp4",
		"] WARNING warning",
		"] ERROR error",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("log missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("redirected log contains color: %q", got)
	}
}

func TestProtocolLoggerHidesAttemptErrorsWhenDebugDisabled(t *testing.T) {
	var output bytes.Buffer
	log := newServerLogger(&output, false)
	protocol := log.protocolOutput()
	_, _ = protocol.Write([]byte("{\"time\":\"2026-07-15T14:33:46Z\",\"level\":\"DEBUG\",\"msg\":\"request body\",\"Method\":\"POST\"}\n"))
	_, _ = protocol.Write([]byte("{\"time\":\"2026-07-15T14:33:47Z\",\"level\":\"ERROR\",\"msg\":\"connection failed\",\"Method\":\"Connect\"}\n"))
	if output.Len() != 0 {
		t.Fatalf("protocol attempt escaped debug filter: %q", output.String())
	}
}

func TestProtocolInfoRemainsDebugOnly(t *testing.T) {
	var output bytes.Buffer
	log := newServerLogger(&output, false)
	_, _ = log.protocolOutput().Write([]byte("{\"level\":\"INFO\",\"msg\":\"receiver status\"}\n"))
	if output.Len() != 0 {
		t.Fatalf("protocol info escaped debug filter: %q", output.String())
	}
}

func TestProtocolLoggerIncludesDebugWhenEnabled(t *testing.T) {
	var output bytes.Buffer
	log := newServerLogger(&output, true)
	protocol := log.protocolOutput()
	_, _ = protocol.Write([]byte("{\"level\":\"DEBUG\",\"msg\":\"message received\"}\n"))
	if got := output.String(); !strings.Contains(got, "DEBUG Protocol: message received") {
		t.Fatalf("debug log missing: %q", got)
	}
}

func TestProtocolAttemptErrorIncludesOriginalLevelInDebug(t *testing.T) {
	var output bytes.Buffer
	log := newServerLogger(&output, true)
	_, _ = log.protocolOutput().Write([]byte("{\"level\":\"ERROR\",\"msg\":\"connection failed\",\"Method\":\"Connect\"}\n"))
	got := output.String()
	if !strings.Contains(got, "DEBUG Protocol: connection failed") || !strings.Contains(got, "protocol_level=ERROR") {
		t.Fatalf("protocol error detail missing: %q", got)
	}
}
