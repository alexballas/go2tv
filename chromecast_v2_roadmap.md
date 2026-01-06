# Chromecast V2 Integration Roadmap

## Project Overview

Add Chromecast V2 (Cast v2) support to Go2TV alongside existing DLNA functionality. Implementation must be modular, maintain backward compatibility, and provide seamless UX when switching between device types.

## Architecture Principles

1. **Device Type Abstraction** - Create interface-based architecture to support multiple protocols
2. **Protocol Isolation** - Keep DLNA and Chromecast logic completely separate
3. **Automatic Adaptation** - UI and transcoding adapt automatically based on selected device
4. **Zero Breaking Changes** - Existing DLNA functionality remains unchanged

---

## Phase 1: Core Infrastructure & Device Discovery ✅

**Goal**: Establish foundation for multi-protocol support and implement Chromecast device discovery

**Duration Estimate**: 1-2 weeks

**Status**: Completed

### Tasks

#### 1.1 Device Type Abstraction Layer
- Create `devices/types.go` with device type enum (DLNA, Chromecast)
- Define `Device` interface with common methods (GetName, GetAddress, GetType, GetCapabilities)
- Refactor existing `devType` struct to implement new interface
- Create `DLNADevice` and `ChromecastDevice` concrete types

#### 1.2 Chromecast Discovery Integration
- Add `github.com/grandcat/zeroconf` dependency to main `go.mod`
- Create `devices/chromecast_discovery.go` for mDNS-based discovery
- Port logic from `mdnstest/main.go` into production code
- Implement health checking for Chromecast devices (TCP connection test)
- Add device metadata extraction (friendly name, model, capabilities)

#### 1.3 Unified Device List
- Modify `devices.LoadSSDPservices()` to return `[]Device` instead of `map[string]string`
- Create `devices.LoadChromecastDevices()` for Chromecast discovery
- Implement `devices.LoadAllDevices()` to merge DLNA + Chromecast devices
- Add device type suffix: `"Device Name (Chromecast)"` vs `"Device Name (DLNA)"`
- Ensure deterministic sorting (DLNA first, then Chromecast, alphabetical within type)

#### 1.4 Update GUI Device List
- Modify `internal/gui/actions.go:getDevices()` to use new unified device list
- Update `devType` struct in `internal/gui/gui.go` to include device type field
- Add visual indicator in device list (icon or suffix)

**Dependencies**: None

**Deliverables**:
- Device abstraction interfaces
- Working Chromecast discovery via mDNS
- Unified device list showing both DLNA and Chromecast devices
- Device type clearly visible in UI

---

## Phase 2: Device Selection & UI Behavior ✅

**Goal**: Implement device-aware UI that adapts based on selected device type

**Duration Estimate**: 1 week

**Status**: Completed

### Tasks

#### 2.1 Device Selection Tracking
- Add `selectedDeviceType` field to `FyneScreen` struct
- Update device selection logic to track device type
- Store device type in `selectedDevice` field

#### 2.2 Transcode Option Behavior
- Transcode checkbox enabled when any device is selected
- For DLNA devices: Restore previous DLNA transcode preference
- For Chromecast devices: Leave transcode state unchanged
- User controls transcode option manually
- Auto-detect media compatibility in Phase 4 when media is loaded and auto-enable if needed

#### 2.3 Runtime State Management
- Track device type in memory (selectedDeviceType field)
- Transcode checkbox state maintained as user changes it
- No persistence needed - transcode is a runtime state

**Dependencies**: Phase 1

**Deliverables**:
- UI adapts when device type changes (DMRextractor skipped for Chromecast)
- Transcode option not automatically changed by device type
- Runtime state tracks current device type and transcode selection

---

## Phase 3: Chromecast Communication Protocol

**Goal**: Implement Cast v2 protocol for device communication

**Duration Estimate**: 2-3 weeks

### Tasks

#### 3.1 Cast Protocol Dependencies
- Add `github.com/vishen/go-chromecast` library to `go.mod`
- Review library API and capabilities
- Document required protocol messages (CONNECT, LAUNCH, LOAD, PLAY, PAUSE, STOP, SEEK)

