# Phase 1: Core Infrastructure & Device Discovery

Implementation plan for adding device type abstraction and Chromecast discovery to Go2TV.

## User Review Required

> [!IMPORTANT]
> This is Phase 1 of a 5-phase project. Review the complete roadmap in [chromecast_v2_roadmap.md](file:///home/alex/test/go2tv/chromecast_v2_roadmap.md) before proceeding.

> [!NOTE]
> **Decisions Made**:
> - Using `github.com/grandcat/zeroconf` library (from mdnstest)
> - Device caching implementation from mdnstest will be ported
> - `LoadSSDPservices()` kept as deprecated wrapper for backward compatibility
> - Health checking: periodic TCP checks to remove stale devices (already in mdnstest)

---

## Proposed Changes

### Chromecast Discovery

#### [NEW] [chromecast_discovery.go](file:///home/alex/test/go2tv/devices/chromecast_discovery.go)

Port mDNS discovery and device caching from `mdnstest/main.go`:

- Package-level variables:
  - `chromeCastDevices map[string]string` - cached devices (address -> friendly name)
  - `ccMu sync.Mutex` - protects device map
- `StartChromecastDiscoveryLoop(ctx context.Context)`
  - Continuously discovers Chromecast devices using mDNS
  - Browse `_googlecast._tcp` service every 10 seconds
  - Add newly discovered devices to cache
  - Run in background goroutine
- `GetChromecastDevices() map[string]string`
  - Returns current cached Chromecast devices
  - Performs TCP health check on each cached device
  - Removes devices that fail health check
  - Returns map of address -> friendly name with " (Chromecast)" suffix
- `HostPortIsAlive(address string) bool`
  - TCP dial with 2-second timeout
  - Returns true if connection succeeds

### Device Discovery Integration

#### [MODIFY] [devices.go](file:///home/alex/test/go2tv/devices/devices.go)

Keep existing `LoadSSDPservices()` unchanged. Add new function:

- `LoadAllDevices(delay int) (map[string]string, error)`
  - **Non-blocking async pattern**: Launch both discoveries concurrently
  - Call `LoadSSDPservices(delay)` in goroutine for DLNA devices (SSDP protocol)
  - Call `GetChromecastDevices()` in goroutine for Chromecast devices (mDNS cache - instant)
  - Use `select` with timeout to collect results as they arrive
  - Return immediately with whatever results are available (partial results OK)
  - If DLNA is slow, Chromecast devices still appear quickly
  - Merge available results into single map (aggregation happens here only)
  - Return combined map with device names suffixed appropriately

**Key principle**: Each protocol runs independently. Delays in one don't block the other. Return partial results immediately.

---

### GUI Integration

#### [MODIFY] [actions.go](file:///home/alex/test/go2tv/internal/gui/actions.go)

Minimal change to use combined device list:

- Modify `getDevices(delay int)`:
  - Change `devices.LoadSSDPservices(delay)` to `devices.LoadAllDevices(delay)`
  - Keep all existing logic unchanged (sorting, conversion to `[]devType`)

#### [MODIFY] [gui.go](file:///home/alex/test/go2tv/internal/gui/gui.go)

Add Chromecast discovery initialization:

- In `Start()` function, before `w.ShowAndRun()`:
  - Start background Chromecast discovery: `go devices.StartChromecastDiscoveryLoop(ctx)`
  - This runs independently from DLNA discovery
  - Continuously updates Chromecast cache in background


---

### Dependencies

#### [MODIFY] [go.mod](file:///home/alex/test/go2tv/go.mod)

Add Chromecast discovery dependency:

```go
require (
    // ... existing dependencies
    github.com/grandcat/zeroconf v1.0.0
)
```

Run `go mod tidy` after adding dependency.

---

## Verification Plan

### Automated Tests

#### Unit Tests for Device Abstraction

**New file**: `devices/types_test.go`

Test device interface implementations:
- `TestDLNADevice_GetDisplayName()` - verify suffix `" (DLNA)"`
- `TestChromecastDevice_GetDisplayName()` - verify suffix `" (Chromecast)"`
- `TestDeviceType_String()` - verify enum string representation

**Command**: `go test -v ./devices -run TestDLNADevice`

#### Unit Tests for Device Discovery

**New file**: `devices/unified_discovery_test.go`

Test device list merging and sorting:
- `TestLoadAllDevices_Sorting()` - verify DLNA devices appear before Chromecast
- `TestLoadAllDevices_EmptyResults()` - verify graceful handling when no devices found

**Command**: `go test -v ./devices -run TestLoadAllDevices`

#### Integration Test for Chromecast Discovery

**New file**: `devices/chromecast_discovery_test.go`

Test mDNS discovery (requires network):
- `TestLoadChromecastDevices_Integration()` - skip if no Chromecast on network
- Use `testing.Short()` to skip in CI: `if testing.Short() { t.Skip() }`

**Command**: `go test -v ./devices -run TestLoadChromecastDevices`

### Manual Verification

#### Test 1: Device List Display

**Prerequisites**: 
- At least one DLNA device on network
- At least one Chromecast device on network (or use Chromecast emulator)

**Steps**:
1. Build and run Go2TV: `make build && ./build/go2tv`
2. Click "Refresh" button in device list
3. Verify device list shows both DLNA and Chromecast devices
4. Verify each device has correct suffix: `(DLNA)` or `(Chromecast)`
5. Verify DLNA devices appear before Chromecast devices in list
6. Verify devices are sorted alphabetically within each type

**Expected Result**: Device list displays unified list with clear type identification.

#### Test 2: DLNA Functionality Unchanged

**Prerequisites**: DLNA device on network

**Steps**:
1. Build and run Go2TV: `make build && ./build/go2tv`
2. Select a DLNA device from list
3. Select a media file
4. Click Play
5. Verify media plays on DLNA device
6. Test pause, stop, seek controls

**Expected Result**: All existing DLNA functionality works without regression.

#### Test 3: Chromecast Device Selection (No Playback Yet)

**Prerequisites**: Chromecast device on network

**Steps**:
1. Build and run Go2TV: `make build && ./build/go2tv`
2. Select a Chromecast device from list
3. Verify device is selected (highlighted in list)
4. Note: Playback will not work yet (Phase 3 feature)

**Expected Result**: Chromecast device can be selected without errors.

### Build Verification

**Command**: `make build`

**Expected Result**: Clean build with no errors or warnings.

**Command**: `make test`

**Expected Result**: All existing tests pass, new tests pass.

---

## Implementation Notes

**Discovery Architecture**:
- **DLNA**: Uses SSDP protocol, synchronous discovery on-demand when `LoadSSDPservices()` called
- **Chromecast**: Uses mDNS protocol, asynchronous background loop continuously updating cache (instant reads)
- **Separation**: Each protocol maintains its own discovery mechanism independently
- **Aggregation**: Only happens in `LoadAllDevices()` when presenting to GUI
- **Non-blocking async**: `LoadAllDevices()` launches both discoveries concurrently, returns partial results immediately
- **No cross-blocking**: Delays in DLNA don't block Chromecast results and vice versa

**Device Caching Strategy** (from mdnstest):
- Background goroutine continuously discovers devices via mDNS (2s query timeout + 500ms delay)
- Devices cached in package-level map with mutex protection
- Health checking goroutine runs every 5 seconds, removes stale devices
- GUI calls `LoadAllDevices()` which reads from cache (fast, no blocking)
- **Mobile**: Uses same 2s timeout pattern for recovery when phone wakes from sleep

**Backward Compatibility**:
- `LoadSSDPservices()` kept unchanged for existing callers
- Returns `map[string]string` for existing callers
- No changes to existing DLNA discovery mechanism
