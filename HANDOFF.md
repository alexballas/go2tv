# Phase 0 handoff

## Changes

- Shared `internal/servermode`: CLI/config validation, canonical roots/origins, exact Host/origin boundary, bootstrap/session cookie, authenticated `/api`, safe DTO contracts, desktop runner, mobile unsupported stub.
- Both commands dispatch `-server` immediately after parse. Added `-listen`, repeatable `-media-root`, repeatable `-allowed-origin`.
- `make build-lite`; consistent version ldflags. Server-lite release builds: Linux/macOS/Windows. Package workflows build `./cmd/go2tv`. Go toolchains aligned to module Go 1.26.
- Tests: flag matrix/shared wiring, roots/listens/origins, Host rebinding/mismatch, cookie/secret, DTO leaks, piped stdin.

## Decisions

- Listen host must IP or `localhost`; non-loopback/wildcard requires explicit HTTP origin(s).
- Allowed hosts come only from loopback defaults and configured origins. No request-Host derivation.
- Session is process-life random cookie. Cookie is sole admitted CSRF/shared-control mechanism; never exposed in JSON/URL/storage/logs.
- Phase 0 server exposes security/bootstrap scaffold only. No controller, media lifecycle, library, WebUI protocol, or static UI.

## Next phase

- Build controller/library behind safe DTO mapping; keep internal device/queue/transport types out of JSON.
- Register API/WebSocket handlers behind existing security wrapper.
- Preserve canonical root boundary and stable public error codes; log details server-side only.

## Verification

- `go test ./...`
- `make build-lite`
- Server-lite cross-build: linux/darwin/windows amd64, CGO disabled.
- Required refyne Android build passed; APK created.

## Unresolved

- None.

# Phase 6 completion-audit gap

## Missing

- Embedded client currently exposes only device refresh/select, media select, and play/pause/stop.
- Add usable UI/state handling for subtitle selection, queue add/remove/move/select, seek, volume, mute, transcode, loop/autoplay/same-type/gapless/image duration, artwork, live playback state/progress/duration, pending/errors/toasts.
- Preserve strict DOM-only untrusted label insertion, protocol/version behavior, stable IDs, and all security/cache/WS limits.

## Evidence

- `internal/webui/dist/index.html` has no queue, subtitle, policy, seek, volume, mute, transcode, artwork, or progress controls.
- `internal/webui/src/app.js` handles only `state.snapshot`, `pending`, `ack`, `error`, and `server.shutdown`; it does not render partial state events or queue/playback/selection/policy payloads.

## Next

- Phase 6 UI specialist must close this gap, rebuild hashed assets, add DOM/interaction/state tests, then rerun all Phase 6 and platform gates.

# Phase 6 completion repair

## Changes

- Replaced minimal shell with responsive Tailwind/Preline client: discovery, nested root browsing, media/subtitle selection, stable-ID queue add/select/play/move/remove, full player controls, policies, artwork, live playback/time/volume state.
- Added complete snapshot and partial devices/queue/playback/selection/policy rendering; pending/ack/error/toast/shutdown handling; reconnect pending reset; reload-once protocol mismatch.
- Kept all server-provided labels/messages in DOM `textContent`; no HTML insertion APIs.
- Added deterministic CSS/JS content-hash build and source HTML pipeline.
- Added executable DOM/interaction tests covering safe labels, command payloads/stable IDs, partial state, pending/errors/reconnect/shutdown, and version mismatch.

## Verification

- `npm run test:webui`; consecutive `npm run build:webui` stable.
- `go test ./...`; focused race including WebUI/controller/playback/devices/HTTP.
- `make build`; `make build-lite`; both binaries root-smoked from empty cwd.
- Lite Linux/macOS/Windows amd64 cross-builds, CGO disabled.
- Fyne check; lite graph no Fyne/refyne; `git diff --check`.
- Required refyne Android build passed; signed `build/Go2TV.apk` verified.

## Unresolved

- None.

# Phase 0 completion-audit gap

## Missing

- Non-loopback startup must print every exact canonical media root and a warning that roots may enter console/journal logs; current code prints only root count.
- Expand both command-local flag matrix tests so each command independently covers all server conflicts/server-only flags/positional cases, not only a thin wiring sample.

