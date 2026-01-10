# Phase 4b: Chromecast Transcoding Integration

**Status**: ✅ Complete

## Summary

Wired Phase 4a transcoding infrastructure to GUI and CLI for complete Chromecast transcoding support.

## New/Modified Files

### GUI Integration
| File | Changes |
|------|---------|
| `internal/gui/gui.go` | Added `checkChromecastCompatibility()` helper |
| `internal/gui/actions.go` | Updated `selectMediaFile()`, `chromecastPlayAction()` with transcoding |
| `internal/gui/main.go` | Device selection callback, transcoded seek in `DragEnd()`/`Tapped()` |

### CLI Integration
| File | Changes |
|------|---------|
| `cmd/go2tv/go2tv.go` | Renamed `dmrURL`→`targetURL`, updated `runChromecastCLI()` with transcoding |

### Bug Fixes
| File | Changes |
|------|---------|
| `soapcalls/utils/transcode.go` | Fixed nil pointer crash (exec.CommandContext issue) |
| `soapcalls/utils/transcode_windows.go` | Same fix for Windows |
| `soapcalls/utils/chromecast_transcode.go` | Same fix |
| `castprotocol/client.go` | Use `WithConnectionRetries(3)` for robustness |

## Key Features

1. **Auto-detect Chromecast compatibility** - Transcode checkbox auto-enabled for incompatible media
2. **Transcoded playback** - Uses fragmented MP4 via FFmpeg
3. **Transcoded seek** - Stop/restart pattern (same as DLNA)
4. **Subtitle handling** - Burns when transcoding, side-loads as WebVTT otherwise
5. **CLI `-tc` flag** - Now works for Chromecast
6. **Auto-detect CLI** - Prints message and enables transcoding for incompatible media

## Bug Fix Details

**Nil pointer crash in transcoding**: `exec.CommandContext` sets up a context watcher immediately that can try to kill a nil process if context cancels before `cmd.Run()`. Fixed by using `exec.Command` with manual context handling after `ff.Start()`.

## Verification

- ✅ `make build` passes
- ✅ `make test` passes
