# Phase 3C: GUI Playback Controls Implementation

## Summary

This phase implemented Chromecast playback controls in the GUI, including play/pause/stop, seek, progress tracking, and subtitle support.

## Features Implemented

### ✅ Core Playback Controls
- **Play/Pause** - Toggle between play and pause states
- **Stop** - Stop playback and disconnect from Chromecast
- **Button state sync** - UI buttons correctly reflect Chromecast state

### ✅ Seek Support
- **Slider drag** - Drag to seek to position
- **Slider tap** - Tap to jump to position
- Uses `chromecastClient.Seek()` for Chromecast, falls back to DLNA for DLNA devices

### ✅ Progress Tracking
- Status watcher polls Chromecast every 1 second
- Updates slider position based on `CurrentTime/Duration`
- Detects media completion when `CurrentTime >= Duration - 1.5s` (go-chromecast doesn't reliably report IDLE)

### ✅ Subtitle Support (Partial)
- SRT files converted to WebVTT format
- VTT files served directly
- CORS headers added for Chromecast compatibility
- `textTrackStyle` added to LOAD command for styling
- **Limitation**: Default Media Receiver on Chromecast with Google TV ignores side-loaded subtitle tracks

## Key Files Modified

| File | Changes |
|------|---------|
| `internal/gui/actions.go` | Chromecast play/pause/stop logic, status watcher |
| `internal/gui/main.go` | Chromecast seek in slider handlers |
| `internal/gui/gui.go` | Device state reset, button updates |
| `castprotocol/client.go` | Load with subtitles, status polling with Update() |
| `castprotocol/loader.go` | TextTrackStyle struct, CustomLoadPayload |
| `castprotocol/media.go` | MediaTrack struct for subtitle tracks |
| `httphandlers/httphandlers.go` | CORS headers for subtitle files |
| `devices/chromecast_discovery.go` | Faster discovery (1s timeout) |

## Known Issues

### Subtitle Display on Google TV
The Default Media Receiver (`CC1AD845`) on Chromecast with Google TV devices does not display side-loaded subtitle tracks. This is a known limitation of the receiver app.

**Workaround**: Use "Transcode" mode to burn subtitles into video stream via FFmpeg.

**Works on**: Original Chromecast dongle, Chromecast Ultra

## Code Patterns

### Status Watcher Pattern
```go
func chromecastStatusWatcher(ctx context.Context, screen *FyneScreen) {
    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()
    
    var mediaStarted bool
    for {
        select {
        case <-ctx.Done(): return
        case <-ticker.C:
            status, _ := screen.chromecastClient.GetStatus()
            // Update UI based on status.PlayerState
            // Detect completion when CurrentTime >= Duration - 1.5
        }
    }
}
```

### Device-Specific Branching
```go
// In playAction - branch early for Chromecast
if screen.selectedDeviceType == devices.DeviceTypeChromecast {
    chromecastPlayAction(screen)
    return
}
// DLNA code follows...
```

## Testing Notes

1. **Play/Pause/Stop** - Verified working
2. **Seek** - Both tap and drag work
3. **Progress bar** - Updates correctly during playback
4. **Subtitles** - WebVTT served with CORS, but not displayed on Google TV
5. **Discovery** - Reduced to 1s timeout for faster device detection

## Next Steps

- Phase 4: CLI mode support for Chromecast
- Phase 5: Protocol interface unification (optional)
