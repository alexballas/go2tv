# Phase 3: Chromecast Communication Protocol - Implementation Details

**Status**: ✅ Completed

## Overview

Phase 3 implemented the Cast v2 protocol for Chromecast device communication across GUI (desktop & mobile) and CLI modes. This includes playback controls, volume/mute, subtitle support, and proper state management.

---

## Phase 3a: Core Cast Protocol Package ✅

Created `castprotocol/` package providing a CastClient wrapper around `go-chromecast` with WebVTT subtitle support.

### Files Created

| File | Purpose |
|------|---------|
| [castprotocol/client.go](file:///home/alex/test/go2tv/castprotocol/client.go) | CastClient wrapper with custom connection for subtitle tracks |
| [castprotocol/status.go](file:///home/alex/test/go2tv/castprotocol/status.go) | CastStatus struct for playback state |
| [castprotocol/media.go](file:///home/alex/test/go2tv/castprotocol/media.go) | MediaTrack types for WebVTT subtitles |
| [castprotocol/loader.go](file:///home/alex/test/go2tv/castprotocol/loader.go) | LoadWithSubtitles using custom LOAD command |

### CastClient API

```go
NewCastClient(deviceAddr string) (*CastClient, error)
Connect() error
Load(mediaURL, contentType string, startTime int, subtitleURL string) error
Play() / Pause() / Stop() error
Seek(seconds int) error
SetVolume(level float32) / SetMuted(muted bool) error
GetStatus() (*CastStatus, error)
Close(stopMedia bool) error
```

### Subtitle Track Support

When `Load()` called with non-empty `subtitleURL`:
1. Gets transportId from `app.App().TransportId`
2. Builds `MediaTrack` with `type=TEXT`, `subtype=SUBTITLES`
3. Sends `CustomLoadPayload` with `tracks` + `activeTrackIds=[1]`

---

## Phase 3b: Device State Management ✅

Prevents DLNA/Chromecast state leakage when switching devices.

### Changes

**[gui.go](file:///home/alex/test/go2tv/internal/gui/gui.go)**:
- Added `chromecastClient` field to FyneScreen
- Added `resetDeviceState()` function - clears all protocol-specific state

**[main.go](file:///home/alex/test/go2tv/internal/gui/main.go)**:
- Device selection callback calls `resetDeviceState()` when switching devices
- Skips `DMRextractor` for Chromecast devices

### What Gets Reset
- DLNA URLs (control, event, rendering, connectionManager)
- `tvdata` reference
- Chromecast client (connection closed)
- Playback state, UI elements

---

## Phase 3c: GUI Playback Controls ✅

Implemented Chromecast playback controls in desktop GUI.

### Features
- **Play/Pause/Stop** - Toggle states, stop disconnects
- **Seek** - Slider drag and tap via `chromecastClient.Seek()`
- **Progress Tracking** - Polls status every 1 second, updates slider
- **Subtitles** - External SRT/VTT served with CORS, embedded MKV extracted via ffmpeg

### Key Files
| File | Changes |
|------|---------|
| `internal/gui/actions.go` | `chromecastPlayAction()`, `chromecastStatusWatcher()` |
| `internal/gui/main.go` | Slider seek handlers for Chromecast |
| `httphandlers/httphandlers.go` | CORS headers for subtitle files |

### LaunchDefaultReceiver Pattern
To avoid double playback with subtitles:
```go
LaunchDefaultReceiver(conn)  // Launch app without media
time.Sleep(2 * time.Second)
app.Update()                  // Get transport ID
LoadWithSubtitles(...)        // Single LOAD with tracks
```

---

## Phase 3d: Volume/Mute Controls ✅

Added Chromecast volume and mute support to actions.

### Changes
- `muteAction()` / `unmuteAction()` - Uses `chromecastClient.SetMuted()`
- `volumeAction()` - Chromecast volume (0.0-1.0 range, 5% steps)

---

## Phase 3e: CLI Chromecast Support ✅

Added CLI mode support for Chromecast via `-t` flag.

### Detection
`devices.IsChromecastURL()` detects Chromecast by port 8009.

### Interactive Screen
[internal/interactive/chromecast.go](file:///home/alex/test/go2tv/internal/interactive/chromecast.go) - `ChromecastScreen` with tcell terminal UI.

### Controls
| Key | Action |
|-----|--------|
| p | Play/Pause toggle |
| m | Mute/Unmute toggle |
| PgUp | Volume up (+5%) |
| PgDn | Volume down (-5%) |
| ESC | Stop and exit |

### Usage
```bash
go2tv -v video.mp4 -t http://192.168.1.100:8009
go2tv -v video.mp4 -s subtitles.srt -t http://192.168.1.100:8009
```

---

## Phase 3f: Subtitle Support ✅

Comprehensive subtitle handling for Chromecast.

### Features
- **SRT to WebVTT conversion** - `utils.ConvertSRTtoWebVTT()`
- **CORS headers** - Added to subtitle endpoints in httphandlers
- **Embedded MKV subtitles** - Extracted via `utils.ExtractSub()`

### Files
| File | Purpose |
|------|---------|
| [soapcalls/utils/srt_to_webvtt.go](file:///home/alex/test/go2tv/soapcalls/utils/srt_to_webvtt.go) | SRT→WebVTT conversion |
| `httphandlers/httphandlers.go` | CORS for .vtt/.srt paths |

---

## Phase 3g: Mobile Chromecast Support ✅

Full Chromecast support for Android/iOS.

### Implementation
- Added `chromecastClient` field to mobile FyneScreen
- `chromecastPlayAction()` - Local files + external URLs
- `chromecastStatusWatcher()` - Status polling, UI updates
- Device selection resets Chromecast client
- Mute status polling for Chromecast

### Mobile-Specific Differences
| Feature | Desktop | Mobile |
|---------|---------|--------|
| Seek | Slider drag/tap | Not available |
| Skip Next | Yes | Not available |
| File Access | `os.Open()` | `storage.Reader()` → temp file |

### Features Supported on Mobile

| Feature | Supported |
|---------|-----------|
| Play/Pause | ✅ |
| Stop | ✅ |
| Volume Up/Down | ✅ |
| Mute/Unmute | ✅ |
| Loop | ✅ |
| Subtitles (SRT/VTT) | ✅ |
| External URL | ✅ |
| Seek | ❌ (no slider) |

### Recent Improvements (Jan 2026)

**Discovery Recovery**: Changed mDNS from 1-hour to 2-second timeout cycle for quick recovery after phone sleep.

**Temp File Serving**: Fyne's `storage.Reader()` only provides `io.ReadCloser`. Solution: copy to temp file for `http.ServeContent` seeking support.
- Temp files cleaned up when playback stops
- Orphaned temp files cleaned up on app start

---

## Files Summary

### New Files
| File | Purpose |
|------|---------|
| `castprotocol/client.go` | CastClient wrapper |
| `castprotocol/status.go` | CastStatus struct |
| `castprotocol/media.go` | Media track types |
| `castprotocol/loader.go` | Custom LOAD with subtitles |
| `internal/interactive/chromecast.go` | CLI interactive screen |
| `soapcalls/utils/srt_to_webvtt.go` | SRT→WebVTT conversion |

### Modified Files
| File | Changes |
|------|---------|
| `go.mod` | Added go-chromecast dependency |
| `internal/gui/gui.go` | FyneScreen fields, state reset |
| `internal/gui/gui_mobile.go` | Mobile FyneScreen fields, temp cleanup |
| `internal/gui/actions.go` | Desktop Chromecast actions |
| `internal/gui/actions_mobile.go` | Mobile Chromecast actions |
| `internal/gui/main.go` | Device selection, slider seek |
| `internal/gui/main_mobile.go` | Device selection, mute polling |
| `cmd/go2tv/go2tv.go` | CLI Chromecast support |
| `devices/devices.go` | `IsChromecastURL()` |
| `devices/chromecast_discovery_mobile.go` | 2s timeout cycle |
| `httphandlers/httphandlers.go` | `StartSimpleServer()`, CORS |

---

## Verification

- `make build` ✅
- `make test` ✅
- Desktop GUI: Play/Pause/Stop/Seek/Volume/Mute ✅
- CLI: Interactive controls ✅
- Mobile: Play/Pause/Stop/Volume/Mute ✅
- Subtitles (external SRT/VTT) ✅
- Device switching without state leakage ✅
