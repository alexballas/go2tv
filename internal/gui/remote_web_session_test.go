//go:build !(android || ios)

package gui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/internal/managedsession"
)

// fakeManagedProcess simulates a spawned child over in-memory pipes. Tests
// script the child side through parentFrames/writeManaged/exit.
type fakeManagedProcess struct {
	args []string

	stdinManager io.WriteCloser
	stdinChild   *io.PipeReader

	stdoutManager *io.PipeReader
	stdoutChild   *io.PipeWriter

	stderrManager *io.PipeReader
	stderrChild   *io.PipeWriter

	started   atomic.Bool
	contained atomic.Bool
	killed    atomic.Int32
	released  atomic.Bool

	exitOnce sync.Once
	exitCh   chan struct{}

	parentFrames chan managedsession.ParentFrame
	parentErr    chan error
}

func newFakeManagedProcess(args []string) *fakeManagedProcess {
	stdinChild, stdinManager := io.Pipe()
	stdoutManager, stdoutChild := io.Pipe()
	stderrManager, stderrChild := io.Pipe()
	p := &fakeManagedProcess{
		args:          args,
		stdinManager:  stdinManager,
		stdinChild:    stdinChild,
		stdoutManager: stdoutManager,
		stdoutChild:   stdoutChild,
		stderrManager: stderrManager,
		stderrChild:   stderrChild,
		exitCh:        make(chan struct{}),
		parentFrames:  make(chan managedsession.ParentFrame, 64),
		parentErr:     make(chan error, 1),
	}
	go func() {
		reader := managedsession.NewLineReader(stdinChild)
		for {
			line, err := reader.ReadLine()
			if err != nil {
				p.parentErr <- err
				close(p.parentFrames)
				return
			}
			frame, err := managedsession.DecodeParentFrame(line)
			if err != nil {
				p.parentErr <- err
				close(p.parentFrames)
				return
			}
			p.parentFrames <- frame
		}
	}()
	return p
}

func (p *fakeManagedProcess) Start() error   { p.started.Store(true); return nil }
func (p *fakeManagedProcess) Contain() error { p.contained.Store(true); return nil }

func (p *fakeManagedProcess) Stdin() io.WriteCloser { return p.stdinManager }
func (p *fakeManagedProcess) Stdout() io.Reader     { return p.stdoutManager }
func (p *fakeManagedProcess) Stderr() io.Reader     { return p.stderrManager }

func (p *fakeManagedProcess) Wait() error {
	<-p.exitCh
	return nil
}

func (p *fakeManagedProcess) KillTree() error {
	p.killed.Add(1)
	p.exit()
	return nil
}

func (p *fakeManagedProcess) Release() error { p.released.Store(true); return nil }

// exit simulates child process termination: output pipes close (drains EOF)
// and Wait unblocks.
func (p *fakeManagedProcess) exit() {
	p.exitOnce.Do(func() {
		p.stdoutChild.Close()
		p.stderrChild.Close()
		p.stdinChild.Close()
		close(p.exitCh)
	})
}

func (p *fakeManagedProcess) writeManaged(t *testing.T, frame managedsession.ManagedFrame) {
	t.Helper()
	line, err := managedsession.EncodeManagedFrame(frame)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.stdoutChild.Write(line); err != nil {
		t.Fatalf("child stdout write: %v", err)
	}
}

func (p *fakeManagedProcess) writeLog(t *testing.T, line string) {
	t.Helper()
	if _, err := p.stdoutChild.Write([]byte(line + "\n")); err != nil {
		t.Fatalf("child log write: %v", err)
	}
}

func (p *fakeManagedProcess) awaitFrame(t *testing.T, timeout time.Duration) managedsession.ParentFrame {
	t.Helper()
	select {
	case frame, ok := <-p.parentFrames:
		if !ok {
			t.Fatal("parent stream closed while awaiting frame")
		}
		return frame
	case <-time.After(timeout):
		t.Fatal("no parent frame received")
		return managedsession.ParentFrame{}
	}
}

