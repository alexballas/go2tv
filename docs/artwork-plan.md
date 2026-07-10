# Music Artwork Implementation Plan

Status: Phase 4 complete

Owner: primary integration agent

## Goal

Automatically send correct per-track music artwork to DLNA and Chromecast renderers without changing playback behavior when artwork is absent or invalid.

## MVP scope

- Automatic artwork; no picker, flag, or manual path.
- Local MP3, M4A/MP4, FLAC, Ogg Vorbis, and Opus embedded covers.
- Same-directory JPEG/PNG sidecar discovery.
- JPEG normalization.
- Desktop, mobile, full CLI, lite CLI.
- DLNA current/next metadata.
- Chromecast initial/reload metadata.
- Correct per-track queue artwork.
- Artwork failure never blocks audio playback.

Deferred:

- Remote URL artwork discovery.
- Explicit artwork picker/CLI flag.
- Artist/album tag propagation beyond fields needed by protocol models.
- Formats without embedded-art parser support.

## Fixed decisions

- Artwork is optional and per media item.
- Local artwork resolution is automatic.
- Resolution order:
  1. Same-directory `<track-stem>.<ext>`.
  2. Embedded image marked front cover.
  3. First valid embedded image.
  4. Same-directory named sidecar, in order: `cover`, `folder`, `front`, `albumart`, `album`, `artwork`, `albumartlarge`, `albumartsmall`, `thumb`.
  5. Windows Media Player pattern `albumart_*_large`, then `albumart_*_small`.
- Sidecar extensions: `.jpg`, `.jpeg`, `.png`.
- Sidecar matching is case-insensitive on every OS.
- Scan the media directory only. No recursion and no arbitrary first-image fallback.
- For the same basename, select largest valid pixel area; tie-break `.jpg`, `.jpeg`, `.png`.
- Local artwork is served by Go2TV; filesystem paths never enter protocol metadata.
- Receiver URL format: `http://<listen-address>/artwork/<sha256>.<ext>`.
- Content hash gives cache-safe URLs and reuse for identical artwork.
- Accept only discovered, valid `image/jpeg` and `image/png` artwork in MVP.
- Normalize to JPEG, maximum 600x600 bounding box, preserving aspect ratio.
- Do not upscale images smaller than 600x600.
- Encode at JPEG quality 88; flatten transparency onto black.
- Limit input to 20 MiB and decoded size to 40 million pixels.
- Serve normalized bytes with `image/jpeg`, GET, HEAD, and CORS.
- Chromecast uses music metadata type `3`; artwork is `images[0]`.
- DLNA uses `upnp:albumArtURI` in DIDL-Lite.
- Omit DLNA `dlna:profileID` until a compliant derived image is generated.
- Existing public Chromecast load methods remain as compatibility wrappers.
- Remote media continues without artwork; do not download whole remote media to inspect tags.
- Shared contracts freeze after Phase 1. Later agents do not redesign them.

## Shared contracts

Add top-level `metadata` package. It may use one maintained pure-Go tag parser. FFmpeg must not be required.

```go
package metadata

type Artwork struct {
	URL      string
	MIMEType string
	Width    int
	Height   int
}

type Media struct {
	Title       string
	Artist      string
	Album       string
	AlbumArtist string
	Artwork     *Artwork
}

type ArtworkAsset struct {
	Data      []byte
	MIMEType string
	Extension string
	Width     int
	Height    int
	ID        string
	Source    string
}
```

Expected helpers:

```go
func ResolveArtwork(mediaPath string) (*ArtworkAsset, error)
func LoadArtwork(data []byte, source string) (*ArtworkAsset, error)
func (a *ArtworkAsset) HandlerPath() string
```

Rules:

- `ID` is lowercase SHA-256 of bytes.
- `ResolveArtwork` returns `(nil, nil)` when no usable artwork exists.
- Candidate read/parse errors are non-fatal; continue through remaining candidates.
- Sidecar discovery uses exact case-insensitive basenames from the fixed list, plus only the two anchored Windows Media Player patterns.
- Embedded selection prefers a front-cover type, then first valid supported image.
- Embedded support target: MP3 `APIC`, M4A/MP4 `covr`, FLAC `PICTURE`, Ogg/Opus `METADATA_BLOCK_PICTURE`.
- Extension is always `.jpg`; MIME is always `image/jpeg`.
- Decode JPEG/PNG, preserve aspect ratio, and normalize. Ignore EXIF orientation in MVP.
- Width/height describe normalized output.
- Reject empty files, unsupported images, input over 20 MiB, and decoded images over 40 million pixels.
- Hash normalized JPEG bytes so identical output reuses one URL.

