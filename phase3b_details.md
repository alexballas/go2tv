# Phase 3b: Device State Management - Implementation Details

**Status**: ✅ Completed

## Summary

Added device state management to prevent DLNA/Chromecast state leakage when switching devices.

## Files Modified

### [internal/gui/gui.go](file:///home/alex/test/go2tv/internal/gui/gui.go)

**1. Added `chromecastClient` field to FyneScreen** (line 63):
```go
chromecastClient *castprotocol.CastClient // Active Chromecast connection
```

**2. Added `resetDeviceState()` function** (lines 409-437):
```go
func (p *FyneScreen) resetDeviceState() {
    // Clear DLNA-specific state
    p.controlURL = ""
    p.eventlURL = ""
    p.renderingControlURL = ""
    p.connectionManagerURL = ""
    p.tvdata = nil

    // Close any active Chromecast connection
    if p.chromecastClient != nil {
        p.chromecastClient.Close(true)
        p.chromecastClient = nil
    }

    // Reset playback state
    p.State = ""
    p.ffmpegSeek = 0

    // Reset UI state
    fyne.Do(func() {
        p.CurrentPos.Set("00:00:00")
        p.EndPos.Set("00:00:00")
        p.SlideBar.Slider.SetValue(0)
        setPlayPauseView("Play", p)
    })
}
```

---

### [internal/gui/main.go](file:///home/alex/test/go2tv/internal/gui/main.go)

**Updated device selection callback** (lines 426-451):
```go
list.OnSelected = func(id widget.ListItemID) {
    playpause.Enable()

    // CRITICAL: Reset state when switching between devices
    if s.selectedDevice.addr != "" && s.selectedDevice.addr != data[id].addr {
        s.resetDeviceState()
    }

    s.selectedDevice = data[id]
    s.selectedDeviceType = data[id].deviceType
    // ... DLNA DMR extraction continues
}
```

---

## Design Decisions

**What gets reset:**
- DLNA URLs (control, event, rendering, connectionManager)
- `tvdata` reference
- Chromecast client (connection closed)
- Playback state and ffmpegSeek
- UI elements (slider, labels, play button)

**What does NOT get reset:**
- HTTP server (only stopped via stop button or device signal)
- Media file/subtitle selections
- User preferences (transcode, gapless, etc.)

## Verification

- `make build` ✅
- `make test` ✅
- Manual: Switch between devices, verify UI resets ✅
