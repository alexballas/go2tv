# Phase 3e: CLI Chromecast Support - Implementation Details

## Overview

Added CLI support for Chromecast devices via the `-t` flag, enabling `go2tv -v file.mp4 -t http://chromecast:8009` to work with an interactive terminal UI.

## Implementation

### 1. Device Type Detection (`devices/devices.go`)

Added `IsChromecastURL()` to detect Chromecast devices by port:

```go
func IsChromecastURL(deviceURL string) bool {
    u, err := url.Parse(deviceURL)
    if err != nil {
        return false
    }
    return u.Port() == "8009"
}
```

### 2. Interactive Screen (`internal/interactive/chromecast.go`)

Created `ChromecastScreen` struct mirroring the DLNA `NewScreen` but using `CastClient`:

```go
type ChromecastScreen struct {
    Current     tcell.Screen
    Client      *castprotocol.CastClient
    exitCTXfunc context.CancelFunc
    mediaTitle  string
    lastAction  string
    mu          sync.RWMutex
}
```

Key methods:
- `EmitMsg()` - displays status with mute indicator
- `InterInit()` - initializes screen, starts status polling
- `HandleKeyEvent()` - handles p (play/pause), m (mute), PgUp/PgDn (volume), ESC (stop/exit)
- `InitChromecastScreen()` - factory function

Status polling includes media completion detection using 1.5s threshold (same as GUI fix).

### 3. CLI Entry Point (`cmd/go2tv/go2tv.go`)

Added branching in `run()` to detect Chromecast URLs:

```go
if devices.IsChromecastURL(flagRes.dmrURL) {
    return runChromecastCLI(exitCTX, cancel, flagRes.dmrURL, absMediaFile, mediaType, absSubtitlesFile)
}
```

Added `runChromecastCLI()` function that:
1. Creates and connects CastClient
2. Starts HTTP server for media serving
3. Handles subtitle conversion (SRT → WebVTT)
4. Initializes ChromecastScreen
5. Loads media asynchronously
6. Runs interactive loop

### 4. Subtitle Support

CLI Chromecast supports both SRT and VTT subtitles:
- SRT files converted to WebVTT via `utils.ConvertSRTtoWebVTT()`
- Served at `/subtitles.vtt` endpoint

## Key Controls

| Key | Action |
|-----|--------|
| p | Play/Pause toggle |
| m | Mute/Unmute toggle |
| PgUp | Volume up (+5%) |
| PgDn | Volume down (-5%) |
| ESC | Stop and exit |

## Files Modified

- `devices/devices.go` - added `IsChromecastURL()`
- `cmd/go2tv/go2tv.go` - added `runChromecastCLI()`, branching logic, castprotocol import

## Files Created

- `internal/interactive/chromecast.go` - ChromecastScreen implementation

## Usage

```bash
# Play local file on Chromecast
go2tv -v video.mp4 -t http://192.168.1.100:8009

# Play with subtitles
go2tv -v video.mp4 -s subtitles.srt -t http://192.168.1.100:8009
```

## Code Style Notes

- Used `switch` over `else if` chains (per AGENT.md)
- Used `url.Parse()` instead of manual string parsing
- Async `Load()` call to prevent blocking
