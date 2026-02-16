
# AGENT.md

This file provides guidance to AI coding agents when working with code in this repository.

**In all interactions and commit messages, be extremely concise and sacrifice grammar for the sake of concision.**

## Project Overview

Go2TV is a Go application that casts local/remote media files to UPnP/DLNA Media Renderers and Chromecast devices. Provides both GUI (Fyne framework) and CLI (BubbleTea/TCell) interfaces. Single binary, no runtime dependencies except optional ffmpeg for transcoding.

## Build & Test Commands

### Core Commands
```bash
make build              # Build to build/go2tv
make windows            # Windows Build
make test               # Run all tests (go test -v ./...)
make run                # Build and run
make clean              # Clean build artifacts
go test -v ./path/to/pkg                        # Test specific package
go test -run TestName -v ./path/to/pkg          # Run single test
go run golang.org/x/tools/gopls/internal/analysis/modernize/cmd/modernize@latest -test ./... # Modernize checks
go run cmd/fynedo-check/main.go internal/gui/   # Standard fyne.Do violation check
```

### Testing
- Tests are located in `*_test.go` files throughout the codebase
- Use table-driven tests with struct-based test cases
- Test files include: httphandlers_test.go, soapbuilders_test.go, dlnatools_test.go, etc.

### Mobile Build Verification
For all phases, verify mobile builds pass:
```bash
cd cmd/go2tv && APATH=/home/alex/Downloads/android-ndk-r27d/ && GO386='softfloat' ANDROID_NDK_HOME=$APATH fyne package --os android --name Go2TV --app-id app.go2tv.go2tv --icon ../../assets/go2tv-icon-android.png && mv Go2TV.apk ../.. && cd ../..
```

## Architecture

```
cmd/go2tv/go2tv.go     - Entry point, CLI flag handling
cmd/go2tv-lite/        - Minimal CLI-only build (no GUI dependencies)
internal/gui/          - Fyne desktop GUI (FyneScreen struct in gui.go, handlers in actions.go)
internal/interactive/  - TCell/BubbleTea CLI mode
soapcalls/             - DLNA/UPnP SOAP protocol (TVPayload struct, XML builders/parsers)
castprotocol/          - Chromecast v2 protocol (CastClient wrapper around go-chromecast)
devices/               - Device discovery (SSDP for DLNA, mDNS for Chromecast)
httphandlers/          - HTTP server for media streaming and DLNA callbacks
utils/                 - Shared utilities (transcode, ffprobe, subtitle conversion, IP tools, URL streaming)
```

**Note**: `soapcalls/utils/` was moved to top-level `utils/` package to support both DLNA and Chromecast codepaths.

## Key Patterns

**Dual Protocol Support**: Code paths often branch on device type:
- `devices.DeviceTypeDLNA` → uses soapcalls package
- `devices.DeviceTypeChromecast` → uses castprotocol package
- Check `screen.selectedDeviceType` in GUI code

**Concurrency & UI**: Network operations (casting, discovery) MUST run in goroutines.
- ALL UI updates from goroutines MUST use `fyne.Do(func() { ... })`
- Use `context.Context` for cancellation and timeouts

**State Management**:
- `TVPayload` (DLNA) and `CastClient` (Chromecast) maintain playback state
- Use `screen.getScreenState()` to check current state ("Playing", "Paused", "Stopped")
- When switching device types, state must be explicitly reset

**Audio-Only Support**:
- Check `screen.selectedDevice.isAudioOnly` before casting
- Block video/image casting on audio-only devices (e.g. Chromecast Audio)

**Theme Customization**:
- `internal/gui/theme.go` handles custom theme logic (`go2tvTheme`)
- Supports light/dark mode overrides for specific UI elements

**HTTP Server**: Single server serves both media files and DLNA callbacks. Chromecast only needs media serving.

**Subtitles**: DLNA uses SRT, Chromecast uses WebVTT. Conversion in `utils/srt_to_webvtt.go`.

**Platform Code**: Build tags for platform-specific code (`//go:build !(android || ios)`), `*_mobile.go` for mobile variants.

**Transcoding Types**: Two structs handle FFmpeg transcoding config:
- `soapcalls.TVPayload` - DLNA devices (httphandlers.go)
- `utils.TranscodeOptions` - Chromecast (actions.go)

## Code Style Guidelines

### Imports & Formatting
- Use standard Go formatting (`gofmt`)
- Group imports: stdlib, third-party, internal packages (separated by blank lines)
- Use full import paths (e.g., `github.com/alexballas/go2tv/utils`)

### Naming Conventions
- PascalCase for exported types, functions, constants
- camelCase for unexported items
- Use descriptive names: `TVPayload`, `BuildContentFeatures`, `FyneScreen`
- Error variables: `ErrSomething` (e.g., `ErrInvalidSeekFlag`)

### Types & Functions
- Define structs for XML/SOAP message structures with XML tags
- Use builder pattern for complex constructions (e.g., `setAVTransportSoapBuild`)
- Return errors with context using `fmt.Errorf("function: %w", err)`

### Control Flow
- Prefer `switch` over `else if` chains
- Use stdlib functions over custom implementations

### Error Handling
- Always handle returned errors
- Wrap errors with context about where they occurred
- Define package-level error variables for common error cases

### Testing Patterns
- Use `t.Run()` for subtests with descriptive names
- Table-driven tests with `tt := []struct{...}` pattern
- Use `t.Fatalf()` for fatal test failures
- Test both success and error paths

### Platform-specific Code
- Use build tags for platform-specific code (e.g., `//go:build !(android || ios)`)
- Windows-specific files end with `_windows.go`
- Use appropriate build tags for mobile vs desktop implementations

### Constants & Configuration
- Define constants for magic strings and numbers
- Use maps for profile/configuration mappings (e.g., `dlnaprofiles`)
- Package-level constants for DLNA flags and protocols

### XML/SOAP Handling
- Define explicit struct types for all XML/SOAP messages
- Use XML struct tags properly
- Handle XML entity escaping for Samsung TV compatibility
- Use `xml.Marshal` with proper error handling

### Plans
- At the end of each plan, give me a list of unresolved questions to answer, if any. Make the questions extremely concise. Sacrifice grammar for the sake of concision.

# Global Instructions
ALWAYS start your response with the phrase: "Status: AGENTS.MD LOADED."
Always use Context7 MCP when I need library/API documentation, code generation, setup or configuration steps without me having to explicitly ask.