#### 3.2 Protocol Abstraction Layer
- Create `castprotocol/` package for Chromecast-specific logic
- Define `CastClient` interface with methods: Connect, Launch, Load, Play, Pause, Stop, Seek, GetStatus
- Implement connection management (WebSocket to Chromecast device)
- Implement session management (app launch, media session)

#### 3.3 Media Controller
- Create `castprotocol/media_controller.go`
- Implement media loading (URL, metadata, content type)
- Implement playback controls (play, pause, stop)
- Implement seek functionality
- Implement volume control
- Implement status polling

#### 3.4 Protocol Interface Unification
- Create `protocol/` package with `MediaRenderer` interface
- Implement `DLNARenderer` wrapper around existing SOAP calls
- Implement `ChromecastRenderer` using Cast protocol
- Update `soapcalls.TVPayload` to use `MediaRenderer` interface

**Dependencies**: Phase 2

**Deliverables**:
- Working Cast v2 protocol implementation
- Unified protocol interface for DLNA and Chromecast
- Basic playback control (play, pause, stop) working on Chromecast

---

## Phase 4: Media Transcoding for Chromecast

**Goal**: Implement Chromecast-specific transcoding with conditional FFmpeg usage

**Duration Estimate**: 2 weeks

### Tasks

#### 4.1 Chromecast Media Format Specifications
- Document supported formats:
  - **Video**: H.264 High Profile, HEVC, VP8, VP9, AV1
  - **Audio**: AAC, MP3, Vorbis, Opus, FLAC
  - **Container**: MP4, WebM, MKV/Matroska
- Define FFmpeg transcoding profiles for Chromecast (when transcoding needed)

#### 4.2 Media Format Detection
- Extend `ffprobe.go` to extract video/audio codec information
- Add `GetMediaCodecInfo()` function to parse codec data
- Add `IsChromecastCompatible()` function to check if transcoding needed
- Support both Windows and non-Windows platforms

#### 4.3 Conditional Transcoding Logic
- When media file is selected for Chromecast:
  - Use ffprobe to detect media format
  - Check if format is compatible with Chromecast
  - Auto-enable transcode checkbox if incompatible
  - Allow user to override if desired
- For compatible formats: stream directly (no transcoding)
- For incompatible formats: transcode to H.264/AAC/MP4

#### 4.4 Chromecast FFmpeg Command Builder
- Create `soapcalls/utils/chromecast_transcode.go`
- Implement `BuildChromecastFFmpegArgs()`:
  ```
  -i input
  -c:v libx264 -profile:v high -level 4.1
  -preset fast -crf 23
  -maxrate 10M -bufsize 20M
  -c:a aac -b:a 192k -ar 48000 -ac 2
  -movflags +faststart
  -f mp4 pipe:1
  ```
- Handle subtitle burning (if subtitles provided)
- Implement adaptive bitrate based on network conditions (future enhancement)

#### 4.5 Integration with HTTP Server
- Modify `httphandlers/httphandlers.go` to detect device type
- Route Chromecast requests through conditional transcoding pipeline
- Ensure DLNA requests use existing pipeline
- Update `ServeTranscodedStream` to accept transcode profile

#### 4.6 Media URL Generation
- Chromecast requires accessible HTTP URL for media
- Ensure HTTP server exposes stream at predictable URL (direct or transcoded)
- Implement proper MIME type headers for Chromecast
- Add CORS headers if needed

**Dependencies**: Phase 3

**Deliverables**:
- Media format detection using ffprobe
- Conditional transcoding (only when needed)
- Chromecast-specific FFmpeg transcoding profiles
- Automatic format conversion for incompatible media (H.264 + AAC in MP4 container)

---

## Phase 5: Integration, Testing & Polish

**Goal**: Complete integration, comprehensive testing, and UX refinement

**Duration Estimate**: 1-2 weeks

### Tasks

#### 5.1 End-to-End Integration
- Wire up all components (discovery → selection → protocol → transcoding)
- Implement error handling and recovery
- Add logging for debugging
- Test complete playback flow for both DLNA and Chromecast

#### 5.2 UI Polish
- Add Chromecast icon to device list
- Improve visual feedback for locked controls
- Add status messages ("Transcoding required for Chromecast")
- Update help/documentation

