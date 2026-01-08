# Phase 3a: Core Cast Protocol Package - Implementation Details

**Status**: ✅ Completed

## Summary

Implemented the `castprotocol/` package providing a CastClient wrapper around `go-chromecast` with native WebVTT subtitle track support via custom LOAD commands.

## Files Created

### [castprotocol/client.go](file:///home/alex/test/go2tv/castprotocol/client.go)

Core client wrapper with connection management for subtitle tracks.

**Key Design Decisions:**
- Creates own `cast.Connection` and passes to Application via `WithConnection()`
- Stores both `app` and `conn` - app for standard operations, conn for custom subtitle loads
- Thread-safe with mutex protection on all operations

**API:**
```go
NewCastClient(deviceAddr string) (*CastClient, error)
Connect() error
Load(mediaURL, contentType string, startTime int, subtitleURL string) error
Play() / Pause() / Stop() error
Seek(seconds int) error
SetVolume(level float32) / SetMuted(muted bool) error
GetStatus() (*CastStatus, error)
Close(stopMedia bool) error
IsConnected() bool
```

---

### [castprotocol/status.go](file:///home/alex/test/go2tv/castprotocol/status.go)

Playback status struct.

```go
type CastStatus struct {
    PlayerState string  // "PLAYING", "PAUSED", "IDLE", "BUFFERING"
    CurrentTime float32
    Duration    float32
    Volume      float32
    Muted       bool
    MediaTitle  string
    ContentType string
}
```

---

### [castprotocol/media.go](file:///home/alex/test/go2tv/castprotocol/media.go)

Extended media types for WebVTT subtitle tracks.

```go
type MediaTrack struct {
    TrackId      int    `json:"trackId"`
    Type         string `json:"type"`            // "TEXT"
    SubType      string `json:"subtype"`         // "SUBTITLES"
    ContentId    string `json:"trackContentId"`  // WebVTT URL
    ContentType  string `json:"trackContentType"` // "text/vtt"
    Name         string `json:"name"`
    Language     string `json:"language"`
}

type MediaItemWithTracks struct {
    ContentId   string       `json:"contentId"`
    ContentType string       `json:"contentType"`
    StreamType  string       `json:"streamType"`
    Duration    float32      `json:"duration,omitempty"`
    Metadata    MediaMeta    `json:"metadata,omitempty"`
    Tracks      []MediaTrack `json:"tracks,omitempty"`
}
```

---

### [castprotocol/loader.go](file:///home/alex/test/go2tv/castprotocol/loader.go)

Custom LOAD command sender with subtitle track support.

**Key Function:**
```go
LoadWithSubtitles(conn cast.Conn, transportId, mediaURL, contentType string, startTime int, subtitleURL string) error
```

**How it works:**
1. Creates `MediaItemWithTracks` with media URL
2. If `subtitleURL` provided, adds `MediaTrack` with type=TEXT, subtype=SUBTITLES
3. Builds `CustomLoadPayload` with tracks and `activeTrackIds=[1]`
4. Sends directly to media receiver via `conn.Send()` using namespace `urn:x-cast:com.google.cast.media`

---

## Subtitle Track Flow

```
CastClient.Load(url, contentType, startTime, subtitleURL)
    │
    ├── subtitleURL provided?
    │   ├── Yes: Get transportId from app.App()
    │   │        → LoadWithSubtitles(conn, transportId, ...)
    │   │           → Build MediaTrack + MediaItemWithTracks
    │   │           → Send CustomLoadPayload via conn.Send()
    │   │
    │   └── No: Standard app.Load() (no subtitles)
    │
    └── Media plays on Chromecast with/without subtitles
```

## go-chromecast API Notes

**Library**: `github.com/vishen/go-chromecast` v0.3.4

**Workaround for subtitle support:**
- Standard `app.Load()` doesn't support `tracks` field
- Solution: Create our own Connection, share with Application
- Send custom LoadMediaCommand with `tracks` + `activeTrackIds` via `conn.Send()`

**API Differences Found:**
| Plan | Actual v0.3.4 |
|------|---------------|
| `Load(url, contentType, ...)` | `Load(url, startTime, contentType, transcode, detach, forceDetach)` |
| `LoadWithOptions()` | Does not exist |
| `CurrentTime float64` | `CurrentTime float32` |

## Verification

- `go build ./castprotocol/...` ✅
- `make build` ✅
- `make test` ✅