// exitOnStdinEOF makes the fake child behave like the real one on graceful
// stop: parent closes stdin, child exits.
func (p *fakeManagedProcess) exitOnStdinEOF() {
	go func() {
		for range p.parentFrames {
		}
		p.exit()
	}()
}

type managerHarness struct {
	manager   *remoteSessionManager
	processes chan *fakeManagedProcess
	refreshes atomic.Int32
	devices   struct {
		mu   sync.Mutex
		list []devices.Device
	}
	feedCh     chan []devices.Device
	leaseCount atomic.Int32
}

func newManagerHarness(t *testing.T) *managerHarness {
	t.Helper()
	h := &managerHarness{processes: make(chan *fakeManagedProcess, 4), feedCh: make(chan []devices.Device, 16)}
	m := newRemoteSessionManager()
	m.executable = func() (string, error) { return "/fake/go2tv", nil }
	m.factory = func(binary string, args []string) (managedProcess, error) {
		p := newFakeManagedProcess(args)
		h.processes <- p
		return p, nil
	}
	m.feed = remoteDiscoveryFeed{
		snapshot: func() []devices.Device {
			h.devices.mu.Lock()
			defer h.devices.mu.Unlock()
			return slices.Clone(h.devices.list)
		},
		subscribe: func(int) (<-chan []devices.Device, func()) {
			ch := make(chan []devices.Device, 16)
			done := make(chan struct{})
			go func() {
				for {
					select {
					case snapshot := <-h.feedCh:
						select {
						case ch <- snapshot:
						case <-done:
							close(ch)
							return
						}
					case <-done:
						close(ch)
						return
					}
				}
			}()
			var once sync.Once
			return ch, func() { once.Do(func() { close(done) }) }
		},
		refresh: func(context.Context) error {
			h.refreshes.Add(1)
			return nil
		},
	}
	h.manager = m
	return h
}

func (h *managerHarness) lease() func() {
	return func() { h.leaseCount.Add(1) }
}

func validRemoteConfig(t *testing.T) remoteSessionConfig {
	t.Helper()
	return remoteSessionConfig{MediaRoots: []string{t.TempDir()}, Listen: "127.0.0.1:0", Version: "test"}
}

// startRunning drives a harness manager to Running and returns the fake child.
func (h *managerHarness) startRunning(t *testing.T) *fakeManagedProcess {
	t.Helper()
	started := make(chan error, 1)
	go func() { started <- h.manager.Start(context.Background(), validRemoteConfig(t), h.lease()) }()
	process := <-h.processes
	initial := process.awaitFrame(t, 5*time.Second)
	if initial.Type != managedsession.TypeDiscoverySnapshot {
		t.Fatalf("first frame = %+v, want snapshot", initial)
	}
	process.writeManaged(t, managedsession.ManagedFrame{Type: managedsession.TypeReady, URL: "http://127.0.0.1:12345/"})
	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("Start error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start never returned")
	}
	if snapshot := h.manager.Snapshot(); snapshot.State != remoteSessionRunning || snapshot.URL != "http://127.0.0.1:12345/" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	return process
}

func awaitState(t *testing.T, m *remoteSessionManager, want remoteSessionState) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m.Snapshot().State == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("state = %q, want %q", m.Snapshot().State, want)
}

