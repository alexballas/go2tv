package servermode

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAccessLogSuppressesRoutineTraffic(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{name: "success", status: http.StatusOK},
		{name: "redirect", status: http.StatusNotModified},
		{name: "optional artwork missing", status: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			handler := accessLog(&output, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			}))
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/thumbnail", nil))
			if output.Len() != 0 {
				t.Fatalf("log = %q, want empty", output.String())
			}
		})
	}
}

func TestAccessLogReportsActionableFailures(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		status int
		want   string
	}{
		{name: "invalid thumbnail", path: "/api/thumbnail", status: http.StatusBadRequest, want: "HTTP GET /api/thumbnail 400\n"},
		{name: "artwork failure", path: "/api/media-artwork", status: http.StatusInternalServerError, want: "HTTP GET /api/media-artwork 500\n"},
		{name: "method", path: "/api/library", status: http.StatusMethodNotAllowed, want: "HTTP GET /api/library 405\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			handler := accessLog(&output, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			}))
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, tt.path, nil))
			if got := output.String(); got != tt.want {
				t.Fatalf("log = %q, want %q", got, tt.want)
			}
		})
	}
}
