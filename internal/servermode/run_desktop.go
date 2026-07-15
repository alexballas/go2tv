//go:build !(android || ios)

package servermode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
)

func Run(ctx context.Context, cfg Config, output io.Writer) error {
	validated, err := Validate(cfg)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", validated.Listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()
	log := newServerLogger(output, validated.Debug)
	securityConfig := validated
	securityConfig.Listen = listener.Addr().String()
	runtime, err := newRuntime(validated, log)
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
}
