# Building Go2TV

See [README.md](README.md#building-from-source) for requirements and the basic `make build`.

## AppImage builds (Linux)

``` console
make appimage          # Without FFmpeg
make appimage-ffmpeg   # With FFmpeg
```

Release AppImages are built on Ubuntu 22.04 (glibc 2.35) for compatibility with
older systems and named `go2tv_vX.Y.Z_amd64.AppImage`. The lowercase prefix and
underscores match other release artifacts; `linux` is omitted because AppImages
are Linux-only. Build on Ubuntu 22.04 when distributing local AppImages;
building on a newer system can require a newer glibc.

Standalone Linux amd64 and arm64 releases use the same Ubuntu 22.04 baseline.
ARMv6 releases use a pinned Raspberry Pi OS Bookworm image (glibc 2.36), which
provides the Wayland and PipeWire headers required by current dependencies.
CI tests Go packages on both Ubuntu 22.04 and the latest Ubuntu runner.

For the FFmpeg build, `APPIMAGE_FFMPEG_MODE` supports `auto` (default), `system`,
`download`, or `none`. Downloaded builds pin a month-end FFmpeg 8.1.2 build for
Pascal/NVENC compatibility. BtbN retains month-end builds for two years; daily
builds expire after 14 releases. Override binary paths with `APPIMAGE_FFMPEG_BIN`
and `APPIMAGE_FFPROBE_BIN`, or override the archive with `APPIMAGE_FFMPEG_URL` and
`APPIMAGE_FFMPEG_SHA256`.

Release AppImages include update information for AppImageUpdate and a matching
`.AppImage.zsync` asset. Local builds omit update information by default. To
generate the pair locally, install `zsyncmake` and set `APPIMAGE_UPDATE_INFO`
and `APPIMAGE_OUT` (using the final release filename) when running `make`.

## Android builds

``` console
make android
```

`make android` builds the arm64 APK with MediaCodec hardware encoding and
libx264 software fallback. Set `ANDROID_HOME` to the SDK and `ANDROID_NDK_HOME`
to NDK r27d. Git, Make, a C build toolchain, and pkg-config are also required.

The first build fetches official FFmpeg/x264 sources at the revisions pinned in
`Makefile`, then compiles them for Android API 21. Later builds reuse the binaries
unless the sources, NDK, API, ABI, or build script change. Set `FFMPEG_BUILD_JOBS`
to limit compiler concurrency. `make android-source` remains an alias; the old
third-party binary-download mode has been removed.

CI calls the same `make android` target and includes the corresponding source
archives from `make android-source-archives`. Required transcoding features and
16 KB ELF alignment are checked before packaging. GPL notices and exact source
revisions are embedded in the APK.

To supply binaries built separately:

``` console
make android ANDROID_FFMPEG_MODE=local \
  ANDROID_FFMPEG_BIN=/path/to/ffmpeg \
  ANDROID_FFPROBE_BIN=/path/to/ffprobe
```

Keep `FFmpeg-GPL-2.0.txt`, `x264-GPL-2.0.txt`, and `build-info.txt` beside FFmpeg.
For alternative source revisions, override `ANDROID_FFMPEG_REF` and
`ANDROID_X264_REF` with full commit hashes. Existing checkouts can be supplied
with `ANDROID_FFMPEG_SOURCE_DIR` and `ANDROID_X264_SOURCE_DIR`; they must match
the pins and have no tracked changes. Source trees live under `build/android-sources`
by default; changing a pin selects a fresh checkout without modifying an old one.

Set `ANDROID_SIGN=false` to produce a zip-aligned unsigned APK for downstream
packagers such as F-Droid. Signed builds remain the default.

## Using Docker

Build the image:
``` console
docker build --force-rm -t go2tv .
```

Run the container (Linux with X11):
``` console
xhost +local:docker
docker run -it --network host -e DISPLAY=$DISPLAY -v /tmp/.X11-unix:/tmp/.X11-unix go2tv go2tv
```
