# Phase 4a: Chromecast Transcoding Core Infrastructure

**Status**: ✅ Complete

## Summary

Phase 4a adds the core infrastructure for Chromecast-specific transcoding without wiring it to the UI/CLI (that's Phase 4b).

## New Files

| File | Purpose |
|------|---------|
| `soapcalls/utils/transcode_options.go` | `TranscodeOptions` struct for Chromecast transcoding config |
| `soapcalls/utils/chromecast_transcode.go` | `ServeChromecastTranscodedStream()` - fragmented MP4 output |
| `soapcalls/utils/chromecast_transcode_windows.go` | Windows stub (not supported) |

## Key Changes

### TranscodeOptions Struct
- `FFmpegPath`, `SubsPath`, `SeekSeconds`, `SubtitleSize`, `LogOutput` fields
- `LogError()` helper with same pattern as `TVPayload`

### ServeChromecastTranscodedStream
- **Output**: Fragmented MP4 (`-movflags +frag_keyframe+empty_moov+default_base_moof`)
- **Preset**: `fast` (vs DLNA's `ultrafast`) for better quality
- **Bitrate**: 10M maxrate (network-friendly)
- **Codec**: H.264 High Profile 4.1, AAC 192kbps @ 48kHz

### HTTP Handler Updates
- New `handler` struct with `transcode *TranscodeOptions` field
- `AddHandler(path, payload, transcode, media)` - 4 params now
- `StartSimpleServerWithTranscode(serverStarted, mediaPath, tcOpts)`
- `serveContentCustomType`/`serveContentReadClose` route Chromecast vs DLNA

### Bonus: Chromecast Connect Retry
- `Connect()` now retries 3x with exponential backoff (1s, 2s)
- Reduces "context deadline exceeded" errors

## Modified Files

- `httphandlers/httphandlers.go` - handler struct, routing logic
- `httphandlers/httphandlers_test.go` - updated test signature
- `internal/gui/actions.go` - AddHandler call sites
- `internal/gui/actions_mobile.go` - AddHandler call sites
- `cmd/go2tv/go2tv.go` - AddHandler call sites
- `castprotocol/client.go` - Connect retry logic

## Verification

- ✅ `make build` passes
- ✅ `make test` passes  
- ✅ Android build passes (`fyne package --os android`)
- ✅ Mobile build passes (`go build -tags mobile`)

## Next: Phase 4b

Wire up transcoding to GUI/CLI:
- `checkChromecastCompatibility()` auto-enable
- `chromecastPlayAction()` transcoding support
- Transcoded seek (stop/restart pattern)
- CLI `-tc` flag for Chromecast