## Next

- Phase 0 specialist fixes only logging/tests, reruns full Phase 0 and regression/platform gates, appends repair evidence.

# Phase 0 completion repair

## Changes

- Startup logging now prints every exact canonical media root and explicitly warns root paths may enter console/journal logs.
- Both command-local table suites independently cover default/explicit listen, repeatable required roots/origins, every legacy conflict, every server-only flag without server, positional arguments, invalid/non-loopback listen cases, and duplicate/overlapping roots.
- Added exact non-loopback startup-output coverage for listen address, usable allowed URLs, trusted-LAN/no-TLS warning, canonical roots, and log-disclosure warning.

## Verification

- Focused servermode and both command tests passed.
- Focused race passed for servermode and both commands.
- `go test ./...`; `make build`; `make build-lite`.
- Lite Linux/macOS/Windows amd64 cross-builds, CGO disabled.
- Fyne check; lite graph no Fyne/refyne; `git diff --check`.
- Android not rerun: desktop-only logging and host command tests; mobile runtime/build wiring unchanged.

## Unresolved

- None.

# Phase 6 WS completion-audit gap

## Missing evidence

- Add deterministic tests proving the global 16-client cap, slow-client disconnect behavior, and ping/pong timeout disconnect.
- Existing tests prove per-IP limit and state coalescing only; those do not prove these three explicit requirements.

## Next

- Phase 6 specialist adds testable timing/configuration only as needed, no behavior weakening, then reruns full/race/platform gates and appends evidence.

# Phase 6 WS completion repair

## Changes

- Added deterministic global 16-client cap test using four clients/source IP.
- Added gated-writer queue overflow test proving slow client gets policy-violation close.
- Added short injected hub timings test proving ping sent, missing pong times out, client removed.
- Runtime defaults unchanged: write 15s, pong 60s, ping 25s.

## Verification

- Missing WS tests: 20 consecutive passes; focused race: 3 consecutive passes.
- Phase 6 focused race suite passed.
- `go test ./...`
- `npm run test:webui`; `npm run build:webui`
- `make build`; `make build-lite`
- Fyne check passed; lite dependency graph contains no Fyne/refyne.
- Android skipped: Android `cmd/go2tv` dependency graph does not reach `internal/webui`.
- `git diff --check`

## Unresolved

- None.

# Phase 6 binary-smoke completion-audit gap

## Missing evidence

- Plan requires an added smoke test that runs the built server binary from an empty working directory.
- Manual smoke evidence exists, but no automated test/script artifact currently proves this on future runs.

## Next

- Phase 6 specialist adds durable smoke coverage for both desktop commands where platform permits, verifies embedded shell/bootstrap without cwd assets, then reruns gates.

# Phase 6 binary-smoke completion handoff

## Changes

- Added desktop-only automated binary smoke for `cmd/go2tv` and `cmd/go2tv-lite`.
- Each subtest builds the real command, starts server mode from an empty cwd with an unread open stdin pipe and ephemeral loopback listener, then verifies embedded shell, both hashed assets, bootstrap JSON, and strict HttpOnly `/api` session cookie through a cookie jar.
- Test uses isolated media/state/temp directories, bounded builds/startup/HTTP, kill plus wait cleanup, and confirms cwd remains empty.

## Verification

- Binary smoke passed three consecutive runs, then passed after final cookie assertions.
- `go test ./internal/servermode`; focused `go test -race` passed.
- `go test ./...`
- `npm run test:webui`; `npm run build:webui`
- `make build`; `make build-lite`
- Fyne check passed; lite dependency graph contains no Fyne/refyne.
- `git diff --check`
- Android skipped: test-only desktop build-tagged change; no production/mobile code changed.

## Unresolved

- None.

# Phase 1 handoff

## Changes

- Added pure `internal/mediamodel`: private queue storage, cloned snapshots, mutation/traversal API, private item paths, read-only accessors, stable random IDs.
- Moved media classification, immutable extension defaults, queue item construction, sorting, and platform display-path handling from GUI.
- Desktop/mobile GUI constructors and subtitle/media pickers now consume shared extension snapshots. Fyne widgets/renderers/thumbnails/artwork remain in `internal/gui`.
- Moved pure queue/display-path coverage to `internal/mediamodel`; retained GUI integration/UI tests.

