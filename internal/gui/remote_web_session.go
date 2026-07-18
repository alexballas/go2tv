//go:build !(android || ios)

package gui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/internal/managedsession"
	"go2tv.app/go2tv/v2/internal/servermode"
)

type remoteSessionState string

const (
	remoteSessionStopped  remoteSessionState = "stopped"
	remoteSessionStarting remoteSessionState = "starting"
	remoteSessionRunning  remoteSessionState = "running"
	remoteSessionStopping remoteSessionState = "stopping"
	remoteSessionFailed   remoteSessionState = "failed"
)

// Sanitized failure codes shown to the user; never raw paths/network data.
const (
	remoteFailureSpawn              = "spawn_failed"
	remoteFailureContain            = "containment_failed"
	remoteFailureReadiness          = "no_readiness"
	remoteFailureExited             = "exited_unexpectedly"
	remoteFailureForcedStop         = "stopped_forcefully"
	remoteFailureProtocol           = "protocol_error"
	remoteFailureAddressInUse       = "address_in_use"
	remoteFailureAddressUnavailable = "address_unavailable"
	remoteFailurePermissionDenied   = "permission_denied"
	remoteFailureListenFailed       = "listen_failed"
)

const (
	remoteLogCapacity   = 200
	remoteLogLineMax    = 400
	remoteRefreshWindow = 12 * time.Second
)

// Variables so lifecycle tests can shrink the waits.
var (
	remoteReadinessWindow = 30 * time.Second
	remoteStopGrace       = 5 * time.Second
)

var (
	errRemoteSessionBusy       = errors.New("remote session already active")
	errRemoteSessionShutdown   = errors.New("remote session manager shut down")
	errRemoteSessionNotRunning = errors.New("no remote session running")
	errRemoteStoppedBeforeUp   = errors.New("remote session stopped before readiness")
	errRemoteFailureReported   = errors.New("remote session failure reported")
)

func reportedRemoteFailure(code string) error {
	return fmt.Errorf("%w: %s", errRemoteFailureReported, code)
}

type remoteSessionConfig struct {
	MediaRoots     []string
	Listen         string
	AllowedOrigins []string
	Debug          bool
	Version        string
}

type remoteSessionSnapshot struct {
	State     remoteSessionState
	URL       string
	LastError string
	Logs      []string
}

// managedProcess abstracts the spawned child for tests.
type managedProcess interface {
	Start() error
	// Contain must be called immediately after Start and before any handshake
	// traffic; on Windows it assigns the Job Object.
	Contain() error
	Stdin() io.WriteCloser
	Stdout() io.Reader
	Stderr() io.Reader
	Wait() error
	KillTree() error
	Release() error
}

type managedProcessFactory func(binary string, args []string) (managedProcess, error)

// remoteDiscoveryFeed abstracts the devices package feed for tests.
type remoteDiscoveryFeed struct {
	snapshot  func() []devices.Device
	subscribe func(int) (<-chan []devices.Device, func())
	refresh   func(context.Context) error
}

func defaultRemoteDiscoveryFeed() remoteDiscoveryFeed {
	return remoteDiscoveryFeed{
		snapshot:  devices.SnapshotAllDevices,
		subscribe: devices.SubscribeDiscovery,
		refresh:   devices.RefreshDiscovery,
	}
}

// remoteSessionRun holds one child lifecycle; a fresh value per Start keeps
// stale goroutines of finished runs from touching current state.
type remoteSessionRun struct {
	generation uint64
	process    managedProcess
	stdin      io.WriteCloser
	logs       *debugWriter

	writerMu sync.Mutex
	revision uint64

	releaseLease  func()
	releaseOnce   sync.Once
	stdinOnce     sync.Once
	stopRequested bool
	forced        atomic.Bool

	ready  chan string
	fatal  chan string
	exited chan struct{}

	cancelFeed func()
	feedDone   chan struct{}
}

func (r *remoteSessionRun) release() {
	r.releaseOnce.Do(func() {
		if r.releaseLease != nil {
			r.releaseLease()
		}
	})
}

func (r *remoteSessionRun) closeStdin() {
	r.stdinOnce.Do(func() {
		if r.stdin != nil {
			_ = r.stdin.Close()
		}
	})
}