func TestManagedChildArgs(t *testing.T) {
	t.Parallel()
	cfg := remoteSessionConfig{
		MediaRoots:     []string{"/media/a", "/media/b"},
		Listen:         "192.168.1.10:9666",
		AllowedOrigins: []string{"http://192.168.1.10:9666"},
		FFmpegPath:     "/tools/ffmpeg",
		Debug:          true,
	}
	got := managedChildArgs(cfg)
	want := []string{"-server", "-managed-child", "-listen", "192.168.1.10:9666", "-media-root", "/media/a", "-media-root", "/media/b", "-allowed-origin", "http://192.168.1.10:9666", "-ffmpeg", "/tools/ffmpeg", "-debug"}
	if !slices.Equal(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
	cfg.Debug = false
	if got := managedChildArgs(cfg); slices.Contains(got, "-debug") {
		t.Fatalf("args include -debug when disabled: %q", got)
	}
}

func TestRemoteSessionStartSendsInitialEmptySnapshotBeforeReady(t *testing.T) {
	t.Parallel()
	h := newManagerHarness(t)
	process := h.startRunning(t)
	process.exitOnStdinEOF()
	if err := h.manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	awaitState(t, h.manager, remoteSessionStopped)
	if h.leaseCount.Load() != 1 {
		t.Fatalf("lease releases = %d, want 1", h.leaseCount.Load())
	}
	if process.killed.Load() != 0 {
		t.Fatalf("graceful stop killed tree %d times", process.killed.Load())
	}
	if !process.released.Load() {
		t.Fatal("containment not released")
	}
}

func TestRemoteSessionPushesDiscoveryUpdatesWithMonotonicRevisions(t *testing.T) {
	t.Parallel()
	h := newManagerHarness(t)
	process := h.startRunning(t)
	defer process.exit()

	h.feedCh <- []devices.Device{{Name: "TV", Addr: "http://192.168.1.20:1400/x.xml", Type: "DLNA"}}
	update := process.awaitFrame(t, 5*time.Second)
	if update.Type != managedsession.TypeDiscoverySnapshot || len(update.Devices) != 1 || update.Devices[0].Name != "TV" {
		t.Fatalf("update = %+v", update)
	}
	if update.Revision < 2 {
		t.Fatalf("revision = %d, want > initial", update.Revision)
	}
	h.feedCh <- nil
	empty := process.awaitFrame(t, 5*time.Second)
	if len(empty.Devices) != 0 || empty.Revision <= update.Revision {
		t.Fatalf("empty update = %+v", empty)
	}
}

func TestRemoteSessionRefreshRoundTrip(t *testing.T) {
	t.Parallel()
	h := newManagerHarness(t)
	process := h.startRunning(t)
	defer process.exit()

	h.devices.mu.Lock()
	h.devices.list = []devices.Device{{Name: "TV", Addr: "http://192.168.1.20:1400/x.xml", Type: "DLNA"}}
	h.devices.mu.Unlock()

	process.writeManaged(t, managedsession.ManagedFrame{Type: managedsession.TypeDiscoveryRefresh, RequestID: "9"})
	result := process.awaitFrame(t, 5*time.Second)
	if result.Type != managedsession.TypeDiscoveryRefreshResult || result.RequestID != "9" || result.ErrorCode != "" {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Devices) != 1 || result.Devices[0].Name != "TV" {
		t.Fatalf("result devices = %+v", result.Devices)
	}
	if h.refreshes.Load() != 1 {
		t.Fatalf("authoritative refreshes = %d, want 1", h.refreshes.Load())
	}
}

func TestRemoteSessionUnexpectedExitFailsAndReleasesLease(t *testing.T) {
	t.Parallel()
	h := newManagerHarness(t)
	process := h.startRunning(t)
	process.exit()
	awaitState(t, h.manager, remoteSessionFailed)
	snapshot := h.manager.Snapshot()
	if snapshot.LastError != remoteFailureExited {
		t.Fatalf("last error = %q", snapshot.LastError)
	}
	if h.leaseCount.Load() != 1 {
		t.Fatalf("lease releases = %d, want 1", h.leaseCount.Load())
	}
}

func TestRemoteSessionReportsAddressInUse(t *testing.T) {
	t.Parallel()
	h := newManagerHarness(t)
	started := make(chan error, 1)
	go func() { started <- h.manager.Start(context.Background(), validRemoteConfig(t), h.lease()) }()
	process := <-h.processes
	process.awaitFrame(t, 5*time.Second)
	process.writeManaged(t, managedsession.ManagedFrame{Type: managedsession.TypeStartupError, ErrorCode: managedsession.StartupErrorAddressInUse})
	process.exit()
	if err := <-started; !errors.Is(err, errRemoteFailureReported) {
		t.Fatal("Start succeeded after bind conflict")
	}
	if got := h.manager.Snapshot().LastError; got != remoteFailureAddressInUse {
		t.Fatalf("last error = %q, want %q", got, remoteFailureAddressInUse)
	}
	if h.leaseCount.Load() != 1 {
		t.Fatalf("lease releases = %d, want 1", h.leaseCount.Load())
	}
}

func TestRemoteStartErrorDialogPolicy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "validation", err: errors.New("invalid configuration"), want: true},
		{name: "published failure", err: reportedRemoteFailure(remoteFailureAddressInUse)},
		{name: "stopped while starting", err: errRemoteStoppedBeforeUp},
		{name: "success"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldShowRemoteStartError(tt.err); got != tt.want {
				t.Fatalf("shouldShowRemoteStartError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRemoteSessionReadinessTimeoutKillsChild(t *testing.T) {
	originalWindow := remoteReadinessWindow
	remoteReadinessWindow = 200 * time.Millisecond
	t.Cleanup(func() { remoteReadinessWindow = originalWindow })

	h := newManagerHarness(t)
	started := make(chan error, 1)
	go func() { started <- h.manager.Start(context.Background(), validRemoteConfig(t), h.lease()) }()
	process := <-h.processes
	process.awaitFrame(t, 5*time.Second)
	select {
	case err := <-started:
		if err == nil {
			t.Fatal("Start succeeded without readiness")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start never timed out")
	}
	if process.killed.Load() == 0 {
		t.Fatal("stuck child not killed")
	}
	awaitState(t, h.manager, remoteSessionFailed)
	if h.leaseCount.Load() != 1 {
		t.Fatalf("lease releases = %d, want 1", h.leaseCount.Load())
	}
}

func TestRemoteSessionMalformedReadinessFails(t *testing.T) {
	t.Parallel()
	h := newManagerHarness(t)
	started := make(chan error, 1)
	go func() { started <- h.manager.Start(context.Background(), validRemoteConfig(t), h.lease()) }()
	process := <-h.processes
	process.awaitFrame(t, 5*time.Second)
	if _, err := process.stdoutChild.Write([]byte("GO2TV_MANAGED {\"protocol_version\":1,\"type\":\"ready\"}\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-started:
		if err == nil {
			t.Fatal("Start accepted malformed readiness")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start never returned")
	}
	awaitState(t, h.manager, remoteSessionFailed)
	if got := h.manager.Snapshot().LastError; got != remoteFailureProtocol {
		t.Fatalf("last error = %q", got)
	}
}

func TestRemoteSessionForcedStopAfterGrace(t *testing.T) {
	originalGrace := remoteStopGrace
	remoteStopGrace = 100 * time.Millisecond
	t.Cleanup(func() { remoteStopGrace = originalGrace })

	h := newManagerHarness(t)
	process := h.startRunning(t)
	// Child ignores stdin EOF: forced kill path must engage.
	if err := h.manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	awaitState(t, h.manager, remoteSessionStopped)
	if process.killed.Load() == 0 {
		t.Fatal("hung child not force-killed")
	}
	if got := h.manager.Snapshot().LastError; got != remoteFailureForcedStop {
		t.Fatalf("last error = %q", got)
	}
	if h.leaseCount.Load() != 1 {
		t.Fatalf("lease releases = %d, want 1", h.leaseCount.Load())
	}
}

func TestRemoteSessionStartStopStartAndShutdown(t *testing.T) {
	t.Parallel()
	h := newManagerHarness(t)

	first := h.startRunning(t)
	first.exitOnStdinEOF()
	if err := h.manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	awaitState(t, h.manager, remoteSessionStopped)

	second := h.startRunning(t)
	second.exitOnStdinEOF()

	// Repeated Stop is safe.
	if err := h.manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := h.manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	awaitState(t, h.manager, remoteSessionStopped)
	if h.leaseCount.Load() != 2 {
		t.Fatalf("lease releases = %d, want 2", h.leaseCount.Load())
	}

	if err := h.manager.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := h.manager.Start(context.Background(), validRemoteConfig(t), h.lease()); !errors.Is(err, errRemoteSessionShutdown) {
		t.Fatalf("Start after Shutdown = %v, want shutdown error", err)
	}
}

func TestRemoteSessionSecondStartRejectedWhileActive(t *testing.T) {
	t.Parallel()
	h := newManagerHarness(t)
	process := h.startRunning(t)
	defer process.exit()
	if err := h.manager.Start(context.Background(), validRemoteConfig(t), h.lease()); !errors.Is(err, errRemoteSessionBusy) {
		t.Fatalf("second Start = %v, want busy", err)
	}
}

func TestRemoteSessionLogsBoundedAndFramesExcluded(t *testing.T) {
	t.Parallel()
	h := newManagerHarness(t)
	process := h.startRunning(t)
	defer process.exit()

	for i := range remoteLogCapacity + 50 {
		process.writeLog(t, fmt.Sprintf("[2026-07-15 10:00:00] INFO line-%03d", i))
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		logs := h.manager.Snapshot().Logs
		// The ring reaches capacity before the manager has consumed the final
		// line, so wait for line-249 rather than for size alone.
		if len(logs) == remoteLogCapacity && strings.HasSuffix(logs[len(logs)-1], "line-249") {
			for _, line := range logs {
				if strings.Contains(line, "GO2TV_") {
					t.Fatalf("frame leaked into logs: %q", line)
				}
			}
			if !strings.HasSuffix(logs[0], "line-050") || !strings.HasSuffix(logs[len(logs)-1], "line-249") {
				t.Fatalf("log ring bounds = %q ... %q", logs[0], logs[len(logs)-1])
			}
			var diagnostics bytes.Buffer
			if err := writeRemoteSessionDiagnostics(&diagnostics, "test", h.manager.Snapshot()); err != nil {
				t.Fatalf("writeRemoteSessionDiagnostics: %v", err)
			}
			output := diagnostics.String()
			if strings.Contains(output, "line-000") || !strings.Contains(output, "line-050") || !strings.Contains(output, "line-249") {
				t.Fatalf("exported log ring has wrong bounds:\n%s", output)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("log ring size = %d, want %d", len(logs), remoteLogCapacity)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRemoteSessionStaleRunCannotUpdateCurrentRun(t *testing.T) {
	t.Parallel()
	h := newManagerHarness(t)
	first := h.startRunning(t)
	first.exitOnStdinEOF()
	if err := h.manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	awaitState(t, h.manager, remoteSessionStopped)

	second := h.startRunning(t)
	defer second.exit()

	// A late frame surfacing from the dead first child must not disturb the
	// running second session.
	time.Sleep(50 * time.Millisecond)
	if snapshot := h.manager.Snapshot(); snapshot.State != remoteSessionRunning {
		t.Fatalf("state = %q, want running", snapshot.State)
	}
}

func TestWireDevicesFiltersAndBounds(t *testing.T) {
	t.Parallel()
	input := []devices.Device{
		{Name: "TV", Addr: "http://192.168.1.20:1400/x.xml", Type: "DLNA"},
		{Name: "TV", Addr: "http://192.168.1.20:1400/x.xml", Type: "DLNA"},
		{Name: "Bad", Addr: "not-a-url", Type: "DLNA"},
		{Name: "Cast", Addr: "http://192.168.1.30:8009", Type: "Chromecast", IsAudioOnly: true},
	}
	wire := wireDevices(input)
	if len(wire) != 2 {
		t.Fatalf("wire = %+v, want 2 devices", wire)
	}
	if wire[1].Protocol != "Chromecast" || !wire[1].AudioOnly {
		t.Fatalf("wire[1] = %+v", wire[1])
	}

	oversized := make([]devices.Device, managedsession.MaxDevices+20)
	for i := range oversized {
		oversized[i] = devices.Device{Name: "d", Addr: "http://192.0.2.1:1400/dev/" + strings.Repeat("a", i+1), Type: "DLNA"}
	}
	if got := len(wireDevices(oversized)); got != managedsession.MaxDevices {
		t.Fatalf("bounded wire devices = %d, want %d", got, managedsession.MaxDevices)
	}
}