## Decisions

- Duplicate paths remain distinct entries with distinct IDs. Path lookup returns first match.
- Removing current item clears current index. Removing before current and moving entries preserve current item identity.
- Extension APIs return clones; membership/classification is case-insensitive.

## Next phase

- Build controller/library against `mediamodel.Queue` methods and `QueueItem` accessors.
- Map to safe DTOs explicitly; never serialize model items or expose `Source()`/`Path()`.
- Do not reintroduce writable queue slices or GUI/Fyne dependencies below GUI.

## Verification

- `go test ./...`
- `go list -deps ./cmd/go2tv-lite`: 288 packages; no Fyne/refyne matches.
- Required refyne Android build passed; `build/Go2TV.apk` created and verified.

## Unresolved

- None.

# Phase 2 handoff

## Changes

- Added Fyne-free playback contracts: injectable discovery, DLNA/Chromecast transports, media server, clock, random, artwork.
- Added instance discovery services: serialized contextual refresh, continuous start, snapshots/notifications, stable lifetime opaque IDs, latest-ID selection.
- Hardened renderer URLs: HTTP/S only, no userinfo, directly connected pinned IP, same-IP services, pinned dials, cross-host redirect denial, 1 MiB description/SOAP caps.
- Added immutable callback events and bounded session worker: strict NOTIFY headers/source/body, 256 KiB cap, SID+SEQ order/wrap/dedupe/gap, stale generation and explicit-stop suppression, 503 backpressure.
- Moved legacy TVPayload/screen mutation and unsubscribe out of callback parser into compatibility sink.
- Added DLNA/Chromecast/image monitors, seek engine for four paths, Chromecast subtitle routing/conversion/burn policy, Fyne-free DLNA gapless engine.

## Decisions

- Callback gaps invoke injected state-query/resubscribe recovery and omit uncertain event.
- Only `finished` permits loop/autoplay. Other terminal reasons never advance.
- Transcoded seek stops old server synchronously, preserves active target, reloads existing transport connection where supported.
- SRT and VTT direct Chromecast subtitles use random `.vtt` routes; transcoded subtitles burn in.
- Gapless requires DLNA plus autoplay. Disable clears NextURI; clear failure stops playback.

## Next phase

- Controller should own these interfaces, generations, monitors, callback session, subtitle cleanup, and gapless lifecycle.
- Use opaque discovery IDs only. Never expose `Device.Addr` or playback `Device.Endpoint` in DTOs.
- Replace legacy GUI callback facade when controller owns subscription lifecycle.

## Verification

- `go test ./...`
- Focused race tests: discovery, callbacks, playback, SOAP.
- `go run cmd/fynedo-check/main.go internal/gui/`
- Lite dependency graph contains no Fyne/refyne.
- Required refyne Android build passed; `build/Go2TV.apk` verified.
- `git diff --check`

## Unresolved

- None.

# Phase 3 handoff

## Changes

- Added Fyne-free `internal/controller`: single-writer actor, bounded command/callback ingress, immutable snapshots, monotonic revisions, request-correlated safe results.
- Added selected-vs-active device targeting, root media/subtitle intent, stable-ID queue mutations, transcode-next-load, atomic policy validation, playback/session state, terminal reasons, sanitized errors.
- Added generation-reserved async playback/control/refresh IO, cancellation/stale suppression, stop-before-load cancellation, ordered replacement teardown, active-session controls, selected-device volume/mute.
- Added monitor/callback ingress, image timer, autoplay/loop/same-type traversal, audio-only followup rejection, queue-removal independence.
- Added 64 MiB byte-bounded artwork LRU with per-ID singleflight.
- Added race-focused controller tests: invariants/conflicts, active targeting, replacement ordering, pending cancellation, queue mutation, audio-only followup, artwork coalescing/eviction.

## Decisions

- Controller snapshots are internal contracts and may contain root-scoped paths; public server DTO mapping must continue excluding them.
- Active target is captured by value. Selection changes/disappearance never retarget active controls or autoplay.
- Playback mutation reserves generation in actor; all transport/media-server IO runs outside actor. Replacement order: transport stop, server stop, transport close, then open/start/load/play.
- Gapless policy requires DLNA + autoplay. Actual DLNA preloading remains injected through session monitor/runtime integration, not GUI state.
- Image duration `0` disables controller timer. Default remains 10 seconds.