HTTP addition:

```go
func (s *HTTPserver) AddStaticHandler(path, mediaType string, media any)
```

It adds explicit MIME without changing existing `AddHandler` behavior. Static handlers support files, bytes, and mobile-safe fresh readers.

Chromecast addition:

```go
type LoadRequest struct {
	MediaURL   string
	ContentType string
	Metadata   metadata.Media
	StartTime  int
	Duration   float64
	SubtitleURL string
	Live       bool
}

func (c *CastClient) LoadMedia(req LoadRequest) error
func (c *CastClient) LoadMediaOnExisting(req LoadRequest) error
```

Existing `Load` and `LoadOnExisting` delegate with title-only metadata.

DLNA addition:

```go
type TVPayload struct {
	// existing fields
	Metadata metadata.Media
}
```

Only `Metadata.Artwork.URL` enters DIDL. `ArtworkAsset.Data` remains owned by the HTTP layer.

## Required payloads

DLNA audio item:

```xml
<item id="1" parentID="0" restricted="1">
  <dc:title>track.mp3</dc:title>
  <upnp:class>object.item.audioItem.musicTrack</upnp:class>
  <upnp:albumArtURI>http://host/artwork/hash.jpg</upnp:albumArtURI>
  <res protocolInfo="...">http://host/track.mp3</res>
</item>
```

Chromecast audio metadata:

```json
{
  "metadataType": 3,
  "title": "track.mp3",
  "images": [
    {
      "url": "http://host/artwork/hash.jpg",
      "width": 600,
      "height": 600
    }
  ]
}
```

## Phase plan

### Phase 1: foundation

Owner: primary agent only

Work:

- Add `metadata` package and table-driven tests.
- Select one maintained pure-Go tag parser; document exact format coverage and license.
- Implement deterministic, case-insensitive same-directory sidecar discovery.
- Implement embedded front-cover extraction for target audio formats.
- Normalize every selected candidate through one pipeline.
- Treat missing, corrupt, or unsupported artwork as no artwork.
- Add typed static HTTP handler.
- Test discovery precedence, embedded selection, JPEG/PNG normalization, dimensions, hash, size limits, invalid data, GET, HEAD, MIME, CORS.
- Fix image DLNA profiles in `utils/dlnatools.go` to include `DLNA.ORG_PN=`.
- Preserve every existing API and behavior.

Exit:

- Shared contracts compiled and documented.
- No playback call sites migrated.
- All verification gates pass.
- Commit: `artwork discovery core and static serving`

### Phase 2: protocol payloads

Owner: primary integrator; DLNA and Chromecast agents may run in parallel.

DLNA agent ownership:

- `soapcalls/soapcallers.go`
- `soapcalls/soapcalls.go`
- `soapcalls/soapbuilders.go`
- DLNA tests

DLNA work:

- Add protocol-neutral metadata to payload/options.
- Refactor duplicated current/next DIDL construction into one helper.
- Add optional title/artist/album/album-art fields.
- Preserve fields in subtitle branches.
- Omit artwork when next URI is cleared.
- Cover standard and Samsung compatibility escaping.

Chromecast agent ownership:

- `castprotocol/media.go`
- `castprotocol/loader.go`
- `castprotocol/client.go`
- Chromecast tests

Chromecast work:

- Add image JSON type with optional dimensions.
- Use metadata type `3` for `audio/*`; retain current non-audio behavior.
- Add request-based load methods and old-method wrappers.
- Ensure artwork alone forces custom LOAD.
- Preserve artwork through existing-receiver loads.

Exit:

- Exact XML/JSON unit tests pass with and without artwork.
- Agents changed no shared foundation/UI files.
- All verification gates pass.
- Commits: `add DLNA artwork metadata`; `add Chromecast artwork metadata`

### Phase 3: CLI vertical slice

Owner: primary agent

Work:

- Resolve artwork automatically from each local media path.
- Keep stdin and remote URL playback artwork-free in MVP.
- Resolve and normalize asset before server start.
- Register artwork handler before SOAP/Cast load.
- Populate protocol metadata URL/dimensions.
- Keep no-art CLI behavior identical.

Acceptance:

- Local audio + embedded artwork works for DLNA and Chromecast.
- Local audio + sidecar artwork works for DLNA and Chromecast.
- Remote/stdin audio continues without artwork.
- Invalid artwork is ignored and audio still plays.
- No artwork remains fully backward compatible.

Exit:

- Automated CLI-path tests where practical.
- Manual payload inspection recorded in handoff.
- All verification gates pass.
- Commit: `add CLI music artwork`

### Phase 4: GUI integration

Owners: desktop agent, mobile agent, primary integrator.

Desktop agent ownership:

- Desktop `internal/gui` files only.
- Do not edit queue/gapless code in this phase.

Mobile agent ownership:

- Mobile `internal/gui` files only.
- Use `fyne.URI`; extract embedded art from a seekable reader or existing temporary media copy.
- Scan sidecars only when the URI exposes a safely accessible filesystem directory.

Work:

- Resolve artwork automatically when preparing local audio.
- Keep external URL playback artwork-free in MVP.
- Register artwork on every newly created server.
- Preserve artwork across pause/resume and transcoded seek restart.
- Avoid new UI controls or translations.
- All artwork work runs off the UI thread; UI changes still use `fyne.Do`.

Exit:

- Current item automatically displays correct cover on both protocols.
- Changing to a no-art file clears stale metadata.
- Desktop and mobile state tests pass.
- All verification gates pass.
- Commits: `integrate desktop artwork discovery`; `integrate mobile artwork discovery`

### Phase 5: queue and lifecycle

Owner: primary agent only

Work:

- Resolve/cache artwork by target media identity; do not store user-entered art paths.
- Selecting an item resolves its art or clears stale metadata.
- DLNA `queueNext` registers next art before `SetNextAVTransportURI`.
- Chromecast skip/previous/next uses target item art.
- Seek/reload/loop retains current art.
- Keep old and next artwork handlers during gapless transition.
- Remove unused handlers only after no renderer can request them.
- Test two consecutive tracks with distinct covers.

Exit:

- Current/next cover never leaks between tracks.
- Same cover reuses same hash URL.
- All verification gates pass.
- Commit: `preserve artwork across queues`

### Phase 6: optional explicit override

Status: deferred; requires explicit approval.

Only add picker, CLI flag, or remote artwork URL if automatic embedded/sidecar resolution proves insufficient. Any explicit source would take precedence over automatic discovery.

### Phase 7: compatibility verification

Owner: primary agent; optional read-only reviewer agent.

Work:

- Review URL/XML escaping and cache behavior.
- Review public API compatibility.
- Review mobile URI/resource lifetime.
- Test representative Chromecast, Chromecast Audio/Nest, and DLNA hardware.
- If hardware needs DLNA profiles, generate compliant derived art before adding `dlna:profileID`.
- Run full verification suite.

Exit:

- Hardware results recorded below.
- Known renderer exceptions documented.
- Commit: fixes only, if needed.

## Playback-path checklist

- [ ] DLNA CLI initial load
- [ ] Chromecast CLI initial load
- [ ] DLNA desktop initial load
- [ ] Chromecast desktop initial load
- [ ] DLNA mobile initial load
- [ ] Chromecast mobile initial load
- [ ] Track-stem sidecar precedence
- [ ] Embedded front-cover fallback
- [ ] Generic named sidecar fallback
- [ ] Remote/stdin no-art behavior
- [ ] Chromecast transcoded reload/seek
- [ ] Chromecast warm-session previous/next
- [ ] DLNA `SetNextAVTransportURI`
- [ ] Loop selected
- [ ] Queue tracks with different covers
- [ ] Art track followed by no-art track
- [ ] Audio-only Chromecast device
- [ ] No-art regression

## Test matrix

Unit:

