package servermode

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.status = http.StatusSwitchingProtocols
	return w.ResponseWriter.(http.Hijacker).Hijack()
}
func accessLog(output io.Writer, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wrapped := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(wrapped, r)
		status := wrapped.status
		if status == 0 {
			status = http.StatusOK
		}
		// Successful browser traffic and missing optional artwork are expected and
		// high-volume. Keep console output for failures that need attention.
		if status < http.StatusBadRequest || status == http.StatusNotFound {
			return
		}
		fmt.Fprintf(output, "HTTP %s %s %d\n", r.Method, routeTemplate(r.URL.Path), status)
	})
}
func routeTemplate(path string) string {
	switch {
	case path == "/":
		return "/"
	case strings.HasPrefix(path, "/assets/"):
		return "/assets/{asset}"
	case path == "/api/bootstrap":
		return "/api/bootstrap"
	case path == "/api/library":
		return "/api/library"
	case path == "/api/thumbnail":
		return "/api/thumbnail"
	case path == "/api/media-artwork":
		return "/api/media-artwork"
	case strings.HasPrefix(path, "/api/artwork/"):
		return "/api/artwork/{content-id}.jpg"
	case path == "/api/ws":
		return "/api/ws"
	default:
		return "{unmatched}"
	}
}
