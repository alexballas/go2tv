# F-Droid submission plan

Handoff for a new session after Android device testing. Target first F-Droid
release: `v2.7.0`; application ID: `app.go2tv.go2tv`.

## Current state

- Go2TV source: MIT.
- Android package: `app.go2tv.go2tv`.
- Release APK target SDK: 35; minimum SDK: 21.
- Android APK is arm64-only and bundles `ffmpeg` and `ffprobe`.
- FFmpeg and x264 build from pinned official source revisions:
  - FFmpeg: `239f2c733de417201d7ad3b3b8b0d9b63285b2b1`
  - x264: `b35605ace3ddf7c1a5d67a2eb553f034aef41d55`
- FFmpeg is built with `--enable-gpl --enable-libx264`; non-free components are
  disabled. The installable APK's effective license is `GPL-2.0-or-later`.
- FFmpeg/x264 license notices and build information are embedded in the APK.
- `make android-source ANDROID_SIGN=false` creates the unsigned APK F-Droid
  needs.
- The source-build workflow creates signed and unsigned artifacts. Releases
  retain the established `go2tv_vX.Y.Z.apk` and
  `go2tv_vX.Y.Z_android.zip` names.
- Expected upstream signing certificate SHA-256:
  `a2bf409bbd333227eb3b5550e62e191527f68deffd90674bf7cdeb2fcdb37546`.
- Existing fdroiddata source libraries can fetch the pinned upstream sources:
  `FFmpeg` and `x264`.

## 1. Finish device acceptance testing

- [ ] Upgrade the GitHub-signed APK over `v2.6.1`; confirm no signer mismatch.
- [ ] Clean-install on Android 9 or newer.
- [ ] Confirm the old-target Android warning is gone.
- [ ] Test DLNA discovery, playback, pause, seek, stop and reconnect.
- [ ] Test Chromecast discovery and playback.
- [ ] Force a transcode and confirm the x264 encoder works.
- [ ] Test local files, URLs, subtitles and Android share/open intents.
- [ ] Test background/foreground transitions and notification controls.
- [ ] Check crash/log output and battery/network permissions.

Record device model, Android version and pass/fail results in issue #161.

## 2. Choose the F-Droid signing path before submission

Prefer an upstream-signed reproducible build. It lets F-Droid publish the same
certificate used by GitHub, so users can move between distribution channels
without uninstalling.

### Preferred: reproducible upstream-signed APK

- [ ] Pin the complete build environment used by both GitHub and fdroiddata:
  Go, JDK, Android SDK/build-tools, NDK r27d, locale, timezone, source refs and
  packaging commands.
- [ ] Use build-tools 34 `apksigner` for the reproducibility experiment; F-Droid
  documents signature-copying problems with newer apksigner versions.
- [ ] Build twice in clean directories and compare unsigned APKs.
- [ ] Compare the F-Droid-built unsigned APK with the unsigned form of the
  published upstream APK using `apksigcopier`/`diffoscope`.
- [ ] Remove nondeterminism: Go build IDs/paths, ZIP ordering/timestamps,
  generated manifest/resources and native build paths.
- [ ] Configure fdroiddata only after byte-for-byte verification:
  - `Binaries: https://github.com/alexballas/go2tv/releases/download/v%v/go2tv_v%v.apk`
  - `AllowedAPKSigningKeys:` with the SHA-256 fingerprint above.

### Fallback: F-Droid signing key

- [ ] Omit `Binaries` and `AllowedAPKSigningKeys`.
- [ ] Clearly document that GitHub APK users must uninstall before installing
  the F-Droid build because Android rejects a different signer for the same ID.

Do not switch signing strategies after users install the first F-Droid release.

## 3. Prepare the first tagged release

- [ ] Change `version.txt` from `2.7.0-dev` to `2.7.0`.
- [ ] Add the `2.7.0` AppStream release notes.
- [ ] Run the full Go test suite and Android source build.
- [ ] Verify release APK metadata:
  - version name `2.7.0`
  - version code `2070099`
  - target SDK 35
  - signer fingerprint matches the expected certificate
  - arm64 ELF LOAD alignment is 16 KiB
  - `libffmpeg.so`, `libffprobe.so` and license notices exist
- [ ] Tag the exact release commit `v2.7.0` and publish the GitHub release.
- [ ] Confirm release assets keep these names:
  - `go2tv_v2.7.0.apk`
  - `go2tv_v2.7.0_android.zip`

F-Droid build metadata must use the full tagged commit SHA, not a branch or tag.

## 4. Add upstream store metadata

Create:

```text
fastlane/metadata/android/en-US/short_description.txt
fastlane/metadata/android/en-US/full_description.txt
fastlane/metadata/android/en-US/changelogs/2070099.txt
fastlane/metadata/android/en-US/images/icon.png
fastlane/metadata/android/en-US/images/phoneScreenshots/1.png
fastlane/metadata/android/en-US/images/phoneScreenshots/2.png
```

- [ ] Keep the short description below 80 characters, without a trailing dot.
- [ ] Keep the changelog below 500 characters.
- [ ] Capture real Android screenshots; do not reuse desktop-only screenshots.
- [ ] Confirm all icons/screenshots are redistributable and document their
  license if it is not already clear.

