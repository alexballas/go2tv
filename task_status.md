# Chromecast V2 Integration Task Breakdown

## Planning Phase
- [x] Analyze existing codebase architecture
- [x] Research Chromecast V2 protocol requirements
- [x] Create phased implementation roadmap
- [x] Create detailed implementation plan
- [x] Get user approval on plan

## Phase 1: Core Infrastructure & Device Discovery ✅
- [x] Create Chromecast mDNS discovery module
- [x] Implement LoadAllDevices with non-blocking async
- [x] Update GUI to use unified device list
- [x] Add zeroconf dependency and run go mod tidy
- [x] Test device discovery (build successful)

## Phase 2: Device Selection & UI Behavior ✅
- [x] Add selectedDeviceType field to FyneScreen struct (desktop + mobile)
- [x] Create proper Device struct in devices package with Type field
- [x] Update devType struct to include device type (desktop + mobile)
- [x] Create device_ui_adapter.go with UI state management
- [x] Implement lockFFmpegControls() and unlockFFmpegControls()
- [x] Update device selection logic to track device type from discovery
- [x] Use explicit device type (no string suffix checks)
- [x] Keep " (Chromecast)" suffix for visual distinction in UI
- [x] Auto-select transcode for Chromecast devices
- [x] Disable transcode checkbox when Chromecast selected (no messages)
- [x] Runtime state management (no persistence needed)
- [x] Update all CLI and GUI code to use new Device struct
- [x] Test build and verify compilation successful
