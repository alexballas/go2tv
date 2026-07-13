package servermode

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

const sessionCookie = "go2tv_session"

var ErrSecretGeneration = errors.New("session secret generation failed")

type securityHandler struct {
	next           http.Handler
	secret         string
	allowedHosts   map[string]struct{}
	allowedOrigins map[string]struct{}
	bootstrapNext  bool
}

func NewHandler(cfg Config, random io.Reader, next http.Handler) (http.Handler, error) {
	if random == nil {
		random = rand.Reader
	}
	secretBytes := make([]byte, 32)
	if _, err := io.ReadFull(random, secretBytes); err != nil {
		return nil, ErrSecretGeneration
	}
	_, bootstrapNext := next.(interface{ ServesBootstrap() })
	if next == nil {
		next = http.NotFoundHandler()
	}
	h := &securityHandler{
		next:           next,
		secret:         base64.RawURLEncoding.EncodeToString(secretBytes),
		allowedHosts:   make(map[string]struct{}),
		allowedOrigins: make(map[string]struct{}),
		bootstrapNext:  bootstrapNext,
	}
	listenHost, port, _ := net.SplitHostPort(cfg.Listen)
	if isLoopbackHost(listenHost) {
		for _, host := range []string{"127.0.0.1", "localhost", "::1"} {
			h.allowedHosts[net.JoinHostPort(host, port)] = struct{}{}
			h.allowedOrigins["http://"+net.JoinHostPort(host, port)] = struct{}{}
		}
	}
	for _, origin := range cfg.AllowedOrigins {
		h.allowedOrigins[origin] = struct{}{}
		u, _ := url.Parse(origin)
		h.allowedHosts[u.Host] = struct{}{}
	}
	return h, nil
}

func (h *securityHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w.Header())
	w.Header().Set("Cache-Control", "no-store")
	if !h.validHost(r.Host) {
		writeAPIError(w, http.StatusForbidden, "request_not_allowed")
		return
	}
	if r.URL.Path == "/api/bootstrap" {
		if !h.validBootstrapSource(r) {
			writeAPIError(w, http.StatusForbidden, "request_not_allowed")
			return
		}
		h.setCookie(w)
		if h.bootstrapNext {
			h.next.ServeHTTP(w, r)
		} else {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ok":true}`)
		}
		return
	}
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
		validOrigin := h.validOriginIfPresent(r)
		if r.URL.Path == "/api/ws" && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			validOrigin = r.Header.Get("Origin") != "" && h.sameOriginRequest(r, r.Header.Get("Origin"))
		}
		if !h.validCookie(r) || !validOrigin {
			writeAPIError(w, http.StatusForbidden, "request_not_allowed")
			return
		}
	}
	h.next.ServeHTTP(w, r)
}

func setSecurityHeaders(header http.Header) {
	header.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "same-origin")
	header.Set("Permissions-Policy", "accelerometer=(), autoplay=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()")
}

func (h *securityHandler) validHost(host string) bool {
	if strings.Contains(host, ",") || strings.TrimSpace(host) != host {
		return false
	}
	canonical, err := canonicalAuthority(host)
	if err != nil {
		return false
	}
	_, ok := h.allowedHosts[canonical]
	return ok
}

func canonicalAuthority(authority string) (string, error) {
	host, port, err := net.SplitHostPort(authority)
	if err != nil || host == "" || port == "" {
		return "", ErrInvalidListen
	}
	return net.JoinHostPort(canonicalHost(host), port), nil
}

func (h *securityHandler) validBootstrapSource(r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		return h.sameOriginRequest(r, origin)
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin"
	}
	ref := r.Referer()
	if ref == "" {
		return false
	}
	u, err := url.Parse(ref)
	if err != nil {
		return false
	}
	return h.sameOriginRequest(r, u.Scheme+"://"+u.Host)
}

func (h *securityHandler) validOriginIfPresent(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "" || h.sameOriginRequest(r, origin)
}

func (h *securityHandler) sameOriginRequest(r *http.Request, raw string) bool {
	origin, err := canonicalOrigin(raw)
	if err != nil {
		return false
	}
	u, _ := url.Parse(origin)
	host, err := canonicalAuthority(r.Host)
	if err != nil || u.Host != host {
		return false
	}
	_, ok := h.allowedOrigins[origin]
	return ok
}

func (h *securityHandler) setCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    h.secret,
		Path:     "/api",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func (h *securityHandler) validCookie(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || len(cookie.Value) != len(h.secret) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(h.secret)) == 1
}

func writeAPIError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{Error: code})
}
