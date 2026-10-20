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

`make android` builds the arm64 APK and bundles Android `ffmpeg`/`ffprobe` executables as native libraries. Set `ANDROID_NDK_HOME` and `ANDROID_HOME`.

To compile the bundled FFmpeg tools from an official source checkout:

``` console
git clone --branch n8.1.1 --depth 1 https://github.com/FFmpeg/FFmpeg.git ffmpeg-source
git clone --branch stable --depth 1 https://code.videolan.org/videolan/x264.git x264-source
make android-source \
  ANDROID_FFMPEG_SOURCE_DIR="$PWD/ffmpeg-source" \
  ANDROID_X264_SOURCE_DIR="$PWD/x264-source"
```

`android-source` performs no dependency downloads. It builds x264, then a GPL
FFmpeg arm64 configuration with both MediaCodec and libx264 using the NDK. It
verifies required transcoding features and 16 KB ELF alignment before packaging.
The source-build workflow pins FFmpeg, x264, and the official Android NDK. GPL
notices and exact source revisions are embedded in the APK and uploaded with the
corresponding source archives.

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
