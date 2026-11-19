//go:build !(android || ios)

package servermode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"

	"go2tv.app/go2tv/v2/devices"
)

func Run(ctx context.Context, cfg Config, output io.Writer) error {
	validated, err := Validate(cfg)
	if err != nil {
		return err
	}
	if validated.ManagedChild {
		return runManaged(ctx, validated, output, os.Stdin)
	}
	listener, err := net.Listen("tcp", validated.Listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()
	log := newServerLogger(output, validated.Debug)
	if validated.Debug {
		devices.SetDiscoveryLogOutput(discoveryLogWriter{log: log})
		defer devices.SetDiscoveryLogOutput(nil)
	}
	if !validated.RelaxedDiscovery {
		// Count only: replies are attacker-controlled, so no remote text
		// reaches the normal-level log.
		devices.SetHiddenNoticeFunc(func(n int) {
			log.Info(fmt.Sprintf("Strict discovery ignored %d SSDP description URLs; see -relaxed-discovery.", n))
		})
		defer devices.SetHiddenNoticeFunc(nil)
	}
	securityConfig := validated
	securityConfig.Listen = listener.Addr().String()
	runtime, err := newRuntime(validated, log, nil)
	if err != nil {
		return err
	}
	defer runtime.Close()
	handler, err := NewHandler(securityConfig, nil, runtime.web)
	if err != nil {
		return err
	}
	logStartup(log, validated, listener.Addr().String())

	server := &http.Server{Handler: accessLog(log, handler), ReadHeaderTimeout: defaultReadHeaderTimeout, ReadTimeout: defaultJSONTimeout, WriteTimeout: defaultJSONTimeout, IdleTimeout: defaultIdleTimeout, MaxHeaderBytes: defaultMaxHeaderBytes}
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultJSONTimeout)
		_ = server.Shutdown(shutdownCtx)
		cancel()
		err = <-result
	case err = <-result:
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func logStartup(log *serverLogger, cfg Config, actualListen string) {
	log.Info("Web server listening: " + actualListen)
	if len(cfg.AllowedOrigins) != 0 {
		for _, origin := range cfg.AllowedOrigins {
			log.Info("Allowed URL: " + origin + "/")
		}
	} else {
		host, port, _ := net.SplitHostPort(actualListen)
		log.Info("Allowed URL: http://" + net.JoinHostPort(host, port) + "/")
	}
	log.Warning("Trusted-LAN mode; no TLS. Do not expose to untrusted networks.")
	for _, root := range cfg.MediaRoots {
		log.Info("Media root: " + root)
	}
	log.Warning("Media root paths may enter console/journal logs.")
	if cfg.RelaxedDiscovery {
		log.Warning("Relaxed discovery: relayed, multi-homed and public-IP DLNA devices are fetched; any LAN device can make go2tv contact other addresses.")
	}
}

// discoveryLogWriter routes device discovery logs, including SSDP policy
// rejections and strict-mode restrictions, to the server debug log.
type discoveryLogWriter struct{ log *serverLogger }

func (w discoveryLogWriter) Write(p []byte) (int, error) {
	w.log.Debug(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
