package servermode

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityBoundary(t *testing.T) {
	t.Parallel()
	cfg := Config{Listen: DefaultListen, AllowedOrigins: []string{"http://trusted.test:9666"}}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler, err := NewHandler(cfg, bytes.NewReader(make([]byte, 32)), next)
	if err != nil {
		t.Fatal(err)
	}

	bootstrap := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9666/api/bootstrap", nil)
	bootstrap.Host = "127.0.0.1:9666"
	bootstrap.Header.Set("Sec-Fetch-Site", "same-origin")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, bootstrap)
	if recorder.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d", recorder.Code)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d", len(cookies))
	}
	cookie := cookies[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/api" || cookie.Secure || cookie.Domain != "" {
		t.Fatalf("unsafe cookie attributes: %#v", cookie)
	}

	tests := []struct {
		name   string
		path   string
		host   string
		origin string
		cookie *http.Cookie
		want   int
	}{
		{name: "cookie API", path: "/api/state", host: "127.0.0.1:9666", cookie: cookie, want: http.StatusNoContent},
		{name: "websocket covered", path: "/api/ws", host: "127.0.0.1:9666", cookie: cookie, want: http.StatusNoContent},
		{name: "missing cookie", path: "/api/state", host: "127.0.0.1:9666", want: http.StatusForbidden},
		{name: "wrong cookie", path: "/api/state", host: "127.0.0.1:9666", cookie: &http.Cookie{Name: sessionCookie, Value: strings.Repeat("x", 43)}, want: http.StatusForbidden},
		{name: "unknown host", path: "/api/state", host: "evil.test:9666", cookie: cookie, want: http.StatusForbidden},
		{name: "multiple hosts", path: "/api/state", host: "127.0.0.1:9666, evil.test:9666", cookie: cookie, want: http.StatusForbidden},
		{name: "rebinding hostname", path: "/api/state", host: "rebind.test:9666", cookie: cookie, want: http.StatusForbidden},
		{name: "null origin", path: "/api/state", host: "127.0.0.1:9666", origin: "null", cookie: cookie, want: http.StatusForbidden},
		{name: "origin mismatch", path: "/api/state", host: "127.0.0.1:9666", origin: "http://evil.test:9666", cookie: cookie, want: http.StatusForbidden},
		{name: "two admitted values mismatch", path: "/api/state", host: "trusted.test:9666", origin: "http://127.0.0.1:9666", cookie: cookie, want: http.StatusForbidden},
		{name: "exact admitted origin and host", path: "/api/state", host: "trusted.test:9666", origin: "http://trusted.test:9666", cookie: cookie, want: http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://example.invalid"+tt.path, nil)
			req.Host = tt.host
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.cookie != nil {
				req.AddCookie(tt.cookie)
			}
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tt.want {
				t.Fatalf("status = %d, want %d", res.Code, tt.want)
			}
		})
	}
}

func TestBootstrapSourceValidation(t *testing.T) {
	t.Parallel()
	handler, err := NewHandler(Config{Listen: DefaultListen}, bytes.NewReader(make([]byte, 32)), nil)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		origin  string
		fetch   string
		referer string
		want    int
	}{
		{name: "same origin metadata", fetch: "same-origin", want: http.StatusOK},
		{name: "same origin header", origin: "http://127.0.0.1:9666", want: http.StatusOK},
		{name: "same origin referer", referer: "http://127.0.0.1:9666/page", want: http.StatusOK},
		{name: "missing proof", want: http.StatusForbidden},
		{name: "cross site", fetch: "cross-site", want: http.StatusForbidden},
		{name: "null", origin: "null", want: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9666/api/bootstrap", nil)
			req.Host = "127.0.0.1:9666"
			req.Header.Set("Origin", tt.origin)
			req.Header.Set("Sec-Fetch-Site", tt.fetch)
			req.Header.Set("Referer", tt.referer)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tt.want {
				t.Fatalf("status = %d, want %d", res.Code, tt.want)
			}
		})
	}
}

func TestSecretFailure(t *testing.T) {
	t.Parallel()
	_, err := NewHandler(Config{Listen: DefaultListen}, errorReader{}, nil)
	if !errors.Is(err, ErrSecretGeneration) {
		t.Fatalf("error = %v", err)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
