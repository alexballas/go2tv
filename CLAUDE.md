# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Go2TV is a Go application that casts local/remote media files to UPnP/DLNA Media Renderers and Chromecast devices. Provides both GUI (Fyne framework) and CLI (BubbleTea/TCell) interfaces. Single binary, no runtime dependencies except optional ffmpeg for transcoding.

## Build & Test Commands

```bash
make build              # Build to build/go2tv
make test               # Run all tests (go test -v ./...)
make run                # Build and run
make clean              # Clean build artifacts
go test -v ./path/to/pkg                        # Test specific package
go test -run TestName -v ./path/to/pkg          # Run single test
```

## Architecture

```
cmd/go2tv/go2tv.go     - Entry point, CLI flag handling
internal/gui/          - Fyne desktop GUI (FyneScreen struct in gui.go, handlers in actions.go)
internal/interactive/  - TCell/BubbleTea CLI mode
soapcalls/             - DLNA/UPnP SOAP protocol (TVPayload struct, XML builders/parsers)
castprotocol/          - Chromecast v2 protocol (CastClient wrapper)
devices/               - Device discovery (SSDP for DLNA, mDNS for Chromecast)
httphandlers/          - HTTP server for media streaming and DLNA callbacks
soapcalls/utils/       - MIME detection, ffmpeg transcoding, subtitle conversion
```

## Key Patterns

**Dual Protocol Support**: Code paths often branch on device type:
- `devices.DeviceTypeDLNA` → uses soapcalls package
- `devices.DeviceTypeChromecast` → uses castprotocol package
- Check `screen.selectedDeviceType` in GUI code

**State Management**: `TVPayload` (DLNA) and `CastClient` (Chromecast) maintain playback state. When switching device types, state must be explicitly reset.

**HTTP Server**: Single server serves both media files and DLNA callbacks. Chromecast only needs media serving.

**Subtitles**: DLNA uses SRT, Chromecast uses WebVTT. Conversion in `soapcalls/utils/srt_to_webvtt.go`.

**Platform Code**: Build tags for platform-specific code (`//go:build !(android || ios)`), `*_mobile.go` for mobile variants.

## Code Style

See AGENT.md for complete guidelines. Be extremely concise in interactions and commit messages. Key points:
- Imports: stdlib, third-party, internal (blank line separated)
- Errors: wrap with context `fmt.Errorf("function: %w", err)`
- Tests: table-driven with `t.Run()` subtests
- XML/SOAP: explicit struct types with XML tags
- Control flow: prefer `switch` over `else if`, use stdlib over custom implementations

## Current Development

Branch `feature/chromecast` implements Chromecast v2 protocol support. See `chromecast_v2_roadmap.md` for implementation phases.
