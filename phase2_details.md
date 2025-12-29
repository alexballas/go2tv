# Phase 2: Device Selection & UI Behavior

Implementation plan for adding device-aware UI that adapts based on selected device type.

## User Review Required

> [!IMPORTANT]
> This is Phase 2 of a 5-phase project. Review complete roadmap in [chromecast_v2_roadmap.md](file:///home/alex/test/go2tv/chromecast_v2_roadmap.md) before proceeding.
>
> [!NOTE]
> **Decisions Made**:
> - Device type tracked explicitly via Device.Type field (not from string parsing)
> - " (Chromecast)" suffix preserved for visual UI distinction
> - No tooltip message explaining locked controls (user feedback: disabled checkbox is sufficient)
> - Runtime state only - no persistence of transcode preference
> - Devices sorted by type first (DLNA before Chromecast), then alphabetically

---

## Proposed Changes

### Device Type Abstraction

#### [MODIFY] [devices.go](file:///home/alex/test/go2tv/devices/devices.go)

Add explicit device type tracking:

- **Type constants**:
  ```go
  const (
      DeviceTypeDLNA       = "DLNA"
      DeviceTypeChromecast = "Chromecast"
  )
  ```

- **Device struct**:
  ```go
  type Device struct {
      Name string
      Addr string
      Type string  // "DLNA" or "Chromecast"
  }
  ```

- **Refactor LoadSSDPservices()**:
  - Change return type from `map[string]string` to `[]Device`
  - Set `Type: DeviceTypeDLNA` for all DLNA devices
  - Keep existing discovery logic unchanged

- **Refactor DevicePicker()**:
  - Change input from `map[string]string` to `[]Device`
  - Simplify to return `devices[n-1].Addr` directly

- **Add sortDevices()**:
  - Sort devices by type first (DLNA < Chromecast)
  - Then alphabetically within each type

### Chromecast Discovery Update

#### [MODIFY] [chromecast_discovery.go](file:///home/alex/test/go2tv/devices/chromecast_discovery.go)

Update return type:

- **Refactor GetChromecastDevices()**:
  - Change return type from `map[string]string` to `[]Device`
  - Set `Type: DeviceTypeChromecast` for all Chromecast devices
  - Keep " (Chromecast)" suffix in Name for UI distinction
  - Keep existing caching and health check logic

### Unified Device Loading

#### [MODIFY] [LoadAllDevices() in devices.go](file:///home/alex/test/go2tv/devices/devices.go)

Update to work with Device structs:

- **Change return type**: `map[string]string` → `[]Device`
- **Merge results**:
  - Launch DLNA discovery in goroutine
  - Launch Chromecast discovery in goroutine
  - Collect results as they arrive (non-blocking)
  - Append to combined slice
- **Sorting**:
  - Call `sortDevices(combined)` before returning
  - Ensures consistent ordering: DLNA first, then Chromecast

### GUI State Management

#### [NEW] [device_ui_adapter.go](file:///home/alex/test/go2tv/internal/gui/device_ui_adapter.go)

Create UI state management functions:

- **lockFFmpegControls(screen *FyneScreen)**:
  - Disable `TranscodeCheckBox` widget
  - Used for Chromecast devices

- **unlockFFmpegControls(screen *FyneScreen)**:
  - Enable `TranscodeCheckBox` widget
  - Used for DLNA devices

- **setTranscodeForDeviceType(screen *FyneScreen, deviceType string)**:
  - Switch based on `deviceType`
  - **Chromecast**:
    - Store current transcode preference in `previousTranscodePref`
    - Set `Transcode = true`
    - Call `TranscodeCheckBox.SetChecked(true)`
    - Call `lockFFmpegControls(screen)`
  - **DLNA**:
    - Restore `Transcode = previousTranscodePref`
    - Call `TranscodeCheckBox.SetChecked(previousTranscodePref)`
    - Call `unlockFFmpegControls(screen)`
  - **Default**: Unlock controls

### GUI Desktop Integration

#### [MODIFY] [gui.go](file:///home/alex/test/go2tv/internal/gui/gui.go)

Add device type tracking:

- **Update FyneScreen struct**:
  - Add `selectedDeviceType string` field
  - Add `previousTranscodePref bool` field

- **Update devType struct**:
  - Add `deviceType string` field

- **Initialize preferences** in `Start()`:
  - Set `previousTranscodePref = false` (starts unchecked each session)
  - No persistence to settings

#### [MODIFY] [actions.go](file:///home/alex/test/go2tv/internal/gui/actions.go)

Update device loading:

- **Update getDevices()**:
  - Change to work with `[]devices.Device` return type
  - Convert to `[]devType` with explicit deviceType field
  - Remove sorting (now done in devices package)

#### [MODIFY] [main.go](file:///home/alex/test/go2tv/internal/gui/main.go)

Update device selection logic:

- **Remove tooltip widget**:
  - Remove `transcodeTooltip` label creation
  - Remove `tooltipContainer` from UI layout
  - No explanatory messages needed

- **Update list.OnSelected callback**:
  - Set `s.selectedDevice = data[id]`
  - Set `s.selectedDeviceType = data[id].deviceType`
  - Call `setTranscodeForDeviceType(s, data[id].deviceType)`
  - **Only call DMRextractor() for DLNA devices**:
    - Check `if data[id].deviceType == devices.DeviceTypeDLNA`
    - This prevents errors when selecting Chromecast (no SOAP)

- **Update transcode.OnChanged callback**:
  - Only update `previousTranscodePref` for DLNA devices
  - Check `if s.selectedDeviceType == devices.DeviceTypeDLNA`
  - No persistence to settings (runtime state only)

### GUI Mobile Integration

#### [MODIFY] [gui_mobile.go](file:///home/alex/test/go2tv/internal/gui/gui_mobile.go)

Add device type tracking (same as desktop):

- **Update FyneScreen struct**:
  - Add `selectedDeviceType string` field
  - Add `previousTranscodePref bool` field
  - Add `TranscodeCheckBox *widget.Check` field

- **Update devType struct**:
  - Add `deviceType string` field

#### [MODIFY] [main_mobile.go](file:///home/alex/test/go2tv/internal/gui/main_mobile.go)

Update device selection logic (same as desktop):

- **Update getDevices()**:
  - Change to work with `[]devices.Device` return type
  - Convert to `[]devType` with explicit deviceType field
  - Remove sorting (now done in devices package)

- **Update list.OnSelected callback**:
  - Set `s.selectedDevice = data[id]`
  - Set `s.selectedDeviceType = data[id].deviceType`
  - **Only call DMRextractor() for DLNA devices**:
    - Check `if data[id].deviceType == devices.DeviceTypeDLNA`

### CLI Integration

#### [MODIFY] [cmd/go2tv/go2tv.go](file:///home/alex/test/go2tv/cmd/go2tv/go2tv.go)

Update CLI to work with Device structs:

- **Update listFlagFunction()**:
  - **IMPORTANT**: Chromecast discovery requires background loop to be running
  - Create bubbletea-based dynamic device list with auto-refresh
  - Add local `Device` struct for CLI display (separate from devices.Device)
  - Create `listDevicesModel` struct implementing `tea.Model`:
    - `devices []Device` - current device list
    - `Init()` - calls `devices.StartChromecastDiscoveryLoop(context.Background())`
      - **Critical**: This starts Chromecast mDNS discovery
    - `checkDevices()` - calls `devices.LoadAllDevices(2)` to fetch devices
    - `Update()` - handles refresh messages and auto-refreshes every 2 seconds
    - `View()` - formats device list display
  - Replace static list with dynamic bubbletea program:
    - Shows "Scanning devices... (q to quit)" initially
    - Auto-refreshes device list every 2 seconds
    - Devices appear as they are discovered (DLNA and Chromecast)
  - Display format: `"• Device Name [URL]"` for each device

- **Update checkTflag()**:
  - Change `devices.LoadSSDPservices(1)` to `devices.LoadAllDevices(1)`
  - Call `DevicePicker()` with slice instead of map

**Key Discovery Behavior**:
- **Chromecast devices only appear after `StartChromecastDiscoveryLoop()` is running**
  - Static list call would show 0 Chromecast devices
  - Background loop continuously discovers via mDNS
  - `Init()` method in tea.Model starts this loop
- **Auto-refresh**: Device list updates every 2 seconds as devices are discovered
- **DLNA devices**: Appear immediately (SSDP is synchronous discovery)
- **Chromecast devices**: Appear as mDNS discovers them (may take a few seconds)

#### [MODIFY] [cmd/go2tv-lite/go2tv.go](file:///home/alex/test/go2tv/cmd/go2tv-lite/go2tv-lite.go)

Same updates as go2tv (CLI consistency):

- **Update listFlagFunction()**: Same changes as go2tv
- **Update checkTflag()**: Same changes as go2tv

---

## Verification Plan

### Automated Tests

#### Unit Tests for Device Type Management

**New file**: `devices/device_test.go`

Test device struct and type constants:
- `TestDevice_StructFields()` - verify Name, Addr, Type fields exist
- `TestDeviceType_Constants()` - verify DLNA and Chromecast constants
- `TestSortDevices_TypeOrdering()` - verify DLNA before Chromecast
- `TestSortDevices_AlphabeticalWithinType()` - verify alphabetical sorting

**Command**: `go test -v ./devices -run TestDevice`

#### Unit Tests for UI State Management

**New file**: `internal/gui/device_ui_adapter_test.go`

Test transcode control behavior:
- `TestSetTranscodeForDeviceType_Chromecast()` - verify checkbox locked/enabled
- `TestSetTranscodeForDeviceType_DLNA()` - verify checkbox unlocked/restored
- `TestLockFFmpegControls()` - verify checkbox disabled
- `TestUnlockFFmpegControls()` - verify checkbox enabled

**Command**: `go test -v ./internal/gui -run TestSetTranscode`

### Manual Verification

#### Test 1: Chromecast Device Selection

**Prerequisites**: Chromecast device on network

**Steps**:
1. Build and run Go2TV: `make build && ./build/go2tv`
2. Select a Chromecast device from list
3. Verify "Transcode" checkbox is automatically checked
4. Verify "Transcode" checkbox is disabled (grayed out)
5. Verify no error message appears for DMRextractor
6. Select a DLNA device from list
7. Verify "Transcode" checkbox is restored to previous state
8. Verify "Transcode" checkbox is enabled (interactive)

**Expected Result**: UI adapts seamlessly between device types, no errors.

#### Test 2: DLNA Functionality Unchanged

**Prerequisites**: DLNA device on network

**Steps**:
1. Build and run Go2TV: `make build && ./build/go2tv`
2. Select a DLNA device from list
3. Verify "Transcode" checkbox is enabled
4. Toggle "Transcode" checkbox on/off
5. Verify preference remembered when switching between DLNA devices
6. Select media file and play
7. Verify normal DLNA playback works

**Expected Result**: All existing DLNA functionality works without regression.

#### Test 3: CLI Device List

**Prerequisites**: DLNA and Chromecast devices on network

**Steps**:
1. Run CLI: `./build/go2tv -list`
2. Verify device list shows both DLNA and Chromecast devices
3. Verify each device shows Type field
4. Verify DLNA devices appear before Chromecast devices
5. Verify alphabetical sorting within each type

**Expected Result**: CLI correctly displays all device types with proper formatting.

### Build Verification

**Command**: `make build`

**Expected Result**: Clean build with no errors or warnings for:
- `cmd/go2tv` (desktop CLI)
- `cmd/go2tv-lite` (lite CLI)
- All GUI targets

**Command**: `make test`

**Expected Result**: All existing tests pass, new tests pass.

---

## Implementation Notes

**Device Type Tracking Strategy**:
- **Explicit typing**: Device.Type field is authoritative source of truth
- **No string parsing**: Never check for " (Chromecast)" or " (DLNA)" suffixes
- **Visual distinction**: Suffix preserved in Name field for UI readability
- **Type constants**: Centralized in devices package, imported where needed

**Runtime State Management**:
- **No persistence**: Transcode preference not saved to settings
- **Per-session memory**: `previousTranscodePref` resets to false on app start
- **Type-aware switching**: Preference remembered when switching between DLNA devices
- **Type-forced state**: Chromecast always enables transcode (user can't disable)

**UI Adaptation Flow**:
1. User selects device from list
2. `list.OnSelected` callback fires
3. Device type extracted from `data[id].deviceType`
4. `setTranscodeForDeviceType()` called with type
5. Based on type:
   - Chromecast: Enable+lock transcode, store old preference
   - DLNA: Restore old preference, unlock transcode
6. DLNA only: Call `DMRextractor()` for SOAP URLs
   - Chromecast skips this (no SOAP protocol yet)

**Sorting Strategy**:
- Primary sort: Device type (DLNA < Chromecast)
- Secondary sort: Device name (alphabetical, case-insensitive)
- Consistent ordering across GUI and CLI
- Devices package handles sorting, not UI layer

**Error Prevention**:
- Chromecast selection no longer calls `DMRextractor()` (avoids SOAP errors)
- Device type check: `if data[id].deviceType == devices.DeviceTypeDLNA`
- Early return: Only DLNA devices attempt DLNA-specific operations

**Chromecast Discovery Architecture**:
- **Background loop**: `StartChromecastDiscoveryLoop()` must be running for devices to appear
- **mDNS protocol**: Continuously browses `_googlecast._tcp` service every 10 seconds
- **Device cache**: Stores discovered devices in package-level map
- **Health checking**: Removes stale devices every 5 seconds
- **CLI requirement**: `-l` flag uses bubbletea with `Init()` starting discovery loop
  - `Init()` calls `devices.StartChromecastDiscoveryLoop(context.Background())`
  - Auto-refreshes device list every 2 seconds as devices are discovered
  - DLNA devices appear immediately (synchronous SSDP)
  - Chromecast devices appear as mDNS discovers them (may take ~5-10 seconds)
- **GUI requirement**: `gui.Start()` calls discovery on app startup
  - `go devices.StartChromecastDiscoveryLoop(ctx)` runs in background
  - Devices appear in list continuously updated

**Bubbletea CLI Implementation**:
- **Dynamic list**: Replaces static device listing with auto-refreshing UI
- **tea.Model pattern**: `listDevicesModel` implements Init, Update, View methods
- **Auto-refresh**: `tea.Tick(time.Second*2)` triggers device list refresh every 2 seconds
- **Discovery startup**: `Init()` method starts Chromecast discovery loop
- **Display format**: `"• Device Name [URL]"` with color support
- **Interactive**: Press 'q' to quit, list refreshes automatically

**Code Organization**:
- **devices package**: Core abstraction, Device struct, discovery, sorting
- **internal/gui/device_ui_adapter.go**: UI state management functions
- **internal/gui/**: Desktop and mobile GUI implementations
- **cmd/go2tv**: CLI implementations using Device struct with bubbletea
