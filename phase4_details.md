# Phase 4: Media Transcoding for Chromecast - Implementation Details

**Status**: 🔄 In Progress

## Overview

Phase 4 implements Chromecast-specific transcoding with conditional FFmpeg usage. Media is analyzed using ffprobe to determine compatibility - compatible formats stream directly, incompatible formats are transcoded to H.264/AAC/MP4.

### Sub-Phases

| Phase | Focus | Tasks |
|-------|-------|-------|
| **4a** | Core Infrastructure | TranscodeOptions struct, `ServeChromecastTranscodedStream()`, HTTP handler changes |
| **4b** | Integration | GUI auto-detect, play action, seek support, CLI fixes, error handling |

Phase 4a can be tested independently before wiring up the UI/CLI integration points in 4b.

> [!IMPORTANT]
> This is Phase 4 of a 5-phase project. See [chromecast_v2_roadmap.md](file:///home/alex/test/go2tv/chromecast_v2_roadmap.md) for complete roadmap.

> [!NOTE]
> **Pre-existing Code**: `GetMediaCodecInfo()` and `IsChromecastCompatible()` already exist in `soapcalls/utils/ffprobe.go`. Phase 4 focuses on integration and transcoding pipeline.

> [!NOTE]
> **Prerequisite Fix Applied**: `ServeTranscodedStream()` now accepts `context.Context` as first parameter and uses `exec.CommandContext()`. This ensures FFmpeg is killed when HTTP request is cancelled (client disconnect). Chromecast transcoding must follow the same pattern.

---

## Chromecast Media Format Specifications

### Supported Formats (Direct Streaming)

| Category | Supported Codecs |
|----------|------------------|
| **Video** | H.264 (High Profile), HEVC/H.265, VP8, VP9, AV1 |
| **Audio** | AAC, MP3, Vorbis, Opus, FLAC |
| **Container** | MP4, WebM, MKV/Matroska |

### Transcoding Output Format

When transcoding is required:
- **Container**: Fragmented MP4 (streaming-friendly)
- **Video**: H.264 High Profile Level 4.1
- **Audio**: AAC stereo 192kbps @ 48kHz
- **Resolution**: Max 1920x1080, preserve aspect ratio

---

## Architecture Notes

### Chromecast vs DLNA HTTP Serving

| Aspect | DLNA | Chromecast |
|--------|------|------------|
| Server Start | `StartServer()` | `StartSimpleServer()` |
| Uses TVPayload | Yes (full SOAP URLs, callbacks) | No (`nil` passed to AddHandler) |
| Callback Handler | Yes (DLNA events) | No |
| Transcoding | Via TVPayload fields | **Needs TranscodeOptions struct** |

**Key insight**: Chromecast uses `StartSimpleServer()` which passes `nil` as TVPayload. We need a separate `TranscodeOptions` struct for Chromecast transcoding configuration.

### CLI Issues to Fix

**Issue 1**: `flagResults.dmrURL` uses DLNA-specific terminology ("DMR" = Digital Media Renderer). Should be renamed to `targetURL` since it holds either DLNA or Chromecast addresses.

**Issue 2**: The `-tc` flag is not passed to `runChromecastCLI()` (line 147):
```go
// Current (broken):
return runChromecastCLI(exitCTX, cancel, flagRes.dmrURL, absMediaFile, mediaType, absSubtitlesFile)
```
The transcoding flag is completely ignored for Chromecast CLI mode.

### Subtitle Handling Behavior

| Scenario | When Processed | Action |
|----------|----------------|--------|
| External SRT file | User selects file | Convert to WebVTT, side-load |
| External VTT file | User selects file | Serve directly, side-load |
| Embedded MKV subtitle | **User selects from dropdown** | Extract → convert to WebVTT OR burn via FFmpeg |
| No subtitle selected | - | No processing |

**Important**: Embedded subtitles are only processed when user explicitly selects them from the dropdown (`SelectInternalSubs.Selected != ""`).

---

## Proposed Changes

---

## Phase 4a: Core Infrastructure

---

### Task 4.1: Verify Existing Media Detection ✅

**File**: [soapcalls/utils/ffprobe.go](file:///home/alex/test/go2tv/soapcalls/utils/ffprobe.go)

Already implemented:
- `GetMediaCodecInfo(ffmpegPath, mediaPath string) (*MediaCodecInfo, error)`
- `IsChromecastCompatible(info *MediaCodecInfo) bool`

**Verification needed**: Run manual tests to confirm detection accuracy.

---

### Task 4.2: Create TranscodeOptions Struct

#### [NEW] [soapcalls/utils/transcode_options.go](file:///home/alex/test/go2tv/soapcalls/utils/transcode_options.go)

Create a lightweight struct for transcoding configuration (separate from TVPayload):

```go
package utils

// TranscodeOptions holds FFmpeg transcoding configuration for Chromecast.
// Used by StartSimpleServerWithTranscode() which doesn't use TVPayload.
//
// Field descriptions:
//
//   FFmpegPath: Absolute path to the ffmpeg binary executable.
//               Example: "/usr/bin/ffmpeg" or "C:\ffmpeg\bin\ffmpeg.exe"
//               Used to spawn the ffmpeg process for transcoding.
//
//   SubsPath: Path to subtitle file to burn into the video stream.
//             Supports SRT and VTT formats. When set, subtitles are
//             embedded via ffmpeg's -vf subtitles filter.
//             Empty string means no subtitle burning.
//             Only used when user explicitly selects subtitles.
//
//   SeekSeconds: Starting position in seconds for transcoding.
//                Used with ffmpeg's -ss flag for seeking.
//                Value of 0 starts from the beginning.
//                Enables seek support during transcoded playback.
//
//   SubtitleSize: Font size for burned-in subtitles.
//                 Use SubtitleSizeSmall (20), SubtitleSizeMedium (24),
//                 or SubtitleSizeLarge (30). Ignored if SubsPath is empty.
//
//   LogOutput: io.Writer for debug logging (same pattern as TVPayload).
//              Pass screen.Debug to enable export from settings menu.
//              Pass nil to disable logging.
//
type TranscodeOptions struct {
    FFmpegPath   string
    SubsPath     string
    SeekSeconds  int
    SubtitleSize int
    LogOutput    io.Writer

    initLogOnce sync.Once
    logger      zerolog.Logger
}

// LogError logs an error using the same pattern as TVPayload.Log().
// Does nothing if LogOutput is nil.
func (t *TranscodeOptions) LogError(function, action string, err error) {
    if t.LogOutput == nil {
        return
    }
    t.initLogOnce.Do(func() {
        t.logger = zerolog.New(t.LogOutput).With().Timestamp().Logger()
    })
    t.logger.Error().Str("function", function).Str("Action", action).Err(err).Msg("")
}
```

---

### Task 4.3: Chromecast FFmpeg Command Builder

#### [NEW] [soapcalls/utils/chromecast_transcode.go](file:///home/alex/test/go2tv/soapcalls/utils/chromecast_transcode.go)

Create Chromecast-specific transcoding function following the same pattern as `ServeTranscodedStream`:

```go
// ServeChromecastTranscodedStream transcodes media to Chromecast-compatible format.
// Output: fragmented MP4 with H.264 video and AAC audio for HTTP streaming.
// The context is used to kill ffmpeg when the HTTP request is cancelled.
//
// Parameters:
//   - ctx: Context for cancellation (pass r.Context() from HTTP handler)
//   - w: HTTP response writer to stream transcoded output
//   - input: Media source - either string (filepath) or io.Reader
//   - ff: Pointer to exec.Cmd for FFmpeg process management (cleanup)
//   - opts: TranscodeOptions containing FFmpeg path, subtitles, seek position, and logger
func ServeChromecastTranscodedStream(
    ctx context.Context,
    w http.ResponseWriter,
    input any,
    ff *exec.Cmd,
    opts *TranscodeOptions,
) error
```

**FFmpeg Command Template**:
```bash
ffmpeg -re \
  -ss <seekSeconds> \
  -copyts \
  -i <input> \
  -c:v libx264 -profile:v high -level 4.1 \
  -preset fast -crf 23 \
  -maxrate 10M -bufsize 20M \
  -vf "scale='min(1920,iw)':'min(1080,ih)':force_original_aspect_ratio=decrease,format=yuv420p" \
  -c:a aac -b:a 192k -ar 48000 -ac 2 \
  -movflags +frag_keyframe+empty_moov+default_base_moof \
  -f mp4 pipe:1
```

**With subtitle burning** (when subtitlePath provided):
```bash
-vf "subtitles='<path>':force_style='FontSize=24',scale=...,format=yuv420p"
```

**Key differences from DLNA transcoding**:

| Aspect | DLNA | Chromecast |
|--------|------|------------|
| Container | MPEGTS (`-f mpegts`) | Fragmented MP4 (`-f mp4 -movflags frag...`) |
| Preset | `ultrafast` | `fast` (better quality) |
| Bitrate | Unbounded | `maxrate 10M` (network-friendly) |
| Tune | `zerolatency` | None (quality over latency) |

#### [NEW] [soapcalls/utils/chromecast_transcode_windows.go](file:///home/alex/test/go2tv/soapcalls/utils/chromecast_transcode_windows.go)

Windows stub (transcoding not supported):

```go
//go:build windows

package utils

func ServeChromecastTranscodedStream(...) error {
    return errors.New("chromecast transcoding not supported on Windows")
}
```

---

### Task 4.4: Extend HTTP Handler for Chromecast Transcoding

#### [MODIFY] [httphandlers/httphandlers.go](file:///home/alex/test/go2tv/httphandlers/httphandlers.go)

**Add TranscodeOptions to handler storage**:

```go
type handler struct {
    payload   *soapcalls.TVPayload    // For DLNA (may be nil for Chromecast)
    transcode *utils.TranscodeOptions // For Chromecast transcoding (may be nil)
    media     any
}
```

**Update AddHandler signature**:

```go
// AddHandler registers a handler for the given path.
// For DLNA: pass payload, transcode=nil
// For Chromecast with transcoding: pass payload=nil, transcode options
// For Chromecast without transcoding: pass both as nil
func (s *HTTPserver) AddHandler(path string, payload *soapcalls.TVPayload, transcode *utils.TranscodeOptions, media any)
```

**Update serveContentCustomType** to handle Chromecast transcoding:

```go
func serveContentCustomType(w http.ResponseWriter, r *http.Request,
    tv *soapcalls.TVPayload,
    tcOpts *utils.TranscodeOptions,
    mediaType string, transcode, seek bool, f osFileType, ff *exec.Cmd) {

    // ... existing DLNA header handling ...

    if transcode && r.Method == http.MethodGet && strings.Contains(mediaType, "video") {
        var input any = f.file
        if f.path != "" {
            input = f.path
        }

        // Route based on which config is provided
        // Note: r.Context() is passed to kill ffmpeg when client disconnects
        switch {
        case tcOpts != nil:
            // Chromecast transcoding (fragmented MP4)
            w.Header().Set("Content-Type", "video/mp4")
            w.Header().Set("Access-Control-Allow-Origin", "*")
            err := utils.ServeChromecastTranscodedStream(r.Context(), w, input, ff, tcOpts)
            if err != nil {
                tcOpts.LogError("serveContentCustomType", "ChromecastTranscode", err)
            }
        case tv != nil:
            // DLNA transcoding (MPEGTS) - uses r.Context() as of this fix
            err := utils.ServeTranscodedStream(r.Context(), w, input, ff,
                tv.FFmpegPath, tv.FFmpegSubsPath, tv.FFmpegSeek, utils.SubtitleSizeMedium)
            if err != nil {
                tv.Log().Error().Str("function", "serveContentCustomType").Err(err).Msg("")
            }
        }
        return
    }

    // ... rest of existing code ...
}
```

---

### Task 4.5: Update StartSimpleServer for Transcoding

#### [MODIFY] [httphandlers/httphandlers.go](file:///home/alex/test/go2tv/httphandlers/httphandlers.go)

Add new method for Chromecast with transcoding support:

```go
// StartSimpleServerWithTranscode starts HTTP server with optional transcoding.
// Used by Chromecast when media needs transcoding.
// Pass tcOpts=nil for direct streaming (no transcoding).
func (s *HTTPserver) StartSimpleServerWithTranscode(
    serverStarted chan<- error,
    mediaPath string,
    tcOpts *utils.TranscodeOptions,
) {
    mediaFilename := "/" + utils.ConvertFilename(mediaPath)
    s.AddHandler(mediaFilename, nil, tcOpts, mediaPath)

    s.Mux.HandleFunc("/", s.ServeMediaHandler())

    ln, err := net.Listen("tcp", s.http.Addr)
    if err != nil {
        serverStarted <- fmt.Errorf("server listen error: %w", err)
        return
    }

    serverStarted <- nil
    _ = s.http.Serve(ln)
}
```

Update existing `StartSimpleServer()` to call new method with `nil` options:

```go
func (s *HTTPserver) StartSimpleServer(serverStarted chan<- error, mediaPath string) {
    s.StartSimpleServerWithTranscode(serverStarted, mediaPath, nil)
}
```

---

## Phase 4b: Integration

---

### Task 4.6: Auto-Enable Transcode for Incompatible Media (GUI)

#### [MODIFY] [gui.go](file:///home/alex/test/go2tv/internal/gui/gui.go)

Add helper method to FyneScreen:

```go
// checkChromecastCompatibility checks if loaded media needs transcoding for Chromecast.
// Auto-enables transcode checkbox if media is incompatible and FFmpeg is available.
func (s *FyneScreen) checkChromecastCompatibility() {
    if s.selectedDeviceType != devices.DeviceTypeChromecast {
        return
    }
    if s.mediafile == "" {
        return
    }
    if err := utils.CheckFFmpeg(s.ffmpegPath); err != nil {
        return // Can't transcode anyway
    }

    info, err := utils.GetMediaCodecInfo(s.ffmpegPath, s.mediafile)
    if err != nil {
        return // Can't determine, let user decide
    }

    if !utils.IsChromecastCompatible(info) {
        s.TranscodeCheckBox.SetChecked(true)
        s.Transcode = true
    }
}
```

#### [MODIFY] [actions.go](file:///home/alex/test/go2tv/internal/gui/actions.go)

Update `selectMediaFile()` to check compatibility after selecting a file:

```go
// After setting screen.mediafile:
if screen.selectedDeviceType == devices.DeviceTypeChromecast {
    screen.checkChromecastCompatibility()
}
```

#### [MODIFY] [main.go](file:///home/alex/test/go2tv/internal/gui/main.go)

Update device selection callback to check compatibility when switching to Chromecast:

```go
// In list.OnSelected callback, after setting selectedDeviceType:
if data[id].deviceType == devices.DeviceTypeChromecast && s.mediafile != "" {
    s.checkChromecastCompatibility()
}
```

---

### Task 4.7: Update chromecastPlayAction for Transcoding (GUI)

#### [MODIFY] [actions.go](file:///home/alex/test/go2tv/internal/gui/actions.go)

Update `chromecastPlayAction()` to support transcoding:

```go
func chromecastPlayAction(screen *FyneScreen) {
    // ... existing pause/resume handling ...

    transcode := screen.Transcode

    // Handle subtitle preparation
    var subsPath string

    // Only process embedded subtitles if user selected from dropdown
    if screen.SelectInternalSubs.Selected != "" {
        // ... existing extraction logic (lines 512-529) ...
        subsPath = screen.subsfile
    } else if screen.subsfile != "" {
        // External subtitle file selected
        subsPath = screen.subsfile
    }

    // ... existing client connection code ...

    // LOCAL FILE: Serve via internal HTTP server
    if !screen.ExternalMediaURL.Checked {
        // ... existing file opening and type detection ...

        screen.httpserver = httphandlers.NewServer(whereToListen)
        serverStarted := make(chan error)

        var tcOpts *utils.TranscodeOptions
        if transcode {
            tcOpts = &utils.TranscodeOptions{
                FFmpegPath:   screen.ffmpegPath,
                SubsPath:     subsPath,  // Burns subtitles if transcoding
                SeekSeconds:  screen.ffmpegSeek,
                SubtitleSize: utils.SubtitleSizeMedium,
                LogOutput:    screen.Debug,
            }
            // Update content type for transcoded output
            mediaType = "video/mp4"
        }

        go func() {
            screen.httpserver.StartSimpleServerWithTranscode(serverStarted, screen.mediafile, tcOpts)
            serverCTXStop()
        }()

        // ... rest of existing code ...
    }

    // Handle subtitles for non-transcoding case (WebVTT side-loading)
    var subtitleURL string
    if subsPath != "" && !transcode && screen.httpserver != nil {
        // ... existing WebVTT conversion and serving code (lines 628-646) ...
    }

    // ... Load media ...
}
```

**Subtitle handling logic**:
- **Transcode=true**: Burn subtitles into video via FFmpeg `-vf subtitles=` filter
- **Transcode=false**: Convert to WebVTT and side-load to Chromecast

---

### Task 4.8: Chromecast Transcoded Seek Support

#### [MODIFY] [main.go](file:///home/alex/test/go2tv/internal/gui/main.go)

Update `DragEnd()` and `Tapped()` slider handlers to support transcoded seek for Chromecast.

**Current behavior** (non-transcoded only):
```go
if t.screen.chromecastClient != nil && t.screen.chromecastClient.IsConnected() {
    seekPos := int((t.screen.SlideBar.Value / t.screen.SlideBar.Max) * float64(status.Duration))
    if err := t.screen.chromecastClient.Seek(seekPos); err != nil {
        return
    }
    return
}
```

**Updated behavior** (handles both transcoded and non-transcoded):
```go
if t.screen.chromecastClient != nil && t.screen.chromecastClient.IsConnected() {
    status, err := t.screen.chromecastClient.GetStatus()
    if err != nil || status.Duration <= 0 {
        return
    }

    seekPos := int((t.screen.SlideBar.Value / t.screen.SlideBar.Max) * float64(status.Duration))

    // Transcoded seek: stop and restart with new position
    // (Chromecast's native Seek() doesn't work on transcoded streams)
    if t.screen.Transcode {
        t.screen.ffmpegSeek = seekPos
        stopAction(t.screen)
        playAction(t.screen)
        return
    }

    // Non-transcoded seek: use Chromecast's native seek
    if err := t.screen.chromecastClient.Seek(seekPos); err != nil {
        return
    }
    return
}
```

**How it works**:
1. `screen.ffmpegSeek` is set to the new seek position
2. `stopAction()` stops the HTTP server and FFmpeg process
3. `playAction()` → `chromecastPlayAction()` creates new `TranscodeOptions` with `SeekSeconds: screen.ffmpegSeek`
4. FFmpeg restarts with `-ss <seekPos>`, Chromecast loads from new position

**Same pattern as DLNA transcoded seek** (lines 169-173 in main.go):
```go
if t.screen.tvdata.Transcode {
    t.screen.ffmpegSeek = roundedInt
    stopAction(t.screen)
    playAction(t.screen)
}
```

---

### Task 4.9: Fix CLI Naming and Add Transcoding Support

#### [MODIFY] [go2tv.go](file:///home/alex/test/go2tv/cmd/go2tv/go2tv.go)

**Rename `dmrURL` to `targetURL`** in flagResults struct (line 44-48):

```go
// Before:
type flagResults struct {
    dmrURL string
    exit   bool
    gui    bool
}

// After:
type flagResults struct {
    targetURL string  // Device URL (DLNA renderer or Chromecast)
    exit      bool
    gui       bool
}
```

**Update all references** to `flagRes.dmrURL` → `flagRes.targetURL`:
- Line 146: `devices.IsChromecastURL(flagRes.targetURL)`
- Line 147: `runChromecastCLI(..., flagRes.targetURL, ...)`
- Line 156: `DMR: flagRes.targetURL`
- Line 435: `res.targetURL = *targetPtr`

**Update runChromecastCLI signature** to accept transcoding parameters (line 193):

```go
// Before:
func runChromecastCLI(ctx context.Context, cancel context.CancelFunc,
    deviceURL, mediaPath, mediaType, subsPath string) error

// After:
func runChromecastCLI(ctx context.Context, cancel context.CancelFunc,
    deviceURL, mediaPath, mediaType, subsPath, ffmpegPath string, transcode bool) error
```

**Update the call site** (line 146-148):

```go
// Before:
if devices.IsChromecastURL(flagRes.dmrURL) {
    return runChromecastCLI(exitCTX, cancel, flagRes.dmrURL, absMediaFile, mediaType, absSubtitlesFile)
}

// After:
if devices.IsChromecastURL(flagRes.targetURL) {
    return runChromecastCLI(exitCTX, cancel, flagRes.targetURL, absMediaFile, mediaType, absSubtitlesFile, ffmpegPath, *transcodePtr)
}
```

**Update runChromecastCLI implementation**:

```go
func runChromecastCLI(ctx context.Context, cancel context.CancelFunc,
    deviceURL, mediaPath, mediaType, subsPath, ffmpegPath string, transcode bool) error {

    // Auto-enable transcoding if media is incompatible and ffmpeg available
    if !transcode && ffmpegPath != "" {
        info, err := utils.GetMediaCodecInfo(ffmpegPath, mediaPath)
        switch {
        case err != nil:
            // Can't determine compatibility, proceed without transcoding
        case !utils.IsChromecastCompatible(info):
            if utils.CheckFFmpeg(ffmpegPath) == nil {
                transcode = true
                fmt.Println("Media format incompatible with Chromecast, transcoding enabled")
            } else {
                fmt.Println("Warning: Media may not play (incompatible format, ffmpeg not available)")
            }
        }
    }

    // ... existing client connection code ...

    // Start HTTP server with transcoding if needed
    httpServer := httphandlers.NewServer(whereToListen)
    serverStarted := make(chan error)

    var tcOpts *utils.TranscodeOptions
    if transcode {
        tcOpts = &utils.TranscodeOptions{
            FFmpegPath:   ffmpegPath,
            SubsPath:     subsPath,
            SeekSeconds:  0,
            SubtitleSize: utils.SubtitleSizeMedium,
            LogOutput:    nil,  // CLI uses stdout for messages
        }
        mediaType = "video/mp4"
    }

    go func() {
        httpServer.StartSimpleServerWithTranscode(serverStarted, mediaPath, tcOpts)
    }()

    // ... rest of existing code ...
}
```

---

### Task 4.10: Error Handling

Handle edge cases:

1. **FFmpeg unavailable + incompatible media**:
   - Show warning: "Media format may not be compatible with Chromecast. Install FFmpeg to enable transcoding."
   - Allow playback attempt (might work for some formats)

2. **Transcode fails mid-stream**:
   - Log error, stop playback gracefully
   - Show user-friendly error message

---

## File Changes Summary

### Phase 4a: New Files

| File | Purpose |
|------|---------|
| `soapcalls/utils/transcode_options.go` | TranscodeOptions struct with field documentation |
| `soapcalls/utils/chromecast_transcode.go` | Chromecast FFmpeg transcoding |
| `soapcalls/utils/chromecast_transcode_windows.go` | Windows stub |

### Phase 4a: Modified Files

| File | Changes |
|------|---------|
| `httphandlers/httphandlers.go` | Add TranscodeOptions to handler, `StartSimpleServerWithTranscode()`, update `serveContentCustomType()` with switch |

### Phase 4b: Modified Files

| File | Changes |
|------|---------|
| `internal/gui/gui.go` | Add `checkChromecastCompatibility()` |
| `internal/gui/actions.go` | Update `chromecastPlayAction()` for transcoding, check on file select |
| `internal/gui/main.go` | Check compatibility on device switch, transcoded seek (DragEnd/Tapped) |
| `cmd/go2tv/go2tv.go` | Rename `dmrURL` → `targetURL`, fix `runChromecastCLI()` to accept `-tc` flag |

---

## Verification Plan

### Manual Tests

#### Test 1: CLI Transcoding with -tc Flag
```bash
./build/go2tv -v video.avi -tc -t http://192.168.1.100:8009
```
1. Verify transcoding activates (FFmpeg process running)
2. Verify video plays on Chromecast

#### Test 2: CLI Auto-Transcode Detection
```bash
./build/go2tv -v incompatible.avi -t http://192.168.1.100:8009
```
1. Verify message: "Media format incompatible with Chromecast, transcoding enabled"
2. Verify automatic transcoding works

#### Test 3: GUI Compatible Media Direct Streaming
1. Select Chromecast, select MP4/H.264/AAC file
2. Verify transcode NOT auto-checked
3. Play → verify direct streaming (no FFmpeg process)

#### Test 4: GUI Incompatible Media Auto-Transcode
1. Select Chromecast, select AVI/MPEG4 file (FFmpeg installed)
2. Verify transcode IS auto-checked
3. Play → verify transcoding works (FFmpeg running)

#### Test 5: Embedded Subtitles (User Selected)
1. Select Chromecast, select MKV with embedded subs
2. Select subtitle from dropdown
3. With transcode=true: verify subs burned into video
4. With transcode=false: verify subs extracted and served as WebVTT

#### Test 6: No Subtitle Selected
1. Select Chromecast, select MKV with embedded subs
2. Do NOT select from subtitle dropdown
3. Play → verify no subtitle processing occurs

#### Test 7: DLNA Not Affected
1. Select DLNA device, play any media with -tc flag
2. Verify existing DLNA transcoding unchanged

### Build Verification

```bash
make build  # Clean build
make test   # All tests pass
```

---

## Implementation Notes

### Why Not Use TVPayload for Chromecast?

TVPayload is DLNA-specific and contains:
- SOAP control/event/rendering URLs
- Callback URL for DLNA events
- Subscription management fields
- DLNA-specific logging

Chromecast uses Cast v2 protocol via `castprotocol.CastClient` - none of these apply. Using TVPayload would be conceptually incorrect and could cause confusion.

### Seek Support with Transcoding

Same pattern as DLNA (see Task 4.8 for implementation):
1. User seeks to position X
2. Set `screen.ffmpegSeek = X`
3. `stopAction()` stops server and FFmpeg process
4. `playAction()` restarts with new `TranscodeOptions.SeekSeconds`
5. Chromecast loads media from new position

**Note**: Chromecast's native `Seek()` only works for direct streaming. Transcoded streams require stop/restart.

---

## Dependencies

- Phase 3 complete (Chromecast playback working)
- FFmpeg installed for transcoding features
- Existing `GetMediaCodecInfo()` and `IsChromecastCompatible()` functions

## Deliverables

### Phase 4a: Core Infrastructure

- [ ] `TranscodeOptions` struct with documented fields (including LogOutput + LogError helper)
- [ ] `ServeChromecastTranscodedStream()` function (fragmented MP4 output)
- [ ] `StartSimpleServerWithTranscode()` HTTP server method
- [ ] HTTP handler routing for Chromecast vs DLNA transcoding

### Phase 4b: Integration

- [ ] Auto-enable transcode for incompatible media (GUI)
- [ ] Update `chromecastPlayAction()` for transcoding support
- [ ] Chromecast transcoded seek support (stop/restart pattern)
- [ ] Subtitle handling: burn (transcode) or WebVTT (direct)
- [ ] Rename `flagResults.dmrURL` → `targetURL` (fix DLNA-specific naming)
- [ ] CLI `-tc` flag support for Chromecast (currently ignored)
- [ ] CLI auto-detect and enable transcoding for incompatible media
- [ ] Error handling with structured logging

### Verification

- [ ] All existing DLNA functionality unchanged
