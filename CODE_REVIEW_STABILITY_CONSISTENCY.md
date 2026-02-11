# Go2TV Code Review Report (Stability + Consistency)

Date: 2026-02-10
Reviewer: OpenCode (automated + manual)

## Scope

- Full repo review, with focus on crash risk, hangs, leaks, race risk, and codepath consistency.
- Protocol paths covered: DLNA, Chromecast, GUI, CLI, HTTP handlers, SOAP calls.

## Validation Run

- `go test -v ./...` -> pass
- `go test -race ./...` -> pass
- `go vet ./...` -> pass
- `go run cmd/fynedo-check/main.go internal/gui/` -> pass (no `fyne.Do` violations)

## Executive Summary

- Overall quality good; test baseline solid.
- Main risk cluster in `soapcalls` (timer map concurrency + network timeout/cancel behavior).
- Several concrete resource leaks found (open files/readers not closed).
- Some DLNA vs mobile/CLI behavior drift can cause user-visible inconsistent behavior.

## Findings

### Critical

### High


5) **HTTP server created with zero timeouts**
- Evidence: `httphandlers/httphandlers.go:387`
- Why risky: no `ReadHeaderTimeout`/`ReadTimeout`/`WriteTimeout`/`IdleTimeout`.
- Impact: slowloris/resource exhaustion risk and hanging connections.
- Fix: set conservative timeouts in `NewServer`.


7) **Interactive mode ticker goroutines never stop (lifecycle leak)**
- Evidence: `internal/interactive/interactive.go:95`, `internal/interactive/interactive.go:97`, `internal/interactive/chromecast.go:109`, `internal/interactive/chromecast.go:111`
- Why risky: ticker keeps running after `Fini`; can keep touching closed screen.
- Impact: goroutine leak + possible invalid UI calls.
- Fix: store ticker/cancel context on struct; stop ticker on `Fini`.


### Medium

12) **GUI background loops have no cancel path (known TODO)**
- Evidence: `internal/gui/main.go:839`, `internal/gui/main.go:846`, `internal/gui/main.go:850`
- Why risky: long-running goroutines/tickers (`refreshDevList`, `checkMutefunc`, `sliderUpdate`) not tied to shutdown context.
- Impact: lifecycle leaks / harder deterministic shutdown.
- Fix: wire app/window cancel context into all loop goroutines.

## Strengths Worth Keeping

- Strong test baseline; race/vet clean for covered paths.
- Good use of `fyne.Do` patterns; dedicated checker currently green.
- Useful protocol split pattern (`DLNA` vs `Chromecast`) across GUI/CLI.
- Good path traversal hardening in directory serving flow (`httphandlers/httphandlers.go:212`).
- FFmpeg lifecycle tied to request context in transcoding helpers (`utils/transcode.go:121`, `utils/chromecast_transcode.go:147`).

## Priority Fix Order

1. Timer map synchronization + renderer state nil checks (`soapcalls`).
2. Network timeout/cancel model (export context, timeouts, no bare `Background()` in hot paths).
3. Close resource leaks (unsubscribe body, file opens, stream readers).
4. Harden server/handler edge cases (`http.Server` timeouts, SID len guard, `getNextMedia` guards).
5. Remove cross-codepath behavior drift (mobile pause token, CLI/lite URL prep).
