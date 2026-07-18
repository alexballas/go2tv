//go:build !(android || ios)

package servermode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"go2tv.app/go2tv/v2/internal/managedsession"
)

// managedHandshakeTimeout bounds the wait for the parent's initial discovery
// snapshot. The parent sends it unconditionally right after spawn, so this
// only fires when the parent is gone or broken. Variable for tests.
var managedHandshakeTimeout = 15 * time.Second

var (
	errManagedHandshakeTimeout = errors.New("managed handshake: no initial discovery snapshot")
	errManagedControlEOF       = errors.New("managed control stream closed")
)

type parentFrameEvent struct {
	frame managedsession.ParentFrame
	err   error
}

// runManaged runs -server as a GUI-managed child: discovery snapshots arrive
// on control (stdin in production), managed event frames leave through the
// logger's mutex-shared raw writer, and control EOF cancels the run.
func runManaged(ctx context.Context, validated Config, output io.Writer, control io.Reader) error {
	log := newServerLogger(output, validated.Debug)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	events := make(chan parentFrameEvent)
	go decodeParentFrames(runCtx, control, events)

	initial, err := awaitInitialSnapshot(runCtx, events)
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", validated.Listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()

	discovery := newManagedDiscovery(func(requestID string) error {
		line, err := managedsession.EncodeManagedFrame(managedsession.ManagedFrame{Type: managedsession.TypeDiscoveryRefresh, RequestID: requestID})
		if err != nil {
			return err
		}
		return log.writeRawLine(line)
	})
	defer discovery.Close()
	discovery.ApplySnapshot(initial.Revision, initial.Devices)

	securityConfig := validated
	securityConfig.Listen = listener.Addr().String()
	runtime, err := newRuntime(validated, log, discovery)
	if err != nil {
		return err
	}
	defer runtime.Close()
	handler, err := NewHandler(securityConfig, nil, runtime.web)
	if err != nil {
		return err
	}
	logStartup(log, validated, listener.Addr().String())
	log.Info("Managed by GUI: discovery arrives from the desktop app")

	server := &http.Server{Handler: accessLog(log, handler), ReadHeaderTimeout: defaultReadHeaderTimeout, ReadTimeout: defaultJSONTimeout, WriteTimeout: defaultJSONTimeout, IdleTimeout: defaultIdleTimeout, MaxHeaderBytes: defaultMaxHeaderBytes}
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()

	ready, err := managedsession.EncodeManagedFrame(managedsession.ManagedFrame{Type: managedsession.TypeReady, URL: managedURL(validated, listener.Addr().String())})
	if err == nil {
		err = log.writeRawLine(ready)
	}
	if err != nil {
		shutdownManagedServer(server, result)
		return fmt.Errorf("managed readiness: %w", err)
	}

	go dispatchParentFrames(runCtx, cancel, events, discovery, log)

	select {
	case <-runCtx.Done():
		err = shutdownManagedServer(server, result)
	case err = <-result:
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func shutdownManagedServer(server *http.Server, result chan error) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultJSONTimeout)
	_ = server.Shutdown(shutdownCtx)
	cancel()
	return <-result
}

// decodeParentFrames turns control lines into validated parent frames. Any
// read or protocol failure terminates the stream: the parent is trusted code,
// so a malformed frame means the channel itself is broken.
func decodeParentFrames(ctx context.Context, control io.Reader, events chan<- parentFrameEvent) {
	reader := managedsession.NewLineReader(control)
	for {
		line, err := reader.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = errManagedControlEOF
			}
			select {
			case events <- parentFrameEvent{err: err}:
			case <-ctx.Done():
			}
			return
		}
		if len(line) == 0 {
			continue
		}
		frame, err := managedsession.DecodeParentFrame(line)
		if err != nil {
			select {
			case events <- parentFrameEvent{err: err}:
			case <-ctx.Done():
			}
			return
		}
		select {
		case events <- parentFrameEvent{frame: frame}:
		case <-ctx.Done():
			return
		}
	}
}

func awaitInitialSnapshot(ctx context.Context, events <-chan parentFrameEvent) (managedsession.ParentFrame, error) {
	timeout := time.NewTimer(managedHandshakeTimeout)
	defer timeout.Stop()
	select {
	case event := <-events:
		if event.err != nil {
			return managedsession.ParentFrame{}, fmt.Errorf("managed handshake: %w", event.err)
		}
		if event.frame.Type != managedsession.TypeDiscoverySnapshot {
			return managedsession.ParentFrame{}, fmt.Errorf("managed handshake: first frame %q", event.frame.Type)
		}
		return event.frame, nil
	case <-timeout.C:
		return managedsession.ParentFrame{}, errManagedHandshakeTimeout
	case <-ctx.Done():
		return managedsession.ParentFrame{}, ctx.Err()
	}
}

func dispatchParentFrames(ctx context.Context, cancel context.CancelFunc, events <-chan parentFrameEvent, discovery *managedDiscovery, log *serverLogger) {
	for {
		select {
		case event := <-events:
			if event.err != nil {
				if errors.Is(event.err, errManagedControlEOF) {
					log.Info("Managed control stream closed; shutting down")
				} else {
					log.Error("Managed control stream failed; shutting down")
				}
				cancel()
				return
			}
			switch event.frame.Type {
			case managedsession.TypeDiscoverySnapshot:
				discovery.ApplySnapshot(event.frame.Revision, event.frame.Devices)
			case managedsession.TypeDiscoveryRefreshResult:
				discovery.ApplyRefreshResult(event.frame.RequestID, event.frame.Revision, event.frame.Devices, event.frame.ErrorCode)
			}
		case <-ctx.Done():
			return
		}
	}
}

func managedURL(cfg Config, actualListen string) string {
	if len(cfg.AllowedOrigins) != 0 {
		return cfg.AllowedOrigins[0] + "/"
	}
	host, port, _ := net.SplitHostPort(actualListen)
	return "http://" + net.JoinHostPort(host, port) + "/"
}
