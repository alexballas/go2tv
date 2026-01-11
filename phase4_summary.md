# Phase 4: Chromecast Transcoding - Complete Summary

**Date**: 2026-01-11  
**Status**: ✅ Complete (optimization pending)

---

## Phase 4a: Core Infrastructure

Added transcoding infrastructure for Chromecast.

| New File | Purpose |
|----------|---------|
| `soapcalls/utils/transcode_options.go` | `TranscodeOptions` struct |
| `soapcalls/utils/chromecast_transcode.go` | `ServeChromecastTranscodedStream()` - fragmented MP4 |
| `soapcalls/utils/chromecast_transcode_windows.go` | Windows stub |

**Key changes:**
- Fragmented MP4 output (`-movflags +frag_keyframe+empty_moov+default_base_moof`)
- H.264 High Profile 4.1, AAC 192kbps @ 48kHz
- `AddHandler(path, payload, transcode, media)` - 4 params
- `StartSimpleServerWithTranscode(serverStarted, mediaPath, tcOpts)`

---

## Phase 4b: GUI/CLI Integration

Wired transcoding to GUI and CLI.

| File | Changes |
|------|---------|
| `internal/gui/gui.go` | `checkChromecastCompatibility()` helper |
| `internal/gui/actions.go` | `chromecastPlayAction()`, `chromecastTranscodedSeek()` |
| `internal/gui/main.go` | Device selection, transcoded seek in slider |
| `cmd/go2tv/go2tv.go` | `-tc` flag, auto-detect for Chromecast |

**Features:**
- Auto-detect incompatible media → enable transcode checkbox
- Transcoded seek via server restart pattern
- Subtitle burn-in (transcoding) or WebVTT side-load (native)
- Duration from ffprobe for accurate progress

---

## Bug Fixes

| Issue | Fix |
|-------|-----|
| Nil pointer in transcoding | Use `exec.Command` + manual context, not `CommandContext` |
| `ineffassign` linter L718 | Removed unused `GetMimeDetailsFromFile` in `chromecastTranscodedSeek` |
| Nil panic in status watcher | Capture `client` once per iteration, avoid race with `stopAction` |
| Mutex contention | Changed to `sync.RWMutex` with `RLock` for `IsConnected()` |
| URL mismatch (Special chars) | Fixed `StartSimpleServerWithTranscode` to use decoded filename (`filepath.Base`) matching incoming request |

---

## Pending Optimization: Progress Bar Latency

**Problem**: `GetStatus()` calls `c.app.Update()` every 1s = slow network round-trip (200-500ms) → jerky slider.

**Solution**: Local time estimation for transcoded streams:

```go
// Track when PLAYING started
playbackStartTime := time.Now()
// Estimate: currentTime = seekOffset + elapsed
elapsed := time.Since(playbackStartTime).Seconds()
currentTime := float64(screen.ffmpegSeek) + elapsed
```

- Update slider locally every 100ms (smooth)
- Poll `GetStatus()` every 3-5s for state changes only
- Use Chromecast time for non-transcoded streams

---

## Next Session TODO

- [ ] Implement local time estimation for transcoded streams
- [ ] Separate fast slider (100ms) from slow status poll (3-5s)
- [ ] Handle pause/resume timing
- [ ] Periodic sync to correct drift
