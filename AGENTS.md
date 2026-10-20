# AGENTS.md

**In all interactions and commit messages, be extremely concise and sacrifice grammar for the sake of concision.**

Go2TV casts local/remote media, torrents, internet radio and live streams to DLNA and Chromecast devices. Fyne GUI (desktop + Android), BubbleTea/TCell CLI, and headless server mode with a browser Web UI. Optional runtime dep: ffmpeg.

## Commands

```bash
make build              # build/go2tv
make build-lite         # CLI-only go2tv-lite
make webui              # rebuild Web UI bundle (internal/webui/src → dist)
go test ./...
npm test                # Web UI JS tests
go run cmd/fynedo-check/main.go internal/gui/   # UI updates outside fyne.Do
go run golang.org/x/tools/go/analysis/passes/modernize/cmd/modernize@latest -test ./...
make check-no-replace   # go.mod must not keep replace directives
```

## Done checklist

- Any Go change: `go test ./...`; modernize on touched packages (exits 3 on findings; older findings exist, fix only those in touched code).
- `internal/gui/` or code the GUI uses: `fynedo-check`, then `make android` (needs `ANDROID_HOME` = SDK, `ANDROID_NDK_HOME` = NDK r27d; bundles FFmpeg).
- `internal/webui/src/`: `npm test`, then `make webui`. `dist/` is embedded and committed with hashed names; commit removed old bundles too.
- `go.mod`: `make check-no-replace`.

## Gotchas

- **refyne**: go2tv uses the fork `github.com/alexballas/refyne/v2`, so Android packaging needs refyne's own `cmd/fyne`. `make android` provisions it at the go.mod version; don't pass `FYNE=` (if you do, it must be an executable path, not `go run`). Stock `fyne` fails with `does not import "fyne.io/fyne/v2/internal/driver/mobile/app"`.
- **fyne.Do**: network work (casting, discovery) runs in goroutines; every UI update from a goroutine goes through `fyne.Do`.
- **Two stacks**: GUI and CLI drive `soapcalls` (DLNA) and `castprotocol` (Chromecast) directly, branching on `devices.DeviceTypeDLNA`/`DeviceTypeChromecast`. `internal/controller` + `playback`/`playbackadapter`/`mediaserver` serve only server mode and Web UI.
- **GUI state**: `screen.getScreenState()` gives "Playing"/"Paused"/"Stopped"; reset state explicitly when switching device type.
- **Audio-only devices** (e.g. Chromecast Audio): block video/image casts when `screen.selectedDevice.isAudioOnly`.
- **Transcode config** lives in two structs: `soapcalls.TVPayload` (DLNA, `httphandlers`) and `utils.TranscodeOptions` (Chromecast).
- **Subtitles**: DLNA uses SRT, Chromecast WebVTT (`utils/srt_to_webvtt.go`).
- **SOAP**: XML entity escaping must stay Samsung-compatible (`soapcalls/soapbuilders.go`).
- Top-level `torrentstream/`, `mediasource/`, `castsubtitles/` are public facades over their `internal/` packages.
- Mobile variants: `*_mobile.go` and `//go:build android || ios` tags.

## Style

- Prefer `switch` over `else if` chains.
- Wrap errors as `fmt.Errorf("function: %w", err)`; package-level `ErrSomething` for common cases.
- Tests: table-driven (`tt := []struct{...}`), `t.Run` subtests, `t.Fatalf`.
- **NO STUPID TESTS:** every test must protect real user-visible behavior or a credible regression. Assert the contract; no implementation trivia, tautologies or redundant coverage.

## Plans

End each plan with a list of unresolved questions, if any. Extremely concise; sacrifice grammar.
