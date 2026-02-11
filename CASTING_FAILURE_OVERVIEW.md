# Casting Failure Overview (DLNA)

Date: 2026-02-11

## Current State

- Discovery works.
- Unit tests pass (`go test -v ./soapcalls ./devices`).
- Real DLNA playback still fails on device.

## Changes In Current Branch

### `soapcalls/soapbuilders.go`

- `Pause`/`Stop` no longer send `Speed`.
- Removed title stripping regex (`[&<>\\]+`) in DIDL title.
- Removed `&amp; -> &` rewrite.
- Kept Samsung quote rewrite (`&#34; -> "`).
- Added `html.UnescapeString(...)` before embedding DIDL metadata.

### `soapcalls/xmlparsers.go`

- Service URL assembly changed to helper `toAbsoluteServiceURL(...)`.
- Non-absolute paths forced to host-root (`scheme://host/...`).
- Absolute URLs (`http://`, `https://`) passthrough.

## Most Likely Remaining Regression Points

1. DIDL `html.UnescapeString(...)` step  
   Could alter entity forms some renderers expect.

2. Samsung quote rewrite (`&#34; -> "`)  
   Global byte rewrite still brittle.

3. Service URL normalization strategy  
   Some renderers may need RFC path-relative resolution, not forced host-root.

4. Soft SOAP error handling still in place  
   Failure may be masked, root cause harder to see.

## Fast Triage Plan

1. Capture one failing `Play1` sequence:
   - `GetProtocolInfo` req/resp
   - `SetAVTransportURI` req/resp
   - `Play` req/resp
2. Confirm failing step + status code + SOAP fault body.
3. A/B minimal patches in order:
   - A: remove `html.UnescapeString(...)` only.
   - B: remove `&#34; -> "` rewrite only.
   - C: toggle service URL mode (host-root vs RFC resolve).

## Required Artifacts

- Debug logs around:
  - `setAVTransportSoapCall`
  - `AVTransportActionSoapCall` (`Play`)
- Device LOCATION XML used for service extraction.
- Device model + firmware.

## Intent Kept

- No user workflow / UX changes intentionally.
- Goal remains compatibility-only.