- Artwork resolver: case-insensitive names, track stem, fixed generic names, no recursion, no arbitrary image, precedence, no-art result.
- Embedded extractor: front-cover preference and first-valid fallback for each supported audio container.
- Artwork loader: JPEG, PNG, misleading extension, normalized JPEG output, aspect ratio, no upscale, transparency, size limits, invalid bytes, empty file.
- HTTP: GET, HEAD, explicit MIME, CORS, unknown path, repeated request.
- DLNA: absent/present art, URL query escaping, subtitles, legacy encoding, next, clear next.
- Chromecast: audio type `3`, image first, dimensions, no art, non-audio, existing receiver.
- Queue: restore/clear per item, distinct hashes, shared hash.

Integration:

- Capture SOAP body with test server.
- Capture Cast LOAD JSON with fake connection.
- Verify artwork route is registered before load command.
- Verify server starts when artwork is its only local resource.

Hardware results:

| Device | Protocol | Current art | Next art | Notes |
|---|---|---:|---:|---|
| TBD | DLNA | TBD | TBD | |
| TBD | Chromecast | TBD | TBD | |
| TBD | Cast audio | TBD | TBD | |

## Verification gates

Run at every phase:

```bash
go test -v ./...
make build
go run cmd/fynedo-check/main.go internal/gui/
ANDROID_HOME=/home/alex/Android/Sdk ANDROID_NDK_HOME=/home/alex/Downloads/android-ndk-r27d FYNE='go run github.com/alexballas/refyne/v2/cmd/fyne@latest' make android
```

Run before final completion:

```bash
make windows
go run golang.org/x/tools/gopls/internal/analysis/modernize/cmd/modernize@latest -test ./...
```

## Agent rules

- Primary agent owns architecture, shared contracts, integration, and plan updates.
- One agent per bounded file set.
- No concurrent edits to same files.
- Agents do not edit `go.mod`, shared metadata, HTTP contracts, or this plan unless assigned.
- Agents run focused tests and report exact commands/results.
- Primary agent reviews diffs and runs full gates.
- Use read-only reviewer agent after integration, not during shared API work.

Agent prompt template:

```text
Read AGENTS.md and docs/artwork-plan.md fully. Implement Phase <N>, task <task> only.
Owned files: <files>. Do not edit other files or redesign shared contracts.
Preserve unrelated work. Use apply_patch. Run focused tests.
Report outcome, files changed, tests, risks. Extremely concise.
```

## Session/context protocol

Use one fresh session per phase. Continue same branch.

Session start:

1. Read `AGENTS.md` and this file.
2. Inspect `git status --short` and recent commits.
3. Confirm current phase and fixed contracts.
4. Run phase baseline tests.
5. Work only current phase.

Session end: replace the handoff block below. Do not append transcripts.

Keep context durable through code, commits, tests, and this file. Do not rely on prior chat history.

## Current handoff

Phase: 4

State: complete

Last commit: `integrate mobile artwork discovery` (this commit)

Completed:

- Added GUI artwork state, local-audio resolution, content-addressed registration, and receiver metadata construction without UI controls/translations.
- Desktop DLNA and Chromecast initial loads resolve/register artwork before server start; external/invalid/no-art loads clear stale artwork.
- Desktop pause/resume keeps current artwork; Chromecast transcoded seek recreates the route and reloads identical metadata.
- Mobile uses `fyne.URI`: filesystem URIs allow sidecars; content URIs use isolated seekable-descriptor links or the existing temp media copy for embedded art only.
- Mobile artwork work runs through background playback actions; DLNA/Chromecast servers register artwork before serving.
- Added GUI state tests for local/no-art/external/non-audio replacement, normalized metadata, CORS route serving, and server restart retention.
- Preserved queue/gapless code and shared contracts; no Phase 5 work started.
- Passed `go test -v ./...`, `make build`, Fyne check, refyne Android package/sign/verify, and `make windows`.
- Repo-wide modernize ran; reports seven pre-existing findings in untouched files.

Next:

1. Start Phase 5 in fresh session only.
2. Add per-target queue artwork lifecycle.
3. Keep current/next handlers through transitions.

Known risks:

- Queue/gapless lifecycle remains deferred to Phase 5.
- Mobile seekable-descriptor artwork extraction is package-verified, not hardware-verified.
- DLNA artwork has no `dlna:profileID` by fixed design.
- Modernize findings remain in Phase 1/2 and unrelated files.
- Hardware compatibility remains unverified.

## Unresolved questions

- Hardware targets available?
