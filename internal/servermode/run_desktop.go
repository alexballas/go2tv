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
	securityConfig := validated
	securityConfig.Listen = listener.Addr().String()
	runtime, err := newRuntime(validated, output)
	if err != nil {
		return err
	}
	defer runtime.Close()
	handler, err := NewHandler(securityConfig, nil, runtime.web)
	if err != nil {
		return err
	}
	logStartup(output, validated, listener.Addr().String())

	server := &http.Server{Handler: accessLog(output, handler), ReadHeaderTimeout: defaultReadHeaderTimeout, ReadTimeout: defaultJSONTimeout, WriteTimeout: defaultJSONTimeout, IdleTimeout: defaultIdleTimeout, MaxHeaderBytes: defaultMaxHeaderBytes}
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

func logStartup(w io.Writer, cfg Config, actualListen string) {
	fmt.Fprintf(w, "Web server listening: %s\n", actualListen)
	fmt.Fprintln(w, "Usable allowed URLs:")
	if len(cfg.AllowedOrigins) != 0 {
		for _, origin := range cfg.AllowedOrigins {
			fmt.Fprintf(w, "  %s/\n", origin)
		}
	} else {
		host, port, _ := net.SplitHostPort(actualListen)
		fmt.Fprintf(w, "  http://%s/\n", net.JoinHostPort(host, port))
	}
	fmt.Fprintln(w, "WARNING: trusted-LAN mode; no TLS. Do not expose to untrusted networks.")
	fmt.Fprintln(w, "Media roots:")
	for _, root := range cfg.MediaRoots {
		fmt.Fprintf(w, "  %s\n", root)
	}
	fmt.Fprintln(w, "WARNING: media root paths may enter console/journal logs.")
}
