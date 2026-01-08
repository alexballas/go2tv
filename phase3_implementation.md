# Phase 3: Chromecast Communication Protocol

Implementation plan for Cast v2 protocol for device communication with robust state management.

## User Review Required

> [!IMPORTANT]
> This is Phase 3 of a 5-phase project. Review complete roadmap in [chromecast_v2_roadmap.md](file:///home/alex/test/go2tv/chromecast_v2_roadmap.md) before proceeding.

> [!WARNING]
> **Critical Priority: State Management Safeguards**
> This plan addresses the state leakage issues where DLNA-specific parameters persisted after switching to Chromecast. The solution includes:
> - Explicit device type checking before any protocol-specific operation
> - Complete state reset when device selection changes
> - New `resetDeviceState()` function to clear all protocol-specific fields
> - Clear separation of DLNA and Chromecast control paths

> [!NOTE]
> **Library Choice**: Using `github.com/vishen/go-chromecast` (v0.3.4) for Cast v2 protocol:
> - Mature, actively maintained library
> - Full Cast v2 protocol support (CONNECT, LAUNCH, LOAD, PLAY, PAUSE, STOP, SEEK)
> - Built-in HTTP server for local media streaming
> - Volume and mute control included

> [!NOTE]
> **Protocol Unification Deferred**: Per roadmap 3.4, `DLNARenderer` wrapper and `TVPayload` refactoring deferred to Phase 5. Phase 3 uses type-checking in action functions to keep existing DLNA code untouched.

---

## Proposed Changes

### Dependencies

#### [MODIFY] [go.mod](file:///home/alex/test/go2tv/go.mod)

Add go-chromecast dependency:

```go
require (
    // ... existing dependencies
    github.com/vishen/go-chromecast v0.3.4
)
```

Run `go mod tidy` after adding dependency.

---

### Subtitle Conversion Utility

#### [NEW] soapcalls/utils/srt_to_webvtt.go

SRT to WebVTT format conversion for Chromecast:

```go
package utils

import (
    "bufio"
    "bytes"
    "fmt"
    "io"
    "os"
    "regexp"
    "strings"
)

// ConvertSRTtoWebVTT converts an SRT subtitle file to WebVTT format.
// Returns the WebVTT content as bytes.
func ConvertSRTtoWebVTT(srtPath string) ([]byte, error) {
    file, err := os.Open(srtPath)
    if err != nil {
        return nil, fmt.Errorf("open srt: %w", err)
    }
    defer file.Close()

    return ConvertSRTReaderToWebVTT(file)
}

// ConvertSRTReaderToWebVTT converts SRT content from a reader to WebVTT.
func ConvertSRTReaderToWebVTT(r io.Reader) ([]byte, error) {
    var buf bytes.Buffer

    // WebVTT header
    buf.WriteString("WEBVTT\n\n")

    scanner := bufio.NewScanner(r)
    // SRT uses comma for milliseconds, WebVTT uses dot
    timeRegex := regexp.MustCompile(`(\d{2}:\d{2}:\d{2}),(\d{3})`)

    for scanner.Scan() {
        line := scanner.Text()

        // Convert time format: 00:00:01,234 --> 00:00:04,567
        // To WebVTT format: 00:00:01.234 --> 00:00:04.567
        if strings.Contains(line, " --> ") {
            line = timeRegex.ReplaceAllString(line, "$1.$2")
        }

        buf.WriteString(line)
        buf.WriteString("\n")
    }

    if err := scanner.Err(); err != nil {
        return nil, fmt.Errorf("read srt: %w", err)
    }

    return buf.Bytes(), nil
}
```

---

#### [MODIFY] [httphandlers/httphandlers.go](file:///home/alex/test/go2tv/httphandlers/httphandlers.go)

**Add CORS for subtitle paths** in `serveContentBytes`:

```go
func serveContentBytes(w http.ResponseWriter, r *http.Request, mediaType string, f []byte) {
    // Add CORS for subtitle files (needed for Chromecast)
    if strings.HasSuffix(r.URL.Path, ".vtt") || strings.HasSuffix(r.URL.Path, ".srt") {
        w.Header().Set("Access-Control-Allow-Origin", "*")
    }

    // ... existing code
}
```

**Add StartSimpleServer for Chromecast** (doesn't require TVPayload):

```go
// StartSimpleServer starts a minimal HTTP server for serving media files.
// Used by Chromecast which doesn't need DLNA callback handlers or TVPayload.
func (s *HTTPserver) StartSimpleServer(serverStarted chan<- error, mediaPath string) {
    // Register media handler
    mediaFilename := "/" + utils.ConvertFilename(mediaPath)
    s.AddHandler(mediaFilename, nil, mediaPath)

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

---

### New Package: Cast Protocol ✅ IMPLEMENTED

> [!NOTE]
> Phase 3a is complete. See [phase3a_details.md](file:///home/alex/test/go2tv/phase3a_details.md) for full implementation details.

#### Files Created

| File | Purpose |
|------|---------|
| [castprotocol/client.go](file:///home/alex/test/go2tv/castprotocol/client.go) | CastClient wrapper with custom connection for subtitle track support |
| [castprotocol/status.go](file:///home/alex/test/go2tv/castprotocol/status.go) | CastStatus struct for playback state |
| [castprotocol/media.go](file:///home/alex/test/go2tv/castprotocol/media.go) | MediaTrack types for WebVTT subtitles |
| [castprotocol/loader.go](file:///home/alex/test/go2tv/castprotocol/loader.go) | LoadWithSubtitles using custom LOAD command with tracks |

#### Key API (CastClient)

```go
NewCastClient(deviceAddr string) (*CastClient, error)
Connect() error
Load(mediaURL, contentType string, startTime int, subtitleURL string) error  // Uses custom LOAD with tracks if subtitleURL provided
Play() / Pause() / Stop() error
Seek(seconds int) error
SetVolume(level float32) / SetMuted(muted bool) error
GetStatus() (*CastStatus, error)
Close(stopMedia bool) error
```

#### WebVTT Subtitle Support

When `Load()` is called with a non-empty `subtitleURL`:
1. Gets transportId from `app.App().TransportId`
2. Calls `LoadWithSubtitles()` which builds:
   - `MediaTrack` with `type=TEXT`, `subtype=SUBTITLES`, `trackContentId=subtitleURL`
   - `CustomLoadPayload` with `tracks` array and `activeTrackIds=[1]`
3. Sends via `conn.Send()` to media receiver namespace

#### go-chromecast v0.3.4 API Notes

The actual library differs from the original plan:
- `Load()` signature: `(url, startTime, contentType, transcode, detach, forceDetach)`
- `LoadWithOptions()` does not exist
- `CurrentTime`/`Duration` are `float32` not `float64`

---

### Device Selection State Reset (Critical Safeguard)

#### [MODIFY] [gui.go](file:///home/alex/test/go2tv/internal/gui/gui.go)

Add Chromecast client field and state reset function to FyneScreen:

**Add to FyneScreen struct** (around line 61-70):
```go
// Add after existing fields:
    chromecastClient  *castprotocol.CastClient  // Active Chromecast connection
```

**Add new function** (after line 406):
```go
// resetDeviceState clears all protocol-specific state when switching devices.
// This prevents DLNA parameters from leaking to Chromecast operations.
func (p *FyneScreen) resetDeviceState() {
    // Stop any active playback first
    if p.httpserver != nil {
        p.httpserver.StopServer()
    }

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

#### [MODIFY] [main.go](file:///home/alex/test/go2tv/internal/gui/main.go)

Update device selection callback to use state reset (lines 426-445):

```go
list.OnSelected = func(id widget.ListItemID) {
    playpause.Enable()

    // CRITICAL: Reset all device state before switching
    // This prevents DLNA state from leaking to Chromecast
    if s.selectedDevice.addr != "" && s.selectedDevice.addr != data[id].addr {
        s.resetDeviceState()
    }

    s.selectedDevice = data[id]
    s.selectedDeviceType = data[id].deviceType

    switch data[id].deviceType {
    case devices.DeviceTypeDLNA:
        // DLNA: Extract SOAP URLs
        t, err := soapcalls.DMRextractor(context.Background(), data[id].addr)
        check(s, err)
        if err == nil {
            s.controlURL = t.AvtransportControlURL
            s.eventlURL = t.AvtransportEventSubURL
            s.renderingControlURL = t.RenderingControlURL
            s.connectionManagerURL = t.ConnectionManagerURL
            if s.tvdata != nil {
                s.tvdata.RenderingControlURL = s.renderingControlURL
            }
        }

    case devices.DeviceTypeChromecast:
        // Chromecast: Initialize cast client (connect on play)
        // Don't connect yet - defer until playAction
        // Ensure DLNA fields remain empty
        s.controlURL = ""
        s.eventlURL = ""
        s.renderingControlURL = ""
        s.connectionManagerURL = ""
    }
}
```

---

### CLI Chromecast Support with Interactive Terminal

This section adds Chromecast support to CLI mode with an interactive terminal UI similar to the DLNA implementation in `internal/interactive/interactive.go`.

---

#### [NEW] internal/interactive/chromecast.go

Interactive terminal screen for Chromecast, reusing tcell patterns from DLNA:

```go
package interactive

import (
    "context"
    "fmt"
    "net/url"
    "strconv"
    "strings"
    "sync"
    "time"

    "github.com/alexballas/go2tv/castprotocol"
    "github.com/gdamore/tcell/v2"
    "github.com/mattn/go-runewidth"
)

// ChromecastScreen provides an interactive terminal for Chromecast control.
// Mirrors NewScreen structure for consistency.
type ChromecastScreen struct {
    Current      tcell.Screen
    Client       *castprotocol.CastClient
    exitCTXfunc  context.CancelFunc
    mediaTitle   string
    lastAction   string
    mu           sync.RWMutex
}

var ccFlipflop bool = true

func (p *ChromecastScreen) emitStr(x, y int, style tcell.Style, str string) {
    s := p.Current
    for _, c := range str {
        var comb []rune
        w := runewidth.RuneWidth(c)
        if w == 0 {
            comb = []rune{c}
            c = ' '
            w = 1
        }
        s.SetContent(x, y, c, comb, style)
        x += w
    }
}

// EmitMsg displays actions to the interactive terminal.
func (p *ChromecastScreen) EmitMsg(inputtext string) {
    p.updateLastAction(inputtext)
    s := p.Current

    p.mu.RLock()
    mediaTitle := p.mediaTitle
    p.mu.RUnlock()

    titleLen := len("Title: " + mediaTitle)
    w, h := s.Size()
    boldStyle := tcell.StyleDefault.
        Background(tcell.ColorBlack).
        Foreground(tcell.ColorWhite).Bold(true)
    blinkStyle := tcell.StyleDefault.
        Background(tcell.ColorBlack).
        Foreground(tcell.ColorWhite).Blink(true)

    s.Clear()

    p.emitStr(w/2-titleLen/2, h/2-2, tcell.StyleDefault, "Title: "+mediaTitle)
    if inputtext == "Waiting for status..." {
        p.emitStr(w/2-len(inputtext)/2, h/2, blinkStyle, inputtext)
    } else {
        p.emitStr(w/2-len(inputtext)/2, h/2, boldStyle, inputtext)
    }
    p.emitStr(1, 1, tcell.StyleDefault, "Press ESC to stop and exit.")

    // Show mute status
    if p.Client != nil {
        status, err := p.Client.GetStatus()
        if err == nil && status.Muted {
            p.emitStr(w/2-len("MUTED")/2, h/2+2, blinkStyle, "MUTED")
        }
    }

    p.emitStr(w/2-len(`"p" (Play/Pause)`)/2, h/2+4, tcell.StyleDefault, `"p" (Play/Pause)`)
    p.emitStr(w/2-len(`"m" (Mute/Unmute)`)/2, h/2+6, tcell.StyleDefault, `"m" (Mute/Unmute)`)
    p.emitStr(w/2-len(`"Page Up" "Page Down" (Volume Up/Down)`)/2, h/2+8, tcell.StyleDefault, `"Page Up" "Page Down" (Volume Up/Down)`)
    s.Show()
}

// InterInit starts the interactive terminal for Chromecast.
func (p *ChromecastScreen) InterInit(client *castprotocol.CastClient, mediaURL string, c chan error) {
    p.Client = client

    muteChecker := time.NewTicker(1 * time.Second)

    go func() {
        for range muteChecker.C {
            p.EmitMsg(p.getLastAction())
        }
    }()

    p.mu.Lock()
    mediaTitlefromURL, err := url.Parse(mediaURL)
    if err != nil {
        c <- fmt.Errorf("interactive screen error: %w", err)
        return
    }
    p.mediaTitle = strings.TrimLeft(mediaTitlefromURL.Path, "/")
    p.mu.Unlock()

    s := p.Current
    if err := s.Init(); err != nil {
        c <- fmt.Errorf("interactive screen error: %w", err)
        return
    }

    defStyle := tcell.StyleDefault.
        Background(tcell.ColorBlack).
        Foreground(tcell.ColorWhite)
    s.SetStyle(defStyle)

    p.updateLastAction("Waiting for status...")
    p.EmitMsg(p.getLastAction())

    // Start playback on Chromecast
    if err := client.Play(); err != nil {
        c <- fmt.Errorf("interactive screen error: %w", err)
        return
    }

    // Status watcher goroutine - updates display with playback state
    go func() {
        ticker := time.NewTicker(1 * time.Second)
        defer ticker.Stop()
        for range ticker.C {
            if p.Client == nil {
                return
            }
            status, err := p.Client.GetStatus()
            if err != nil {
                continue
            }
            switch status.PlayerState {
            case "PLAYING":
                p.EmitMsg("Playing")
            case "PAUSED":
                p.EmitMsg("Paused")
            case "BUFFERING":
                p.EmitMsg("Buffering...")
            case "IDLE":
                // Media ended
                p.Fini()
                return
            }
        }
    }()

    for {
        switch ev := s.PollEvent().(type) {
        case *tcell.EventResize:
            s.Sync()
            p.EmitMsg(p.getLastAction())
        case *tcell.EventKey:
            p.HandleKeyEvent(ev)
        }
    }
}

// HandleKeyEvent handles all key press events.
func (p *ChromecastScreen) HandleKeyEvent(ev *tcell.EventKey) {
    client := p.Client

    if ev.Key() == tcell.KeyEscape {
        _ = client.Stop()
        p.Fini()
    }

    if ev.Key() == tcell.KeyPgUp || ev.Key() == tcell.KeyPgDn {
        status, err := client.GetStatus()
        if err != nil {
            return
        }

        // Volume is 0.0-1.0, map to 0-100 for step calculation
        currentVolume := int(status.Volume * 100)
        setVolume := currentVolume - 5
        if ev.Key() == tcell.KeyPgUp {
            setVolume = currentVolume + 5
        }
        if setVolume < 0 {
            setVolume = 0
        }
        if setVolume > 100 {
            setVolume = 100
        }

        _ = client.SetVolume(float32(setVolume) / 100.0)
    }

    switch ev.Rune() {
    case 'p':
        if ccFlipflop {
            ccFlipflop = false
            _ = client.Pause()
            break
        }
        ccFlipflop = true
        _ = client.Play()

    case 'm':
        status, err := client.GetStatus()
        if err != nil {
            break
        }
        _ = client.SetMuted(!status.Muted)
        p.EmitMsg(p.getLastAction())
    }
}

// Fini closes the interactive screen.
func (p *ChromecastScreen) Fini() {
    p.Current.Fini()
    p.exitCTXfunc()
}

// InitChromecastScreen creates a new ChromecastScreen.
func InitChromecastScreen(ctxCancel context.CancelFunc) (*ChromecastScreen, error) {
    s, err := tcell.NewScreen()
    if err != nil {
        return nil, fmt.Errorf("interactive screen error: %w", err)
    }

    return &ChromecastScreen{
        Current:     s,
        exitCTXfunc: ctxCancel,
    }, nil
}

func (p *ChromecastScreen) getLastAction() string {
    p.mu.RLock()
    defer p.mu.RUnlock()
    return p.lastAction
}

func (p *ChromecastScreen) updateLastAction(s string) {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.lastAction = s
}
```

---

#### [MODIFY] [cmd/go2tv/go2tv.go](file:///home/alex/test/go2tv/cmd/go2tv/go2tv.go)

Add Chromecast playback support for CLI mode.

**Add to flagResults struct** (line 43-47):
```go
type flagResults struct {
    dmrURL     string
    deviceType string  // "DLNA" or "Chromecast"
    exit       bool
    gui        bool
}
```

**Add isChromecastURL helper function**:
```go
// isChromecastURL checks if the target URL is a Chromecast device.
// Chromecast devices typically use port 8009.
func isChromecastURL(targetURL string) bool {
    u, err := url.Parse(targetURL)
    if err != nil {
        return false
    }
    return u.Port() == "8009"
}
```

**Update checkTflag** (line 343-355):
```go
func checkTflag(res *flagResults) error {
    if *targetPtr != "" {
        // Validate URL before proceeding.
        _, err := url.ParseRequestURI(*targetPtr)
        if err != nil {
            return fmt.Errorf("checkTflag parse error: %w", err)
        }

        res.dmrURL = *targetPtr

        // Detect device type from URL
        if isChromecastURL(*targetPtr) {
            res.deviceType = devices.DeviceTypeChromecast
        } else {
            res.deviceType = devices.DeviceTypeDLNA
        }
    }

    return nil
}
```

**Update run() function** (after line 143, before interactive screen):

Add Chromecast branch:

```go
// Branch based on device type
if flagRes.deviceType == devices.DeviceTypeChromecast {
    return runChromecastCLI(exitCTX, cancel, flagRes, absMediaFile, mediaType, mediaFile)
}

// Continue with existing DLNA logic...
scr, err := interactive.InitTcellNewScreen(cancel)
// ... rest of existing code
```

**Add runChromecastCLI function**:
```go
// runChromecastCLI handles CLI mode playback to Chromecast devices with interactive terminal.
func runChromecastCLI(ctx context.Context, cancel context.CancelFunc, flagRes *flagResults, mediaPath, mediaType string, mediaFile any) error {
    // Initialize Chromecast client
    client, err := castprotocol.NewCastClient(flagRes.dmrURL)
    if err != nil {
        return fmt.Errorf("chromecast init: %w", err)
    }

    if err := client.Connect(); err != nil {
        return fmt.Errorf("chromecast connect: %w", err)
    }
    defer client.Close(true)

    // Start HTTP server for local media
    whereToListen, err := utils.URLtoListenIPandPort(flagRes.dmrURL)
    if err != nil {
        return err
    }

    s := httphandlers.NewServer(whereToListen)
    serverStarted := make(chan error)

    go func() {
        s.StartSimpleServer(serverStarted, mediaPath)
    }()

    if err := <-serverStarted; err != nil {
        return err
    }

    // Load media on Chromecast
    mediaURL := "http://" + whereToListen + "/" + utils.ConvertFilename(mediaPath)
    if err := client.Load(mediaURL, mediaType, 0, ""); err != nil { // No subtitles for CLI (yet)
        return fmt.Errorf("chromecast load: %w", err)
    }

    // Initialize interactive terminal screen (mirrors DLNA behavior)
    scr, err := interactive.InitChromecastScreen(cancel)
    if err != nil {
        return err
    }

    scrErr := make(chan error)
    go scr.InterInit(client, mediaURL, scrErr)

    select {
    case <-ctx.Done():
        return nil
    case err := <-scrErr:
        return err
    }
}
```

---

### GUI Actions Integration

#### [MODIFY] [actions.go](file:///home/alex/test/go2tv/internal/gui/actions.go)

Add Chromecast playback support to action functions.

**Add imports** (after line 28):
```go
    "github.com/alexballas/go2tv/castprotocol"
    "github.com/alexballas/go2tv/protocol"
```

**Modify playAction** (around line 253-265):

Replace the device check with type-aware logic:

```go
// Replace:
// if screen.controlURL == "" {
//     check(screen, errors.New(lang.L("please select a device")))
//     startAfreshPlayButton(screen)
//     return
// }

// With:
if screen.selectedDevice.addr == "" {
    check(screen, errors.New(lang.L("please select a device")))
    startAfreshPlayButton(screen)
    return
}

// Branch based on device type
if screen.selectedDeviceType == devices.DeviceTypeChromecast {
    chromecastPlayAction(screen)
    return
}

// Continue with existing DLNA logic...
if screen.controlURL == "" {
    check(screen, errors.New(lang.L("please select a device")))
    startAfreshPlayButton(screen)
    return
}
```

**Add chromecastPlayAction** (new function after playAction):

```go
// chromecastPlayAction handles playback on Chromecast devices.
// Supports both local files (via internal HTTP server) and external URLs (direct).
func chromecastPlayAction(screen *FyneScreen) {
    // Handle pause/resume if already playing
    if screen.chromecastClient != nil && screen.chromecastClient.IsConnected() {
        currentState := screen.getScreenState()
        if currentState == "Paused" {
            if err := screen.chromecastClient.Play(); err != nil {
                check(screen, err)
            }
            return
        }
        if screen.PlayPause.Text == "Pause" {
            if err := screen.chromecastClient.Pause(); err != nil {
                check(screen, err)
            }
            return
        }
    }

    // Validate media file or URL
    if screen.mediafile == "" && screen.MediaText.Text == "" {
        check(screen, errors.New(lang.L("please select a media file or enter a media URL")))
        startAfreshPlayButton(screen)
        return
    }

    // Initialize Chromecast client
    client, err := castprotocol.NewCastClient(screen.selectedDevice.addr)
    if err != nil {
        check(screen, fmt.Errorf("chromecast init: %w", err))
        startAfreshPlayButton(screen)
        return
    }

    if err := client.Connect(); err != nil {
        check(screen, fmt.Errorf("chromecast connect: %w", err))
        startAfreshPlayButton(screen)
        return
    }

    screen.chromecastClient = client

    var mediaURL string
    var mediaType string
    var serverStoppedCTX context.Context

    if screen.ExternalMediaURL.Checked {
        // EXTERNAL URL: Pass directly to Chromecast (no HTTP server needed)
        mediaURL = screen.MediaText.Text
        screen.mediafile = mediaURL  // Track for state management

        // Get media type from stream
        mediaURLinfo, err := utils.StreamURL(context.Background(), mediaURL)
        if err != nil {
            check(screen, err)
            startAfreshPlayButton(screen)
            return
        }
        mediaType, err = utils.GetMimeDetailsFromStream(mediaURLinfo)
        mediaURLinfo.Close()
        if err != nil {
            check(screen, err)
            startAfreshPlayButton(screen)
            return
        }

        // No HTTP server for external URLs - use background context for watcher
        var cancel context.CancelFunc
        serverStoppedCTX, cancel = context.WithCancel(context.Background())
        screen.serverStopCTX = serverStoppedCTX
        // Store cancel func to stop watcher on stop action
        go func() {
            <-serverStoppedCTX.Done()
            cancel()
        }()

    } else {
        // LOCAL FILE: Serve via internal HTTP server
        mfile, err := os.Open(screen.mediafile)
        if err != nil {
            check(screen, err)
            startAfreshPlayButton(screen)
            return
        }
        mediaType, err = utils.GetMimeDetailsFromFile(mfile)
        mfile.Close()
        if err != nil {
            check(screen, err)
            startAfreshPlayButton(screen)
            return
        }

        // Start HTTP server for local media
        whereToListen, err := utils.URLtoListenIPandPort(screen.selectedDevice.addr)
        if err != nil {
            check(screen, err)
            startAfreshPlayButton(screen)
            return
        }

        // Stop any existing server
        if screen.httpserver != nil {
            screen.httpserver.StopServer()
        }

        screen.httpserver = httphandlers.NewServer(whereToListen)
        serverStarted := make(chan error)
        var serverCTXStop context.CancelFunc
        serverStoppedCTX, serverCTXStop = context.WithCancel(context.Background())
        screen.serverStopCTX = serverStoppedCTX

        // Start simple HTTP server for Chromecast (no TVPayload/DLNA callbacks needed)
        go func() {
            screen.httpserver.StartSimpleServer(serverStarted, screen.mediafile)
            serverCTXStop()
        }()

        if err := <-serverStarted; err != nil {
            check(screen, err)
            startAfreshPlayButton(screen)
            return
        }

        mediaURL = "http://" + whereToListen + "/" + utils.ConvertFilename(screen.mediafile)
    }

    // Handle subtitle file for Chromecast (WebVTT conversion)
    // PHASE 4 INTEGRATION POINT: Two subtitle paths:
    //   1. Transcoding DISABLED: Serve WebVTT sidecar (handled here)
    //   2. Transcoding ENABLED: Subtitles burned into video stream (Phase 4 FFmpeg handles)
    //
    // When screen.TranscodeCheckBox.Checked is true, Phase 4's FFmpeg command will include
    // subtitle burn-in filter, so we skip WebVTT sidecar here.
    var subtitleURL string
    if screen.subsfile != "" && !screen.TranscodeCheckBox.Checked {
        // Only use WebVTT sidecar when NOT transcoding
        whereToListen, _ := utils.URLtoListenIPandPort(screen.selectedDevice.addr)

        // Handle subtitle based on file extension
        ext := strings.ToLower(filepath.Ext(screen.subsfile))
        switch ext {
        case ".srt":
            webvttData, err := utils.ConvertSRTtoWebVTT(screen.subsfile)
            if err != nil {
                check(screen, fmt.Errorf("subtitle conversion: %w", err))
                // Continue without subtitles rather than failing
            } else {
                // Register WebVTT data with HTTP server using existing AddHandler pattern
                screen.httpserver.AddHandler("/subtitles.vtt", nil, webvttData)
                subtitleURL = "http://" + whereToListen + "/subtitles.vtt"
            }
        case ".vtt":
            // Already WebVTT - register file path with AddHandler
            screen.httpserver.AddHandler("/subtitles.vtt", nil, screen.subsfile)
            subtitleURL = "http://" + whereToListen + "/subtitles.vtt"
        default:
            // Other subtitle formats (e.g., ASS, SSA) require transcoding with burn-in
        }
    }
    // When transcoding is enabled, screen.subsfile is passed to FFmpeg in Phase 4
    // and subtitles are burned into the video stream - no sidecar needed

    // Load media on Chromecast (with optional subtitles)
    if err := client.Load(mediaURL, mediaType, screen.ffmpegSeek, subtitleURL); err != nil {
        check(screen, fmt.Errorf("chromecast load: %w", err))
        startAfreshPlayButton(screen)
        return
    }

    setPlayPauseView("Pause", screen)
    screen.updateScreenState("Playing")

    // Start Chromecast status polling
    go chromecastStatusWatcher(serverStoppedCTX, screen)
}
```

**Add chromecastStatusWatcher** (new function):

```go
// chromecastStatusWatcher polls Chromecast status and updates UI.
// Triggers auto-play next via Fini() when media ends, consistent with DLNA.
func chromecastStatusWatcher(ctx context.Context, screen *FyneScreen) {
    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()

    var previousState string

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            if screen.chromecastClient == nil || !screen.chromecastClient.IsConnected() {
                return
            }

            status, err := screen.chromecastClient.GetStatus()
            if err != nil {
                continue
            }

            // Update state based on player state
            switch status.PlayerState {
            case "PLAYING":
                if screen.getScreenState() != "Playing" {
                    setPlayPauseView("Pause", screen)
                    screen.updateScreenState("Playing")
                }
            case "PAUSED":
                if screen.getScreenState() != "Paused" {
                    setPlayPauseView("Play", screen)
                    screen.updateScreenState("Paused")
                }
            case "IDLE":
                // Media finished playing - trigger auto-play next via Fini()
                // Only trigger if we were playing (not if stopped manually)
                if previousState == "PLAYING" {
                    // Call Fini() which handles auto-play next logic
                    // This is consistent with DLNA callback behavior
                    screen.Fini()
                    return
                }
            }

            previousState = status.PlayerState

            // Update slider position
            if !screen.sliderActive && status.Duration > 0 {
                progress := (status.CurrentTime / status.Duration) * screen.SlideBar.Max
                fyne.Do(func() {
                    screen.SlideBar.SetValue(progress)
                })

                // Update time labels
                current, _ := utils.SecondsToClockTime(int(status.CurrentTime))
                total, _ := utils.SecondsToClockTime(int(status.Duration))
                screen.CurrentPos.Set(current)
                screen.EndPos.Set(total)
            }
        }
    }
}
```

**Modify stopAction** (lines 580-595):

```go
func stopAction(screen *FyneScreen) {
    screen.PlayPause.Enable()

    // Handle Chromecast stop
    if screen.chromecastClient != nil && screen.chromecastClient.IsConnected() {
        _ = screen.chromecastClient.Stop()
        screen.chromecastClient.Close(false)
        screen.chromecastClient = nil
    }

    // Handle DLNA stop (existing logic)
    if screen.tvdata != nil && screen.tvdata.ControlURL != "" {
        _ = screen.tvdata.SendtoTV("Stop")
    }

    if screen.httpserver != nil {
        screen.httpserver.StopServer()
    }

    screen.tvdata = nil
    screen.EmitMsg("Stopped")
}
```

**Modify muteAction** (lines 31-55):

Add Chromecast mute handling at the start:

```go
func muteAction(screen *FyneScreen) {
    // Handle Chromecast mute
    if screen.selectedDeviceType == devices.DeviceTypeChromecast {
        if screen.chromecastClient == nil {
            check(screen, errors.New(lang.L("please select a device")))
            return
        }

        status, err := screen.chromecastClient.GetStatus()
        if err != nil {
            check(screen, errors.New(lang.L("could not get mute status")))
            return
        }

        if err := screen.chromecastClient.SetMuted(!status.Muted); err != nil {
            check(screen, errors.New(lang.L("could not send mute action")))
            return
        }

        if status.Muted {
            setMuteUnmuteView("Mute", screen)
        } else {
            setMuteUnmuteView("Unmute", screen)
        }
        return
    }

    // Existing DLNA logic...
    if screen.renderingControlURL == "" {
        check(screen, errors.New(lang.L("please select a device")))
        return
    }
    // ... rest of existing code
}
```

**Modify volumeAction** (lines 615-649):

Add Chromecast volume handling at the start:

```go
func volumeAction(screen *FyneScreen, up bool) {
    // Handle Chromecast volume
    if screen.selectedDeviceType == devices.DeviceTypeChromecast {
        if screen.chromecastClient == nil {
            check(screen, errors.New(lang.L("please select a device")))
            return
        }

        status, err := screen.chromecastClient.GetStatus()
        if err != nil {
            check(screen, errors.New(lang.L("could not get the volume levels")))
            return
        }

        newVolume := status.Volume - 0.05
        if up {
            newVolume = status.Volume + 0.05
        }
        if newVolume < 0 {
            newVolume = 0
        }
        if newVolume > 1 {
            newVolume = 1
        }

        if err := screen.chromecastClient.SetVolume(newVolume); err != nil {
            check(screen, errors.New(lang.L("could not send volume action")))
        }
        return
    }

    // Existing DLNA logic...
    if screen.renderingControlURL == "" {
        check(screen, errors.New(lang.L("please select a device")))
        return
    }
    // ... rest of existing code
}
```

---

### Slider Seek Integration

#### [MODIFY] [main.go](file:///home/alex/test/go2tv/internal/gui/main.go)

Update `DragEnd()` and `Tapped()` methods for Chromecast seek support.

**Modify DragEnd** (lines 118-161):

Add Chromecast seek at the start:

```go
func (t *tappedSlider) DragEnd() {
    t.screen.sliderActive = true

    if t.screen.State == "Playing" || t.screen.State == "Paused" {
        // Handle Chromecast seek
        if t.screen.selectedDeviceType == devices.DeviceTypeChromecast {
            if t.screen.chromecastClient == nil || !t.screen.chromecastClient.IsConnected() {
                return
            }

            status, err := t.screen.chromecastClient.GetStatus()
            if err != nil || status.Duration == 0 {
                return
            }

            // Calculate seek position from slider value
            cur := (status.Duration * t.screen.SlideBar.Value) / t.screen.SlideBar.Max
            roundedInt := int(math.Round(cur))

            // Seek to position
            if err := t.screen.chromecastClient.Seek(roundedInt); err != nil {
                return
            }

            // Update time display
            reltime, err := utils.SecondsToClockTime(roundedInt)
            if err != nil {
                return
            }
            t.screen.CurrentPos.Set(reltime)
            return
        }

        // Existing DLNA logic...
        getPos, err := t.screen.tvdata.GetPositionInfo()
        // ... rest of existing code
    }
}
```

**Modify Tapped** (lines 163-207):

Add Chromecast seek at the start (same pattern as DragEnd):

```go
func (t *tappedSlider) Tapped(p *fyne.PointEvent) {
    t.screen.sliderActive = true
    t.Slider.Tapped(p)

    if t.screen.State == "Playing" || t.screen.State == "Paused" {
        // Handle Chromecast seek
        if t.screen.selectedDeviceType == devices.DeviceTypeChromecast {
            if t.screen.chromecastClient == nil || !t.screen.chromecastClient.IsConnected() {
                return
            }

            status, err := t.screen.chromecastClient.GetStatus()
            if err != nil || status.Duration == 0 {
                return
            }

            // Calculate seek position from slider value
            cur := (status.Duration * t.screen.SlideBar.Value) / t.screen.SlideBar.Max
            roundedInt := int(math.Round(cur))

            // Seek to position
            if err := t.screen.chromecastClient.Seek(roundedInt); err != nil {
                return
            }

            // Update time display
            reltime, err := utils.SecondsToClockTime(roundedInt)
            if err != nil {
                return
            }
            t.screen.CurrentPos.Set(reltime)
            return
        }

        // Existing DLNA logic...
        getPos, err := t.screen.tvdata.GetPositionInfo()
        // ... rest of existing code
    }
}
```

---

## Verification Plan

### Automated Tests

#### Unit Tests for CastClient

**New file**: `castprotocol/client_test.go`

- `TestCastClient_NewCastClient()` - verify client creation from URL
- `TestCastClient_AddressParser()` - verify host:port extraction
- Test connection state management

**Command**: `go test -v ./castprotocol`

#### Unit Tests for ChromecastRenderer

**New file**: `protocol/chromecast_renderer_test.go`

- `TestChromecastRenderer_Interface()` - verify interface compliance
- Test volume conversion (0-100 ↔ 0.0-1.0)
- Test time format conversion

**Command**: `go test -v ./protocol`

### Manual Verification

#### Test 1: Device Selection State Reset

**Prerequisites**: DLNA and Chromecast devices on network

**Steps**:
1. Build and run Go2TV: `make build && ./build/go2tv`
2. Select a DLNA device and start playback
3. While playing, select a Chromecast device
4. Verify:
   - Playback stops on DLNA device
   - UI resets (slider to 0, time to 00:00:00)
   - No error messages about DLNA SOAP operations
5. Start playback on Chromecast
6. Verify no DLNA parameters are used (check logs)

**Expected Result**: Clean device switch with no state leakage.

#### Test 2: Chromecast Basic Playback

**Prerequisites**: Chromecast device on network

**Steps**:
1. Build and run Go2TV: `make build && ./build/go2tv`
2. Select a Chromecast device
3. Select a compatible media file (MP4/H.264)
4. Click Play
5. Verify media plays on Chromecast
6. Test Play/Pause toggle
7. Test Stop button
8. Verify slider updates with playback position

**Expected Result**: Basic playback controls work on Chromecast.

#### Test 3: Chromecast Volume Control

**Prerequisites**: Chromecast device on network

**Steps**:
1. Start playback on Chromecast
2. Click volume up/down buttons
3. Verify volume changes on device
4. Test mute/unmute toggle

**Expected Result**: Volume and mute controls work.

#### Test 4: DLNA Regression Test

**Prerequisites**: DLNA device on network

**Steps**:
1. Build and run Go2TV: `make build && ./build/go2tv`
2. Select a DLNA device
3. Select media file, start playback
4. Test all controls: play, pause, stop, seek, volume, mute
5. Verify all existing functionality works

**Expected Result**: No regression in DLNA functionality.

### Build Verification

**Command**: `make build`

**Expected Result**: Clean build with no errors.

**Command**: `make test`

**Expected Result**: All tests pass.

---

## Implementation Notes

### State Management Architecture

**Problem Solved**: DLNA-specific parameters (`controlURL`, `eventlURL`, `renderingControlURL`) persisted after switching to Chromecast, causing operations to use stale DLNA endpoints.

**Solution**: 
1. `resetDeviceState()` function clears ALL protocol-specific state
2. Device selection callback calls `resetDeviceState()` when switching devices
3. Type-aware branching in all action functions
4. Chromecast uses `chromecastClient` field, DLNA uses `tvdata` field
5. Never mix Chromecast and DLNA operations

**State Reset Triggers**:
- Device selection changes → `resetDeviceState()`
- Stop action → Clear relevant client
- App close → Cleanup both

### Chromecast Client Lifecycle

1. **Creation**: On device selection (optional, defer to play)
2. **Connection**: On first play action
3. **Playback**: Load media URL, control playlist
4. **Disconnection**: On stop or device switch
5. **Cleanup**: `Close(stopMedia)` method

### go-chromecast Library Usage

**Key API Methods**:
- `application.NewApplication()` - create application
- `app.Start(addr, port)` - connect to device
- `app.Load(url, startTime, contentType, ...)` - load media
- `app.Pause()` / `app.Unpause()` - pause control
- `app.Stop()` - stop current media
- `app.Seek(seconds)` - seek relative
- `app.SetVolume(float32)` - set volume (0.0-1.0)
- `app.SetMuted(bool)` - mute control
- `app.Status()` - get current status
- `app.Close(stopMedia)` - disconnect

---

## Design Decisions

### Auto-Play Next Media
**Decision**: Handle in go2tv application logic for consistency with DLNA.

- Poll Chromecast status in `chromecastStatusWatcher`
- When `PlayerState` becomes `IDLE` and was previously `PLAYING`:
  - Call existing `Fini()` method which triggers `getNextMedia()` and `playAction()`
- This reuses existing auto-play logic, no Chromecast-specific queue API

### Subtitle Support
**Decision**: Two paths based on transcoding state (`screen.TranscodeCheckBox.Checked`).

**Phase 3 (this plan)** - WebVTT sidecar for direct streaming:
- Condition: `screen.subsfile != "" && !screen.TranscodeCheckBox.Checked`
- Convert SRT → WebVTT via `utils.ConvertSRTtoWebVTT()`
- Serve WebVTT via HTTP server (`RegisterSubtitleData()`)
- Pass subtitle URL to Chromecast `Load()` call

**Phase 4 (future)** - Burn-in for transcoding:
- Condition: `screen.TranscodeCheckBox.Checked` (skip WebVTT logic)
- FFmpeg includes subtitle filter: `-vf "subtitles=file.srt"`
- `screen.subsfile` passed to FFmpeg command builder
- No separate subtitle track - burned into video stream

**Integration Contract**:
```
if screen.subsfile != "" {
    if screen.TranscodeCheckBox.Checked {
        // Phase 4: FFmpeg burns subtitles into stream
        // Pass screen.subsfile to BuildChromecastFFmpegArgs()
    } else {
        // Phase 3: WebVTT sidecar (already implemented)
        subtitleURL = serveWebVTT(screen.subsfile)
    }
}
```

### Transcode Integration
**Decision**: Phase 3 is protocol-agnostic to transcoding.

**Two media source modes supported**:

1. **Local Files** (default):
   - User selects a file from filesystem
   - Go2TV starts internal HTTP server
   - Server serves file (direct or transcoded via Phase 4)
   - Chromecast receives URL: `http://192.168.x.x:PORT/filename`

2. **External URLs** ("Media from URL" checkbox):
   - User enters an external URL (e.g., `https://example.com/video.mp4`)
   - URL passed directly to Chromecast (no internal HTTP server)
   - Chromecast fetches directly from external source

**Phase 4 transcoding**:
- For local files only (external URLs streamed as-is)
- Phase 4 adds format detection and conditional transcoding
- HTTP server serves transcoded stream transparently
- No changes needed in Phase 3 for transcoding support

