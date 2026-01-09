# Phase 3g: Mobile Chromecast Support - Implementation Details

## Overview

Added Chromecast support to the mobile (Android/iOS) version of Go2TV, enabling playback on Chromecast devices from mobile apps.

## Implementation

### 1. FyneScreen Struct Updates (`gui_mobile.go`)

Added fields for Chromecast client and context management:

```go
type FyneScreen struct {
    // ... existing fields ...
    chromecastClient *castprotocol.CastClient
    serverStopCTX    context.Context
    cancelServerStop context.CancelFunc
}
```

### 2. Chromecast Discovery (`gui_mobile.go`)

Started Chromecast discovery loop in `Start()`:

```go
func Start(ctx context.Context, s *FyneScreen) {
    go devices.StartChromecastDiscoveryLoop(ctx)
    // ...
}
```

### 3. Device Discovery (`actions_mobile.go`)

Updated `getDevices()` to use `LoadAllDevices()` which includes both DLNA and Chromecast devices:

```go
func getDevices(delay int) ([]devType, error) {
    deviceList, err := devices.LoadAllDevices(delay)
    // ...
}
```

### 4. Chromecast Play Action (`actions_mobile.go`)

Created `chromecastPlayAction()` that:
- Handles pause/resume for already playing media
- Creates CastClient if not already connected
- Supports both local files (via HTTP server) and external URLs
- Converts SRT subtitles to WebVTT
- Starts status watcher for UI updates

### 5. Status Watcher (`actions_mobile.go`)

Created `chromecastStatusWatcher()` that:
- Polls Chromecast status every second
- Updates Play/Pause button based on state
- Triggers `Fini()` when media ends (for loop support)
- Uses 1.5s threshold for media completion detection

### 6. Updated Actions

#### `playAction()`
Branches to `chromecastPlayAction()` when device type is Chromecast.

#### `stopAction()`
Handles Chromecast stop with client cleanup:
```go
if screen.chromecastClient != nil && screen.chromecastClient.IsConnected() {
    _ = screen.chromecastClient.Stop()
    screen.chromecastClient.Close(false)
    screen.chromecastClient = nil
    // ...
}
```

#### `muteAction()` / `unmuteAction()`
Added Chromecast mute/unmute via `chromecastClient.SetMuted()`.

#### `volumeAction()`
Added Chromecast volume control (0.0-1.0 range, 5% steps).

### 7. Device Selection (`main_mobile.go`)

Updated `list.OnSelected` callback to:
- Reset Chromecast client when switching devices
- Skip `DMRextractor` for Chromecast devices
- Clear DLNA-specific state when selecting Chromecast

### 8. Mute Status Polling (`main_mobile.go`)

Updated `checkMutefunc()` to poll Chromecast mute status instead of DLNA SOAP calls when Chromecast is selected.

## Key Differences from Desktop

1. **No Slider/Seek**: Mobile version doesn't have seek slider, so no seek functionality
2. **No Skip Next**: Mobile doesn't have skip next feature
3. **URI vs Path**: Mobile uses `fyne.URI` for media files, desktop uses file paths
4. **Subtitles via Reader**: Mobile reads subtitle files via `storage.Reader()` instead of filesystem

## Files Modified

- `internal/gui/gui_mobile.go` - Added FyneScreen fields, Chromecast discovery start
- `internal/gui/actions_mobile.go` - Added Chromecast actions, updated existing actions
- `internal/gui/main_mobile.go` - Updated device selection, mute status polling

## Supported Features on Mobile

| Feature | Supported |
|---------|-----------|
| Play/Pause | Yes |
| Stop | Yes |
| Volume Up/Down | Yes |
| Mute/Unmute | Yes |
| Loop | Yes |
| Subtitles (SRT/VTT) | Yes |
| External URL | Yes |
| Seek | No (no slider) |
| Skip Next | No (no feature) |

## Usage

1. Select media file or enter URL
2. Select Chromecast device from list
3. Tap Play button
4. Use volume/mute buttons for audio control
5. Tap Stop to end playback

---

## Recent Improvements

### Chromecast Discovery Recovery (Jan 2026)

Changed mDNS discovery from 1-hour timeout to 2-second cycle + 500ms delay. This matches the desktop pattern and allows discovery to recover quickly when the phone wakes from sleep.

**File**: `devices/chromecast_discovery_mobile.go`

### Temp File Serving for http.ServeContent (Jan 2026)

Fyne's `storage.Reader()` only provides `io.ReadCloser` which doesn't support seeking. `http.ServeContent` requires `io.ReadSeeker` for range requests (video seeking).

**Solution**: Copy media files to temp directory before serving:
- Enables proper HTTP range requests for video seeking
- Temp files cleaned up when playback stops
- Orphaned temp files cleaned up on app start (crash recovery)

**Files**:
- `internal/gui/gui_mobile.go` - Added `tempMediaFile` field, temp cleanup on start
- `internal/gui/actions_mobile.go` - Temp file copy for both DLNA and Chromecast