## Next phase

- Map controller snapshots/results to safe API DTOs; never serialize paths, endpoints, transports, or model items.
- Instantiate protocol adapters/media server/monitor through controller config; keep callback session queue at 128.
- On WebSocket reconnect/bootstrap, discard client-local pending request state.

## Verification

- `go test ./...`
- `go test -race ./internal/controller ./internal/playback ./devices ./httphandlers`
- `go run cmd/fynedo-check/main.go internal/gui/`
- Lite dependency graph contains no Fyne/refyne.
- Required refyne Android build passed; `build/Go2TV.apk` created and verified.
- `git diff --check`

## Unresolved

- None.

# Phase 4 handoff

## Changes

- Added Fyne-free `internal/library`: canonical non-overlapping roots retained as process-life `os.Root` handles; random public root IDs and sanitized display-only names.
- Added HMAC entry IDs covering root ID plus raw relative filename bytes. Enforced 4 KiB decode cap; rejected tamper, absolute/traversal/hidden paths, escapes, special files, and unsupported extensions while preserving invalid UTF-8 names.
- Added root-confined fresh reopen operations for select/play/autoplay/direct/transcode plus opened sidecar/artwork adapters. Every open verifies regular type after open; Unix opens nonblocking to reject swapped FIFOs safely.
- Added incremental filesystem-order pagination: default 100/max 200, per-request scan cap, 60-second cursor TTL, 32 live maximum, mutation/rename invalidation, and close on exhaustion/expiry/shutdown.
- Added containment/pagination tests: duplicate/overlap roots, traversal/hidden/tamper/non-UTF8, mixed-case extensions/special files, symlink and rename swaps, retained renamed roots, scan cap/order/TTL/mutation/cursor cap/cleanup, and fresh direct/transcode handles.

## Decisions

- Entry IDs represent root-relative names, not file identity. Safe in-root replacement is visible on reopen; outside-root symlink replacement fails.
- Cursor order is raw incremental filesystem order. Unsupported/hidden entries consume scan budget but never appear.
- Subtitle/artwork discovery accepts only derived sibling extensions and returns already-open files; no library write/mutation API exists.

## Next phase

- API maps only `Root`, `Entry`, and pagination fields; never expose `os.File`, internal relative names, canonical paths, or raw errors.
- Direct routes call `OpenDirect` per HTTP request. Transcoding passes `OpenTranscode`'s existing file to FFmpeg; never convert it to `File.Name()`/path.
- Close library during server shutdown to release cursors and root handles.

## Verification

- `go test ./...`
- `go test -race ./internal/library`
- Windows library cross-compile.
- `go run cmd/fynedo-check/main.go internal/gui/`
- Lite dependency graph contains no Fyne/refyne.
- Required refyne Android build passed; `build/Go2TV.apk` created and verified.
- `git diff --check`

## Unresolved

- None.

# Phase 5 handoff

## Changes

- Added Fyne-free `internal/mediaserver` implementing `playback.MediaServer`: listener-first single bind, actual advertised address, explicit routes registered before serve, Ready/Done observability, and retained Serve errors.
- Added independent 32-byte crypto-random session, media, subtitle, callback, and opaque route-ID values. Renderer URLs expose only tokens plus validated compatibility extensions; caller IDs and filenames never enter routes.
- Added immediate route removal/session revocation, stale-route rejection after restart, GET/HEAD/range/method handling, random NOTIFY callback coexistence, and content-addressed artwork with immutable cache/ETag.
- Added header/read/idle limits with no streaming write timeout. Idempotent Stop cancels active requests/transcodes, closes readers/listener/connections, clears handlers, and waits server/request goroutines.
- Extended `MediaRoute` with `SubtitleURL`; controller passes it to transport load. Start registers direct subtitles before serving.
- Raised renderer-description response cap to 2 MiB. SOAP response cap remains 1 MiB.
- Added race-tested lifecycle coverage: actual/bind address, token rotation/revocation/stale routes, no filename/caller-ID routes, subtitle readiness, GET/HEAD/range/method/callback, stop during transcode, repeat Stop, Done, request cleanup, artwork addressing/cache.

