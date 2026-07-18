//go:build !(android || ios)

package servermode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/internal/managedsession"
)

type managedHarness struct {
	t          *testing.T
	stdinWrite io.WriteCloser
	lines      chan string
	done       chan struct{}
	err        error
	cancel     context.CancelFunc
}

func startManagedChild(t *testing.T, cfg Config) *managedHarness {
	t.Helper()
	stdinRead, stdinWrite := io.Pipe()
	stdoutRead, stdoutWrite := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	h := &managedHarness{t: t, stdinWrite: stdinWrite, lines: make(chan string, 256), done: make(chan struct{}), cancel: cancel}
	go func() {
		h.err = runManaged(ctx, cfg, stdoutWrite, stdinRead)
		close(h.done)
		stdoutWrite.Close()
	}()
	go func() {
		scanner := bufio.NewScanner(stdoutRead)
		scanner.Buffer(make([]byte, 4096), managedsession.MaxFrameBytes)
		for scanner.Scan() {
			h.lines <- scanner.Text()
		}
		// Pipe close errors are the normal end-of-run signal here.
		_ = scanner.Err()
		close(h.lines)
	}()
	t.Cleanup(func() {
		cancel()
		stdinWrite.Close()
		go func() {
			for range h.lines {
			}
		}()
		select {
		case <-h.done:
		case <-time.After(10 * time.Second):
			t.Error("managed child never exited")
		}
	})
	return h
}

// wait returns the managed run result once the child exits.
func (h *managedHarness) wait(timeout time.Duration) (error, bool) {
	select {
	case <-h.done:
		return h.err, true
	case <-time.After(timeout):
		return nil, false
	}
}

func (h *managedHarness) sendFrame(frame managedsession.ParentFrame) {
	h.t.Helper()
	line, err := managedsession.EncodeParentFrame(frame)
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.stdinWrite.Write(line); err != nil {
		h.t.Fatal(err)
	}
}

// awaitManagedFrame consumes stdout lines until the next managed frame,
// failing on frame decode errors and returning human log lines via logs.
func (h *managedHarness) awaitManagedFrame(timeout time.Duration) (managedsession.ManagedFrame, []string) {
	h.t.Helper()
	var logs []string
	deadline := time.After(timeout)
	for {
		select {
		case line, ok := <-h.lines:
			if !ok {
				h.t.Fatalf("stdout closed before managed frame; logs=%q", logs)
			}
			frame, isFrame, err := managedsession.DecodeManagedLine([]byte(line))
			if err != nil {
				h.t.Fatalf("bad managed frame %q: %v", line, err)
			}
			if isFrame {
				return frame, logs
			}
			logs = append(logs, line)
		case <-deadline:
			h.t.Fatalf("no managed frame within %v; logs=%q", timeout, logs)
		}
	}
}

func validManagedConfig(t *testing.T) Config {
	t.Helper()
	return Config{Listen: "127.0.0.1:0", MediaRoots: []string{t.TempDir()}, Version: "test", ManagedChild: true}
}