## 5. Create the fdroiddata recipe

Fork `fdroid/fdroiddata`, create branch `app.go2tv.go2tv`, then add
`metadata/app.go2tv.go2tv.yml`.

Initial shape:

```yaml
Categories:
  - Multimedia
License: GPL-2.0-or-later
WebSite: https://go2tv.app
SourceCode: https://github.com/alexballas/go2tv
IssueTracker: https://github.com/alexballas/go2tv/issues

RepoType: git
Repo: https://github.com/alexballas/go2tv.git

Builds:
  - versionName: 2.7.0
    versionCode: 2070099
    commit: REPLACE_WITH_FULL_V2.7.0_COMMIT_SHA
    timeout: 10800
    target: android-35
    ndk: r27d
    srclibs:
      - FFmpeg@239f2c733de417201d7ad3b3b8b0d9b63285b2b1
      - x264@b35605ace3ddf7c1a5d67a2eb553f034aef41d55
    build:
      - export ANDROID_HOME=$$SDK$$ ANDROID_NDK_HOME=$$NDK$$
      - make android-source ANDROID_SIGN=false
        ANDROID_FFMPEG_SOURCE_DIR=$$FFmpeg$$
        ANDROID_X264_SOURCE_DIR=$$x264$$
    output: build/Go2TV.apk

AutoUpdateMode: None
UpdateCheckMode: Tags ^v[0-9]+\.[0-9]+\.[0-9]+$
CurrentVersion: 2.7.0
CurrentVersionCode: 2070099
```

Recipe work still required:

- [ ] Confirm the F-Droid VM has Go 1.26 and every native build prerequisite.
- [ ] Add only necessary Debian packages under `sudo`.
- [ ] Make Go module fetching work in the permitted dependency-fetch phase;
  keep `go.sum` verification enabled and avoid unpinned downloads.
- [ ] Confirm refyne's packaging CLI is built from the exact version in
  `go.mod`.
- [ ] Run the scanner before adding any narrowly scoped `scanignore`; explain
  every exception in the merge request.
- [ ] Decide whether the Chromecast interoperability needs disclosure in
  `MaintainerNotes`; it should not require `NonFreeNet` because DLNA/local
  functionality does not depend entirely on a proprietary service.
- [ ] Add `Binaries`/`AllowedAPKSigningKeys` only if reproducibility succeeds.

Start with manual updates. Custom version-code arithmetic and pinned multimedia
source revisions make automatic build-block generation risky. Automate only
after adding a machine-readable release version code and validating one update.

## 6. Validate in fdroidserver

Run from the fdroiddata checkout:

```sh
fdroid rewritemeta app.go2tv.go2tv
fdroid lint app.go2tv.go2tv
fdroid build --verbose --server app.go2tv.go2tv:2070099
```

- [ ] Run the F-Droid scanner with no unexplained findings.
- [ ] Confirm the build uses official pinned FFmpeg/x264 source, not downloaded
  executables.
- [ ] Confirm `CONFIG_GPL=1`, `CONFIG_NONFREE=0` and x264 GPL mode.
- [ ] Inspect the unsigned APK with `aapt`, `apksigner`, `zipalign`, `unzip` and
  `llvm-readelf`.
- [ ] Install the F-Droid-signed test APK or verified upstream APK on a device
  and repeat the core playback/transcoding smoke test.
- [ ] If reproducible, run F-Droid's signature-copy verification against
  `go2tv_v2.7.0.apk` and archive the result in the merge request.

## 7. Submit and maintain

- [ ] Push the fdroiddata branch and let its GitLab CI pass.
- [ ] Open the fdroiddata merge request; link issue #161 and the source-build
  workflow run.
- [ ] State that the combined Android binary is GPL-2.0-or-later and identify
  the exact FFmpeg/x264 revisions.
- [ ] Respond to scanner, licensing and reproducibility review comments.
- [ ] After publication, add the F-Droid link/badge to the README and website.
- [ ] Close issue #161 only after the package is visible in the main index.
- [ ] For every release, keep the Git tag, version name/code, source revisions,
  fdroiddata build block and release asset URL synchronized.

## References

- [F-Droid submission guide](https://f-droid.org/en/docs/Submitting_to_F-Droid_Quick_Start_Guide/)
- [F-Droid inclusion policy](https://f-droid.org/en/docs/Inclusion_Policy/)
- [Build metadata reference](https://f-droid.org/en/docs/Build_Metadata_Reference/)
- [Reproducible builds](https://f-droid.org/en/docs/Reproducible_Builds/)
- [fdroiddata contributing guide](https://gitlab.com/fdroid/fdroiddata/-/blob/master/CONTRIBUTING.md)
- [Go2TV F-Droid request](https://github.com/alexballas/go2tv/issues/161)

## Unresolved questions

- Reproducible upstream signing or F-Droid key?
- Which Android devices/versions passed?
- F-Droid VM Go 1.26 available?
- Chromecast disclosure requested by reviewer?