type remoteSessionManager struct {
	mu          sync.Mutex
	state       remoteSessionState
	generation  uint64
	url         string
	lastError   string
	logs        *debugWriter
	run         *remoteSessionRun
	subscribers map[uint64]chan remoteSessionSnapshot
	nextSub     uint64
	shutdown    bool

	factory    managedProcessFactory
	feed       remoteDiscoveryFeed
	executable func() (string, error)
}

func newRemoteSessionManager() *remoteSessionManager {
	return &remoteSessionManager{
		state:       remoteSessionStopped,
		logs:        newDebugWriter(remoteLogCapacity),
		subscribers: make(map[uint64]chan remoteSessionSnapshot),
		factory:     newExecManagedProcess,
		feed:        defaultRemoteDiscoveryFeed(),
		executable:  os.Executable,
	}
}

func (m *remoteSessionManager) Snapshot() remoteSessionSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked()
}

func (m *remoteSessionManager) clearError() {
	m.mu.Lock()
	if m.lastError == "" && m.state != remoteSessionFailed {
		m.mu.Unlock()
		return
	}
	m.lastError = ""
	if m.state == remoteSessionFailed {
		m.state = remoteSessionStopped
	}
	m.publishLocked()
	m.mu.Unlock()
}

func (m *remoteSessionManager) snapshotLocked() remoteSessionSnapshot {
	logs := debugLogEntries(m.logs)
	for i := range logs {
		logs[i] = strings.TrimRight(logs[i], "\r\n")
	}
	return remoteSessionSnapshot{State: m.state, URL: m.url, LastError: m.lastError, Logs: logs}
}

// Subscribe delivers the current snapshot and then latest-only updates.
func (m *remoteSessionManager) Subscribe() (<-chan remoteSessionSnapshot, func()) {
	m.mu.Lock()
	m.nextSub++
	id := m.nextSub
	ch := make(chan remoteSessionSnapshot, 1)
	m.subscribers[id] = ch
	ch <- m.snapshotLocked()
	m.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			m.mu.Lock()
			delete(m.subscribers, id)
			close(ch)
			m.mu.Unlock()
		})
	}
}

func (m *remoteSessionManager) publishLocked() {
	snapshot := m.snapshotLocked()
	for _, subscriber := range m.subscribers {
		select {
		case subscriber <- snapshot:
		default:
			select {
			case <-subscriber:
			default:
			}
			select {
			case subscriber <- snapshot:
			default:
			}
		}
	}
}

func (m *remoteSessionManager) setStateLocked(state remoteSessionState) {
	m.state = state
	m.publishLocked()
}

func (m *remoteSessionManager) appendLog(run *remoteSessionRun, line string) {
	line = strings.TrimRight(line, "\r")
	if line == "" || run == nil || run.logs == nil {
		return
	}
	if len(line) > remoteLogLineMax {
		line = line[:remoteLogLineMax]
	}
	_, _ = run.logs.Write([]byte(line + "\n"))
}

// managedChildArgs builds the child argv; roots and origins stay separate
// arguments and no shell is ever involved.
func managedChildArgs(cfg remoteSessionConfig) []string {
	args := []string{"-server", "-managed-child", "-listen", cfg.Listen}
	for _, root := range cfg.MediaRoots {
		args = append(args, "-media-root", root)
	}
	for _, origin := range cfg.AllowedOrigins {
		args = append(args, "-allowed-origin", origin)
	}
	if cfg.Debug {
		args = append(args, "-debug")
	}
	return args
}