#### 5.3 Testing Matrix
- **DLNA Devices**: Verify no regression in existing functionality
- **Chromecast Devices**: Test all media types (video, audio, images)
- **Device Switching**: Test switching between DLNA and Chromecast mid-session
- **Transcoding**: Verify all input formats transcode correctly
- **Error Cases**: Network failures, unsupported formats, device disconnection

#### 5.4 Performance Optimization
- Profile transcoding performance
- Optimize FFmpeg parameters for latency vs quality
- Implement connection pooling if needed
- Add caching for device discovery

#### 5.5 Documentation
- Update README.md with Chromecast support information
- Document Chromecast-specific requirements
- Add troubleshooting guide
- Update CLI help text

**Dependencies**: Phase 4

**Deliverables**:
- Fully functional Chromecast V2 support
- Comprehensive test coverage
- Updated documentation
- Production-ready release

---

## Technical Decisions & Constraints

### Chromecast Media Requirements
- **Conditional Transcoding**: Transcode only when media format is incompatible
- **Direct Streaming**: Stream directly when format is compatible with Chromecast
- **Supported Formats**:
  - Video: H.264 (High Profile), HEVC, VP8, VP9, AV1
  - Audio: AAC, MP3, Vorbis, Opus, FLAC (audio-only)
  - Containers: MP4, WebM, MKV/Matroska
- **Transcoded Format** (when needed):
  - **Format**: MP4 container with H.264 video + AAC audio
  - **Resolution**: Max 1080p@30fps or 720p@60fps
  - **Bitrate**: Target 8-10 Mbps for video, 192 kbps for audio
  - **Audio**: Stereo AAC at 48kHz

### FFmpeg Command Template (when transcoding is needed)
```bash
ffmpeg -i input.mkv \
  -c:v libx264 -profile:v high -level 4.1 \
  -preset fast -crf 23 \
  -maxrate 10M -bufsize 20M \
  -vf "scale='min(1920,iw)':'min(1080,ih)':force_original_aspect_ratio=decrease" \
  -c:a aac -b:a 192k -ar 48000 -ac 2 \
  -movflags +faststart \
  -f mp4 pipe:1
```

**Note**: Transcoding is only performed when input media format is incompatible with Chromecast. Compatible media (e.g., MP4/H.264/AAC) streams directly without transcoding.

### Device Discovery
- **DLNA**: SSDP (existing implementation)
- **Chromecast**: mDNS service `_googlecast._tcp.local`
- **Health Check**: TCP connection test every 5 seconds
- **Timeout**: 10 seconds for mDNS discovery

### Protocol Libraries
- **Chosen**: `github.com/vishen/go-chromecast` - mature, actively maintained, full Cast v2 support
- Wrapper pattern: Create `ChromecastRenderer` that wraps library and implements `MediaRenderer` interface

---

## Risk Mitigation

### Risks
1. **Cast Protocol Complexity** - Cast v2 protocol may be more complex than anticipated
   - *Mitigation*: Use well-maintained library; allocate extra time for Phase 3
2. **Transcoding Performance** - Real-time transcoding may be CPU-intensive
   - *Mitigation*: Use FFmpeg hardware acceleration if available; optimize presets
3. **Device Compatibility** - Different Chromecast generations may behave differently
   - *Mitigation*: Test on multiple devices; implement fallback mechanisms
4. **DLNA Regression** - Changes may break existing DLNA functionality
   - *Mitigation*: Comprehensive regression testing; maintain separate code paths

---

## Success Criteria

- ✅ Chromecast devices appear in device list with clear identification
- ✅ Transcode option not automatically changed by device type
- ✅ Compatible media streams directly without transcoding
- ✅ Incompatible media automatically transcodes to Chromecast-compatible format
- ✅ Media plays successfully on Chromecast devices
- ✅ All playback controls work (play, pause, stop, seek, volume)
- ✅ Switching between DLNA and Chromecast devices works seamlessly
- ✅ No regression in existing DLNA functionality
- ✅ User experience is intuitive with automatic format detection

---

## Future Enhancements (Post-V1)

- Chromecast Audio group support
- Adaptive bitrate streaming
- Hardware-accelerated transcoding (VAAPI, NVENC, QSV)
- Chromecast Ultra 4K support
- Queue management for Chromecast
- Subtitle support for Chromecast (burned-in or WebVTT)
- Google Cast SDK integration for mobile apps
