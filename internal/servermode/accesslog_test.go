//go:build !(android || ios)

package servermode

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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
			handler := accessLog(newServerLogger(&output, false), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			}))
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/thumbnail", nil))
			if output.Len() != 0 {
				t.Fatalf("log = %q, want empty", output.String())
			}
		})
	}
}

func TestAccessLogReportsFailuresWithoutDynamicPaths(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		status    int
		wantRoute string
		forbidden string
	}{
		{name: "known route", path: "/api/thumbnail", status: http.StatusBadRequest, wantRoute: "/api/thumbnail"},
		{name: "artwork ID", path: "/api/artwork/private-id.jpg", status: http.StatusInternalServerError, wantRoute: "/api/artwork/{content-id}.jpg", forbidden: "private-id"},
		{name: "unmatched", path: "/private/path", status: http.StatusMethodNotAllowed, wantRoute: "{unmatched}", forbidden: "/private/path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			handler := accessLog(newServerLogger(&output, false), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			}))
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, tt.path, nil))
			got := output.String()
			if !strings.Contains(got, "GET "+tt.wantRoute) || !strings.Contains(got, strconv.Itoa(tt.status)) {
				t.Fatalf("log = %q", got)
			}
			if tt.forbidden != "" && strings.Contains(got, tt.forbidden) {
				t.Fatalf("dynamic path leaked: %q", got)
			}
		})
	}
}