## Decisions

- Media server receives a fresh confined opener; it never derives filesystem access from route text. Phase 4 library adapters should supply `OpenDirect`/already-open handles.
- Transcode adapter owns FFmpeg execution and must honor request context. Stop cancels context and closes input before waiting.
- Renderer server URLs are renderer-only contracts. Web/API DTOs must not expose them or use them for browser artwork/media.
- `Start` rejects an existing live session. Replacement lifecycle remains controller stop-then-start.

## Next phase

- Runtime composition should instantiate `mediaserver.Server` with library fresh-open and FFmpeg adapters, then inject it into controller. Keep renderer URLs out of browser DTOs.
- Callback handler should be the bounded Phase 2 callback session for DLNA sessions; Chromecast may omit callback registration.
- If direct SRT is used for Chromecast, convert through existing VTT preparation before Start/Add.

## Verification

- `go test ./...`
- Focused race: media server, controller, playback, HTTP callbacks, SOAP.
- `go run cmd/fynedo-check/main.go internal/gui/`
- Lite dependency graph contains no Fyne/refyne.
- Required refyne Android build passed; `build/Go2TV.apk` created and verified.
- `git diff --check`

## Unresolved

- None.

# Phase 6 blocker

## Missing prerequisite APIs

- Controller has no seek command/API. Required semantics cannot be implemented: integer seconds, duration/capability validation, DLNA/Chromecast adapter conversion.
- Controller has no stable-ID queue add/select API. `SetQueue([]source)` rebuilds queue and IDs, violating stable-ID-only commands and preserving existing IDs.
- Library returns confined open files, but controller selection/play contracts accept source strings and media server composition has no library opener adapter. Passing display names would break confinement/runtime playback.
- No desktop server runtime composition exists for discovery, transport factory, media server, monitor/callback, artwork loader. A nil controller can expose shell/API but cannot satisfy end-to-end commands.

## Partial tree state

- Added unintegrated `internal/webui`: embedded shell/hashed assets, HTTP handler, DTO mapping, initial WS hub/command decoder.
- Added Gorilla WebSocket v1.5.3 dependency.
- `internal/servermode` remains Phase 0 scaffold; WebUI not wired. Partial WebUI has not been compiled/tested and must not ship.

## Decision

- Stopped; no unsupported-command workaround and no out-of-phase controller expansion.

# Phase 2 repair handoff

## Changes

- Added production `internal/playbackadapter`: fresh discovery scanner, DLNA/Chromecast transports/factory, validated callback bridge, protocol monitor runner, legacy Fyne-free artwork resolver.
- DLNA adapter serializes contextual TVPayload calls and preserves existing SOAP retries, POST/M-POST, metadata compatibility, stop-before-load fallback, logging, subscriptions, endpoint pinning, volume/mute, seek, gapless, and teardown.
- Chromecast adapter preserves CastClient retry/reconnect/logging behavior and exposes contextual controller/seek/monitor methods.
- Added fresh bounded device scan API. DLNA description loads inherit scan context; mDNS and SSDP waits remain bounded by protocol timeouts.
- Exported narrow TVPayload URI/subscription wrappers. Controller transport contract now aliases shared playback transport contract for direct factory injection.

## Verification

- Adapter/controller/device/SOAP focused tests passed.
- Focused race passed: playback adapter, playback, devices, callbacks, SOAP, controller.
- Fyne check passed. Lite dependency graph has no Fyne/refyne. Android build passed; APK verified.
- `git diff --check` passed.
- Full `go test ./...` blocked only by pre-existing Phase 6 partial WebUI compile error: `internal/webui/handler.go:98:22: h.cfg.ControllerArtwork undefined`.

## Next

- Phase 6 runtime composition can inject `playbackadapter.Scanner`, `Factory`, `RunMonitor`, `CallbackBridge`, and `mediaserver.Server` directly.
- Library-confined media/artwork opener composition remains Phase 6 prerequisite; do not pass display paths to legacy artwork resolver.
- Remaining cross-phase interface: media server must receive active renderer target before `Start` (for route-local listener IP). Current controller `ServerRequest` has no target and server defaults to loopback. Add target-aware pre-Start hook or target field; no Phase 2 workaround added.