// Start validates config, spawns the managed child, bridges discovery, and
// blocks (off the UI goroutine) until readiness or failure. releaseLease is
// called exactly once on every terminal path of the run.
func (m *remoteSessionManager) Start(ctx context.Context, cfg remoteSessionConfig, releaseLease func()) error {
	if ctx == nil {
		ctx = context.Background()
	}
	// Reuse servermode validation; do not duplicate root/origin/listen rules.
	if _, err := servermode.Validate(servermode.Config{Listen: cfg.Listen, MediaRoots: cfg.MediaRoots, AllowedOrigins: cfg.AllowedOrigins, Version: cfg.Version, Debug: cfg.Debug}); err != nil {
		return err
	}
	binary, err := m.executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	m.mu.Lock()
	if m.shutdown {
		m.mu.Unlock()
		return errRemoteSessionShutdown
	}
	if m.run != nil || (m.state != remoteSessionStopped && m.state != remoteSessionFailed) {
		m.mu.Unlock()
		return errRemoteSessionBusy
	}
	m.generation++
	run := &remoteSessionRun{
		generation:   m.generation,
		releaseLease: releaseLease,
		logs:         newDebugWriter(remoteLogCapacity),
		ready:        make(chan string, 1),
		fatal:        make(chan string, 4),
		exited:       make(chan struct{}),
		feedDone:     make(chan struct{}),
	}
	m.run = run
	m.url = ""
	m.lastError = ""
	m.logs = run.logs
	m.setStateLocked(remoteSessionStarting)
	m.mu.Unlock()

	if err := m.spawnAndBridge(run, binary, cfg); err != nil {
		m.forceStopRun(run)
		<-run.exited
		code := sanitizedFailure(err)
		m.finishRun(run, remoteSessionFailed, code)
		return reportedRemoteFailure(code)
	}

	select {
	case url := <-run.ready:
		m.mu.Lock()
		if m.run == run {
			m.url = url
			m.setStateLocked(remoteSessionRunning)
		}
		m.mu.Unlock()
		go m.superviseRun(run)
		return nil
	case code := <-run.fatal:
		m.forceStopRun(run)
		<-run.exited
		m.finishRun(run, remoteSessionFailed, code)
		return reportedRemoteFailure(code)
	case <-run.exited:
		if run.stopRequested {
			m.finishRun(run, remoteSessionStopped, "")
			return errRemoteStoppedBeforeUp
		}
		select {
		case code := <-run.fatal:
			m.finishRun(run, remoteSessionFailed, code)
			return reportedRemoteFailure(code)
		default:
		}
		m.finishRun(run, remoteSessionFailed, remoteFailureExited)
		return reportedRemoteFailure(remoteFailureExited)
	case <-time.After(remoteReadinessWindow):
		m.forceStopRun(run)
		<-run.exited
		m.finishRun(run, remoteSessionFailed, remoteFailureReadiness)
		return reportedRemoteFailure(remoteFailureReadiness)
	case <-ctx.Done():
		m.forceStopRun(run)
		<-run.exited
		m.finishRun(run, remoteSessionFailed, remoteFailureReadiness)
		return reportedRemoteFailure(remoteFailureReadiness)
	}
}

// spawnAndBridge starts the child process, containment, pipe drains, the
// unconditional initial discovery snapshot, and the discovery subscription.
func (m *remoteSessionManager) spawnAndBridge(run *remoteSessionRun, binary string, cfg remoteSessionConfig) error {
	process, err := m.factory(binary, managedChildArgs(cfg))
	if err != nil {
		close(run.exited)
		close(run.feedDone)
		return fmt.Errorf("%s: %w", remoteFailureSpawn, err)
	}
	run.process = process
	if err := process.Start(); err != nil {
		close(run.exited)
		close(run.feedDone)
		return fmt.Errorf("%s: %w", remoteFailureSpawn, err)
	}
	if err := process.Contain(); err != nil {
		_ = process.KillTree()
		_ = process.Wait()
		_ = process.Release()
		close(run.exited)
		close(run.feedDone)
		return fmt.Errorf("%s: %w", remoteFailureContain, err)
	}
	run.stdin = process.Stdin()

	var drains sync.WaitGroup
	drains.Add(2)
	go func() {
		defer drains.Done()
		m.drainChildStdout(run)
	}()
	go func() {
		defer drains.Done()
		m.drainChildStderr(run)
	}()
	go func() {
		drains.Wait()
		_ = process.Wait()
		_ = process.Release()
		close(run.exited)
	}()

	// The initial snapshot goes out unconditionally right after containment;
	// an empty frame is the normal cold-cache case. Never gated on a scan.
	if err := m.writeSnapshotFrame(run, m.feed.snapshot()); err != nil {
		return fmt.Errorf("%s: %w", remoteFailureProtocol, err)
	}

	updates, cancelFeed := m.feed.subscribe(4)
	run.cancelFeed = cancelFeed
	go func() {
		defer close(run.feedDone)
		for snapshot := range updates {
			if err := m.writeSnapshotFrame(run, snapshot); err != nil {
				return
			}
		}
	}()
	return nil
}

// superviseRun watches a running child for unexpected exit or protocol
// failure and finalizes state exactly once.
func (m *remoteSessionManager) superviseRun(run *remoteSessionRun) {
	select {
	case <-run.exited:
		if run.stopRequested {
			m.finishRun(run, remoteSessionStopped, run.stopFailure())
			return
		}
		m.finishRun(run, remoteSessionFailed, remoteFailureExited)
	case code := <-run.fatal:
		m.forceStopRun(run)
		<-run.exited
		if run.stopRequested {
			m.finishRun(run, remoteSessionStopped, run.stopFailure())
			return
		}
		m.finishRun(run, remoteSessionFailed, code)
	}
}

