# Phase 3C: GUI Playback Controls Implementation

## Summary

This phase implemented Chromecast playback controls in the GUI, including play/pause/stop, seek, progress tracking, and subtitle support (external and embedded).

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
- Detects media completion when `CurrentTime >= Duration - 1.5s`

### ✅ Subtitle Support
- **External SRT/VTT**: Served via HTTP with CORS headers
- **Embedded MKV subs**: Extracted via ffmpeg to temp SRT, converted to WebVTT
- `LaunchDefaultReceiver()` → single `LoadWithSubtitles()` prevents double playback
- **Note**: Default Media Receiver on Google TV may ignore side-loaded tracks

## Key Files Modified

| File | Changes |
|------|---------|
| `internal/gui/actions.go` | Chromecast play/pause/stop, status watcher, embedded sub extraction |
| `internal/gui/main.go` | Chromecast seek in slider handlers |
| `castprotocol/client.go` | Load with subtitles, LaunchDefaultReceiver integration |
| `castprotocol/loader.go` | LaunchDefaultReceiver, TextTrackStyle, CustomLoadPayload |
| `castprotocol/media.go` | MediaTrack, MediaItemWithTracks with TextTrackStyle |
| `httphandlers/httphandlers.go` | CORS headers for subtitle files |
| `devices/chromecast_discovery.go` | Faster discovery (1s timeout) |

## Key Implementation Details

### LaunchDefaultReceiver Pattern
To avoid double playback when loading with subtitles:
```go
// In client.go Load() - when subtitleURL is present:
LaunchDefaultReceiver(c.conn)  // Launch app without media
time.Sleep(2 * time.Second)
c.app.Update()                  // Get transport ID
LoadWithSubtitles(...)          // Single LOAD with tracks
```

### Embedded Subtitle Extraction
```go
// In chromecastPlayAction - before Chromecast client init:
if screen.SelectInternalSubs.Selected != "" {
    tempSubsPath, _ := utils.ExtractSub(ffmpegPath, n, mediafile)
    screen.subsfile = tempSubsPath  // Used by subtitle handler
}
```

## Testing Notes

1. **Play/Pause/Stop** - Verified working
2. **Seek** - Both tap and drag work
3. **Progress bar** - Updates correctly during playback
4. **External subtitles (.srt/.vtt)** - Working
5. **Embedded MKV subtitles** - Extraction via ffmpeg working
6. **Subtitle display on Google TV** - Receiver limitation, may not display

## Next Steps

- Phase 3d: Volume/Mute controls
- Phase 3e: CLI mode support for Chromecast