# Phase 3 repair handoff

## Changes

- Added opener-backed, root-scoped media/subtitle refs. Controller and media server no longer accept source/display paths; renderer routes reopen only confined handles.
- Replaced destructive queue setter with stable-ID add/select/clear mutations. Add returns typed item ID; existing IDs remain unchanged.
- Added integer-second controller seek with duration/protocol capability validation, active-target preservation, controller generation rollover, stale monitor suppression, and Phase 2 `SeekEngine` execution.
- Captured active renderer in media-server start request. Default listener now binds route-local IP derived from renderer endpoint before route creation; explicit listen remains test/config override.
- Added controller runtime config composing production discovery, transport factory, callback provider, and monitor adapters; no servermode/WebUI wiring.

## Verification

- Focused tests and race passed: controller, media server, playback, playback adapter.
- Fyne check passed. Lite dependency graph contains no Fyne/refyne.
- Android build passed; APK created and verified.
- `git diff --check` passed.
- Full `go test ./...` blocked only by Phase 6 partial WebUI: missing `ControllerArtwork`; old string arguments no longer satisfy `controller.MediaRef`/`controller.SubtitleRef`.

## Next

- Phase 6 must build refs from library root/entry IDs using fresh `OpenDirect`/`OpenTranscode`/sidecar closures; never use metadata names as sources.
- Update WebUI commands to typed queue add/select/clear and seek results, then compose `mediaserver.Server` plus callback bridge through `NewRuntimeConfig`.

# Phase 6 handoff

## Changes

- Added `internal/webui`: embedded generated shell, content-hashed Tailwind/Preline assets, vanilla ES module client, sanitized bootstrap/library/artwork APIs, exact cache policy and security headers.
- Added strict versioned WebSocket envelopes and typed controller command mapping: device refresh/select, confined media/subtitle select, stable-ID queue add/remove/move/select, play/pause/stop/seek/volume/mute/transcode, policy.
- Added bounded WS hub: 64 KiB text messages, strict fields/types/nesting, 16 global/4-IP limits, 32 outbound queue, state coalescing/slow disconnect, request-ID dedupe, ping/pong deadlines, reconnect snapshots, shutdown notification/close.
- Added safe state snapshot/devices/queue/playback/selection/policy, pending/ack/error/toast/shutdown events. Frontend reloads once on protocol mismatch and only inserts untrusted labels with `textContent`.
- Replaced Phase 0 server scaffold with production desktop composition: retained library, playback adapters/callback bridge, renderer media server, controller, WebUI, confined direct/transcode openers, artwork cache, Chromecast SRT-to-VTT, transcoded subtitle burn.
- Added HTTP limits, exact Host/origin/session boundary, WebSocket origin requirement, query/ID-redacted route-template access logs, graceful shutdown. Both binaries pass version into server bootstrap.
- Added reproducible npm build inputs/lock for Tailwind CSS, Preline UI, esbuild; only generated hashed assets embedded.

## Decisions

- Browser artwork IDs are SHA-256 content IDs mapped process-locally to confined sibling openers. Browser never receives renderer URLs, roots, paths, endpoints, or raw errors.
- Actual listener authority seeds loopback Host/origin checks, allowing safe ephemeral-port tests.
- Chromecast direct SRT is converted from confined handle to in-memory VTT. Transcoded media remains an already-open confined handle; subtitle burn uses a short-lived private temp copy because FFmpeg filters require a filename.
- Server shutdown closes WebUI clients/resources, controller/session, callback bridge, then library. Ordinary WebSocket disconnect only removes that client.

## Verification

- `go test ./...`
- `go test -race ./internal/webui ./internal/servermode ./internal/controller ./internal/playback ./internal/playbackadapter ./internal/mediaserver`
- `make build`; `make build-lite`; both binaries server-smoked from empty cwd.
- Linux/macOS/Windows amd64 lite cross-builds with CGO disabled.
- Fyne check passed; lite dependency graph contains no Fyne/refyne.
- Required refyne Android build passed; signed APK verified.
- `git diff --check`

## Unresolved

- None.