// stopFailure reports the sanitized status for a requested stop: empty for a
// graceful EOF shutdown, forced-stop when the tree had to be killed.
func (r *remoteSessionRun) stopFailure() string {
	if r.forced.Load() {
		return remoteFailureForcedStop
	}
	return ""
}

// finishRun tears down bridge resources, releases the lease exactly once, and
// records the terminal state unless a newer run already took over.
func (m *remoteSessionManager) finishRun(run *remoteSessionRun, state remoteSessionState, failure string) {
	if run.cancelFeed != nil {
		run.cancelFeed()
	}
	<-run.feedDone
	run.closeStdin()

	m.mu.Lock()
	if m.run == run {
		m.run = nil
		if failure != "" {
			m.lastError = failure
		}
		if state != remoteSessionRunning {
			m.url = ""
		}
		m.setStateLocked(state)
	}
	m.mu.Unlock()
	run.release()
}

func (m *remoteSessionManager) forceStopRun(run *remoteSessionRun) {
	run.closeStdin()
	if run.process != nil {
		_ = run.process.KillTree()
	}
}

// Stop performs the graceful stdin-EOF shutdown, escalating to a tree kill
// when the child does not exit within the grace period or ctx.
func (m *remoteSessionManager) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	run := m.run
	if run == nil {
		m.mu.Unlock()
		return nil
	}
	run.stopRequested = true
	m.setStateLocked(remoteSessionStopping)
	m.mu.Unlock()

	if run.cancelFeed != nil {
		run.cancelFeed()
	}
	run.closeStdin()

	grace := time.NewTimer(remoteStopGrace)
	defer grace.Stop()
	forced := false
	select {
	case <-run.exited:
	case <-grace.C:
		forced = true
	case <-ctx.Done():
		forced = true
	}
	if forced {
		run.forced.Store(true)
		if run.process != nil {
			_ = run.process.KillTree()
		}
		<-run.exited
	}
	// Wait for the supervisor (or starting Start call) to finalize the run.
	m.awaitRunFinalized(run)
	return nil
}