func TestManagedChildReadinessAndBootstrap(t *testing.T) {
	h := startManagedChild(t, validManagedConfig(t))
	h.sendFrame(managedsession.ParentFrame{Type: managedsession.TypeDiscoverySnapshot, Revision: 1, Devices: []managedsession.Device{{Name: "TV", Protocol: "DLNA", Endpoint: "http://192.168.1.20:1400/device.xml"}}})

	frame, logs := h.awaitManagedFrame(10 * time.Second)
	if frame.Type != managedsession.TypeReady {
		t.Fatalf("first frame = %+v", frame)
	}
	if !strings.HasPrefix(frame.URL, "http://127.0.0.1:") || strings.Contains(frame.URL, ":0/") {
		t.Fatalf("ready URL = %q, want resolved port", frame.URL)
	}
	for _, log := range logs {
		if strings.Contains(log, "GO2TV_MANAGED") || strings.Contains(log, "GO2TV_PARENT") {
			t.Fatalf("marker leaked into log line %q", log)
		}
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	request, err := http.NewRequest(http.MethodGet, strings.TrimSuffix(frame.URL, "/")+"/api/bootstrap", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("bootstrap status=%d err=%v", response.StatusCode, err)
	}
	var payload struct {
		ManagedByGUI bool            `json:"managed_by_gui"`
		Snapshot     json.RawMessage `json:"snapshot"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.ManagedByGUI {
		t.Fatalf("managed_by_gui = false; body=%s", body)
	}
	if bytes.Contains(body, []byte("192.168.1.20")) || bytes.Contains(body, []byte("device.xml")) {
		t.Fatalf("endpoint leaked into bootstrap: %s", body)
	}

	// EOF on stdin shuts the child down cleanly.
	h.stdinWrite.Close()
	if err, exited := h.wait(10 * time.Second); !exited {
		t.Fatal("child did not shut down on stdin EOF")
	} else if err != nil {
		t.Fatalf("managed run error after EOF: %v", err)
	}
}

func TestManagedChildNoReadinessWithoutInitialSnapshot(t *testing.T) {
	originalTimeout := managedHandshakeTimeout
	managedHandshakeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { managedHandshakeTimeout = originalTimeout })

	h := startManagedChild(t, validManagedConfig(t))
	if err, exited := h.wait(10 * time.Second); !exited {
		t.Fatal("managed run did not fail on handshake timeout")
	} else if err == nil {
		t.Fatal("managed run succeeded without initial snapshot")
	}
	for line := range h.lines {
		if _, isFrame, _ := managedsession.DecodeManagedLine([]byte(line)); isFrame {
			t.Fatalf("readiness emitted without handshake: %q", line)
		}
	}
}

func TestManagedChildReportsBindConflict(t *testing.T) {
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()

	cfg := validManagedConfig(t)
	cfg.Listen = blocker.Addr().String()
	h := startManagedChild(t, cfg)
	h.sendFrame(managedsession.ParentFrame{Type: managedsession.TypeDiscoverySnapshot, Revision: 1})
	if err, exited := h.wait(10 * time.Second); !exited {
		t.Fatal("managed run did not fail on bind conflict")
	} else if err == nil {
		t.Fatal("managed run succeeded despite bind conflict")
	}
	frame, _ := h.awaitManagedFrame(10 * time.Second)
	if frame.Type != managedsession.TypeStartupError || frame.ErrorCode != managedsession.StartupErrorAddressInUse {
		t.Fatalf("frame = %+v, want address-in-use startup error", frame)
	}
}

func TestManagedChildEmptyInitialSnapshotIsValid(t *testing.T) {
	h := startManagedChild(t, validManagedConfig(t))
	h.sendFrame(managedsession.ParentFrame{Type: managedsession.TypeDiscoverySnapshot, Revision: 1})
	frame, _ := h.awaitManagedFrame(10 * time.Second)
	if frame.Type != managedsession.TypeReady {
		t.Fatalf("frame = %+v, want ready", frame)
	}
}

func TestManagedChildMalformedFrameShutsDown(t *testing.T) {
	h := startManagedChild(t, validManagedConfig(t))
	h.sendFrame(managedsession.ParentFrame{Type: managedsession.TypeDiscoverySnapshot, Revision: 1})
	frame, _ := h.awaitManagedFrame(10 * time.Second)
	if frame.Type != managedsession.TypeReady {
		t.Fatalf("frame = %+v, want ready", frame)
	}
	if _, err := io.WriteString(h.stdinWrite, "GO2TV_PARENT {\"protocol_version\":9}\n"); err != nil {
		t.Fatal(err)
	}
	if err, exited := h.wait(10 * time.Second); !exited {
		t.Fatal("child did not shut down on malformed frame")
	} else if err != nil {
		t.Fatalf("managed run error = %v, want clean shutdown", err)
	}
}

func TestManagedFrameWriterSerializedWithLogs(t *testing.T) {
	t.Parallel()
	var buf lockedBuffer
	log := newServerLogger(&buf, false)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				line, err := managedsession.EncodeManagedFrame(managedsession.ManagedFrame{Type: managedsession.TypeDiscoveryRefresh, RequestID: "r"})
				if err != nil {
					t.Error(err)
					return
				}
				_ = log.writeRawLine(line)
			} else {
				log.Info("GO2TV_MANAGED should never start a log line")
			}
		}(i)
	}
	wg.Wait()
	scanner := bufio.NewScanner(strings.NewReader(buf.String()))
	for scanner.Scan() {
		line := scanner.Text()
		frame, isFrame, err := managedsession.DecodeManagedLine([]byte(line))
		if isFrame {
			if err != nil || frame.RequestID != "r" {
				t.Fatalf("interleaved frame %q err=%v", line, err)
			}
			continue
		}
		if !strings.HasPrefix(line, "[") {
			t.Fatalf("log line without timestamp prefix: %q", line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}