func (m *remoteSessionManager) awaitRunFinalized(run *remoteSessionRun) {
	for {
		m.mu.Lock()
		current := m.run
		m.mu.Unlock()
		if current != run {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Shutdown is the one-shot final stop; afterwards Start is rejected forever.
func (m *remoteSessionManager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.shutdown = true
	m.mu.Unlock()
	return m.Stop(ctx)
}

// drainChildStdout parses managed frames at line start only; everything else
// is a human log line and managed frames never reach the visible logs.
func (m *remoteSessionManager) drainChildStdout(run *remoteSessionRun) {
	reader := managedsession.NewLineReader(run.process.Stdout())
	for {
		line, err := reader.ReadLine()
		if err != nil {
			return
		}
		frame, isFrame, err := managedsession.DecodeManagedLine(line)
		if err != nil {
			select {
			case run.fatal <- remoteFailureProtocol:
			default:
			}
			return
		}
		if !isFrame {
			m.appendLog(run, string(line))
			continue
		}
		switch frame.Type {
		case managedsession.TypeReady:
			select {
			case run.ready <- frame.URL:
			default:
			}
		case managedsession.TypeStartupError:
			select {
			case run.fatal <- remoteStartupFailure(frame.ErrorCode):
			default:
			}
			return
		case managedsession.TypeDiscoveryRefresh:
			go m.handleRefreshRequest(run, frame.RequestID)
		}
	}
}

func remoteStartupFailure(code string) string {
	switch code {
	case managedsession.StartupErrorAddressInUse:
		return remoteFailureAddressInUse
	case managedsession.StartupErrorAddressUnavailable:
		return remoteFailureAddressUnavailable
	case managedsession.StartupErrorPermissionDenied:
		return remoteFailurePermissionDenied
	default:
		return remoteFailureListenFailed
	}
}

func (m *remoteSessionManager) drainChildStderr(run *remoteSessionRun) {
	reader := managedsession.NewLineReader(run.process.Stderr())
	for {
		line, err := reader.ReadLine()
		if err != nil {
			return
		}
		m.appendLog(run, string(line))
	}
}

// handleRefreshRequest runs one coalesced authoritative refresh per received
// request_id and always answers with exactly one refresh_result.
func (m *remoteSessionManager) handleRefreshRequest(run *remoteSessionRun, requestID string) {
	ctx, cancel := context.WithTimeout(context.Background(), remoteRefreshWindow)
	err := m.feed.refresh(ctx)
	cancel()
	errorCode := ""
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, devices.ErrDiscoveryRefreshTimeout) {
			errorCode = managedsession.RefreshErrorTimeout
		} else {
			errorCode = managedsession.RefreshErrorUnavailable
		}
	}
	_ = m.writeFrame(run, managedsession.ParentFrame{
		Type:      managedsession.TypeDiscoveryRefreshResult,
		RequestID: requestID,
		Devices:   wireDevices(m.feed.snapshot()),
		ErrorCode: errorCode,
	})
}

func (m *remoteSessionManager) writeSnapshotFrame(run *remoteSessionRun, snapshot []devices.Device) error {
	return m.writeFrame(run, managedsession.ParentFrame{Type: managedsession.TypeDiscoverySnapshot, Devices: wireDevices(snapshot)})
}

// writeFrame serializes stdin writes and stamps the shared per-run revision
// counter so pushes and refresh results stay monotonic in write order.
func (m *remoteSessionManager) writeFrame(run *remoteSessionRun, frame managedsession.ParentFrame) error {
	run.writerMu.Lock()
	defer run.writerMu.Unlock()
	if run.stdin == nil {
		return errRemoteSessionNotRunning
	}
	run.revision++
	frame.Revision = run.revision
	line, err := managedsession.EncodeParentFrame(frame)
	if err != nil {
		return err
	}
	_, err = run.stdin.Write(line)
	return err
}

// wireDevices maps GUI cache devices onto the bounded wire schema, dropping
// anything that fails validation instead of poisoning the whole frame.
func wireDevices(snapshot []devices.Device) []managedsession.Device {
	result := make([]managedsession.Device, 0, min(len(snapshot), managedsession.MaxDevices))
	seen := make(map[string]struct{}, len(snapshot))
	for _, device := range snapshot {
		if len(result) == managedsession.MaxDevices {
			break
		}
		wire := managedsession.Device{Name: device.Name, Protocol: device.Type, Endpoint: device.Addr, AudioOnly: device.IsAudioOnly}
		key := wire.Protocol + "\x00" + wire.Endpoint
		if _, dup := seen[key]; dup {
			continue
		}
		if managedsession.ValidateDevices([]managedsession.Device{wire}) != nil {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, wire)
	}
	return result
}

// sanitizedFailure keeps user-visible errors free of paths and raw output.
func sanitizedFailure(err error) string {
	for _, code := range []string{remoteFailureSpawn, remoteFailureContain, remoteFailureProtocol, remoteFailureReadiness, remoteFailureExited} {
		if strings.Contains(err.Error(), code) {
			return code
		}
	}
	return remoteFailureSpawn
}

// execManagedProcess is the production child implementation over exec.Cmd
// anonymous pipes; no sockets, files, shells, or extra inherited FDs.
type execManagedProcess struct {
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      io.ReadCloser
	stderr      io.ReadCloser
	containment *managedContainment
}

func newExecManagedProcess(binary string, args []string) (managedProcess, error) {
	cmd := exec.Command(binary, args...)
	configureManagedSysProcAttr(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	return &execManagedProcess{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr}, nil
}

func (p *execManagedProcess) Start() error { return p.cmd.Start() }

func (p *execManagedProcess) Contain() error {
	containment, err := containManagedProcess(p.cmd)
	if err != nil {
		return err
	}
	p.containment = containment
	return nil
}

func (p *execManagedProcess) Stdin() io.WriteCloser { return p.stdin }
func (p *execManagedProcess) Stdout() io.Reader     { return p.stdout }
func (p *execManagedProcess) Stderr() io.Reader     { return p.stderr }
func (p *execManagedProcess) Wait() error           { return p.cmd.Wait() }

func (p *execManagedProcess) KillTree() error {
	if p.containment != nil {
		return p.containment.KillTree()
	}
	if p.cmd.Process != nil {
		return p.cmd.Process.Kill()
	}
	return nil
}

func (p *execManagedProcess) Release() error {
	if p.containment != nil {
		return p.containment.Close()
	}
	return nil
}
