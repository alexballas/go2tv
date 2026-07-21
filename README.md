<br/>
<p align="center">
<img src="assets/go2tv-logo-color.svg" width="225" alt="Go2TV logo">
</p>
<br/>
<div align="center">
<p>

[![Release Version](https://img.shields.io/github/v/release/alexballas/Go2TV?label=Release)](https://github.com/alexballas/Go2TV/releases/latest)
[![Tests](https://github.com/alexballas/go2tv/actions/workflows/go.yml/badge.svg?branch=devel)](https://github.com/alexballas/go2tv/actions/workflows/go.yml)

[![Build for ARMv6 (32-bit)](https://github.com/alexballas/go2tv/actions/workflows/build-arm.yml/badge.svg?branch=devel)](https://github.com/alexballas/go2tv/actions/workflows/build-arm.yml)
[![Build for ARMv8 (64-bit)](https://github.com/alexballas/go2tv/actions/workflows/build-arm64.yml/badge.svg?branch=devel)](https://github.com/alexballas/go2tv/actions/workflows/build-arm64.yml)
[![Build for Android](https://github.com/alexballas/go2tv/actions/workflows/build-android.yml/badge.svg?branch=devel)](https://github.com/alexballas/go2tv/actions/workflows/build-android.yml)
[![Build for Linux](https://github.com/alexballas/go2tv/actions/workflows/build-linux.yml/badge.svg?branch=devel)](https://github.com/alexballas/go2tv/actions/workflows/build-linux.yml)
[![Build for MacOS Intel](https://github.com/alexballas/go2tv/actions/workflows/build-mac-intel.yml/badge.svg?branch=devel)](https://github.com/alexballas/go2tv/actions/workflows/build-mac-intel.yml)
[![Build for MacOS Apple Silicon](https://github.com/alexballas/go2tv/actions/workflows/build-mac.yml/badge.svg?branch=devel)](https://github.com/alexballas/go2tv/actions/workflows/build-mac.yml)
[![Build for Windows](https://github.com/alexballas/go2tv/actions/workflows/build-windows.yml/badge.svg?branch=devel)](https://github.com/alexballas/go2tv/actions/workflows/build-windows.yml)
</p>
Cast media files to Smart TVs and Chromecast devices.
</div>

---

## What is Go2TV?

Go2TV lets you play video, audio, and image files on your Smart TV or Chromecast device directly from your computer or phone. It works with:

- **Smart TVs** - Samsung, LG, Sony, and others that support DLNA/UPnP
- **Chromecast** - Google Chromecast, Chromecast with Google TV, and compatible devices
- **Apps** - BubbleUPnP, GMediaRender, and other media receiver apps

No need to copy files to a USB drive or set up a media server. Just select your file, pick your device, and play.

## Features

- **Auto-discovery** - Automatically finds Smart TVs and Chromecast devices on your network
- **Transcoding** - Converts incompatible video formats on-the-fly (requires FFmpeg)
- **Subtitles** - Supports external SRT/VTT files and embedded MKV subtitles
- **Seek support** - Jump to any position in the video
- **Playlist playback** - Single-file and multi-file playlists with add/remove/reorder/select support
- **Loop and auto-play** - Loop the current file or auto-play through the playlist
- **Gapless playback** - Supported for DLNA devices
- **RTMP Server** - Cast live streams from OBS directly to Chromecast (requires FFmpeg)
- **Cast Desktop (experimental)** - Cast desktop as live stream to Chromecast (requires FFmpeg)
- **Web UI (server mode)** - Browse media roots and control casting from any browser on your network
- **GUI and CLI** - Use the graphical interface or command line

---

## Screenshots

**GUI Mode**

![](https://i.imgur.com/6hwB4DC.png)
![](https://i.imgur.com/dQ40FWb.png)

**CLI Mode**

![](https://i.imgur.com/BsMevHi.gif)

**Web UI (Server Mode)**

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/webui-dark.png">
  <source media="(prefers-color-scheme: light)" srcset="assets/webui-light.png">
  <img alt="Go2TV Web UI" src="assets/webui-dark.png">
</picture>

---

## Installation

**macOS**: Install with Homebrew:

```bash
brew install --cask go2tv
```

**Linux**: Install from [Flathub](https://flathub.org/en/apps/app.go2tv.go2tv):

```bash
flatpak install flathub app.go2tv.go2tv
```

Or **download the latest release** from the [releases page](https://github.com/alexballas/Go2TV/releases/latest).

- **Windows GUI**: Download `go2tv_vX.Y.Z_windows_amd64.zip`, extract, and run `go2tv.exe`
- **Windows CLI**: Download `go2tv_vX.Y.Z_windows_amd64_lite.zip`, extract, and run `go2tv-lite.exe`
- **macOS**: Download `go2tv_vX.Y.Z_macOS_amd64.zip` (Intel) or `go2tv_vX.Y.Z_macOS_arm64.zip` (Apple Silicon), extract, and run the app
- **Linux**: Download `go2tv_vX.Y.Z_linux_amd64.zip` (or appropriate arch), extract, and run `go2tv`
- **Android**: Download `go2tv_vX.Y.Z.apk` for the arm64 build with bundled FFmpeg transcoding

For release downloads, Go2TV is a single executable with no installation required. Just download and run.

### Optional: FFmpeg for Transcoding

For maximum compatibility with all devices and file formats, install [FFmpeg](https://ffmpeg.org/download.html). Go2TV will automatically use it when needed.
When transcoding is enabled, Go2TV probes available GPU H.264 encoders first and falls back to `libx264` if hardware encoding is unavailable or fails at startup.

- **Linux**: `sudo apt install ffmpeg` or equivalent for your distro
- **macOS**: `brew install ffmpeg`
- **Windows**: Download from [ffmpeg.org](https://ffmpeg.org/download.html) and add to PATH
- **Flatpak**: FFmpeg is bundled automatically
- **Android**: FFmpeg is bundled in the default APK. It is arm64-only and requires Android 9.0+.

---

## Usage

### GUI

Select a media file (or drag and drop it onto the main window), pick a device from the list, and click **Play**.

#### Playlist

The **Playlist** window lets you add, remove, reorder, and select files. Dragging files onto
the main window replaces the playlist; dragging them onto the Playlist window appends them.
**Next**, **Previous**, and **Auto-Play Next File** follow the playlist order.

### CLI

Run `go2tv -h` for all options. Common commands:

``` console
# List available devices
go2tv -l

# Play a local file
go2tv -v movie.mp4 -t http://192.168.1.100:8060/

# Play with subtitles and transcoding
go2tv -v movie.mkv -s movie.srt -tc -t http://192.168.1.100:8060/

# Play a remote file
go2tv -u https://example.com/movie.mp4 -t http://192.168.1.50:8009

# Stream from another command
yt-dlp -o - "https://youtu.be/..." | go2tv -t http://192.168.1.50:8009
```

### Web UI (Server Mode)

Run Go2TV as a web server to browse selected media folders and control casting from a
browser. The Web UI has separate device, playlist, and playback state from the desktop GUI.

From the GUI, open **Settings → Remote Web Session…**, add media folders, choose local or
LAN access, and start the session.

For headless use, pass `-server` and at least one `-media-root`:

``` console
go2tv -server -media-root /path/to/Media
```

The default URL is `http://127.0.0.1:9666/`. Repeat `-media-root` to expose multiple
directories:

``` console
go2tv -server \
  -media-root /path/to/Movies \
  -media-root /path/to/Music \
  -media-root "/path/to/TV Shows"
```

For LAN access, set a non-loopback `-listen` address and allow each browser origin:

``` console
go2tv -server \
  -listen 0.0.0.0:9666 \
  -allowed-origin http://192.168.1.20:9666 \
  -media-root /path/to/Media
```

> LAN mode uses HTTP without TLS. Use it only on trusted networks.

### RTMP Streaming (Chromecast only)

Select a Chromecast, enable **RTMP Server**, and click **Play**. Use the displayed URL in
OBS or other streaming software. Requires FFmpeg.

### Cast Desktop (Experimental, Chromecast only)

Select a Chromecast, enable **Cast Desktop (experimental)**, and click **Cast**. Requires
FFmpeg and is not supported on audio-only Chromecast devices.

---

## Supported File Types (GUI)

mp4, avi, mkv, mpeg, mov, webm, m4v, mpv, mp3, flac, wav, jpg, jpeg, png

The CLI accepts any file type.

---

## Companion Project: mcp-beam MCP Server (Powered by Go2TV)

[mcp-beam](https://go2tv.app/mcp-beam/) is a companion MCP server (stdio transport) built on top of Go2TV core packages.

It lets MCP clients discover Chromecast and DLNA/UPnP devices, cast local files or remote
URLs, and stop playback. For direct control, use the Go2TV GUI or CLI.

---

## Troubleshooting

### Firewall Configuration

If you're behind a firewall, allow inbound traffic from devices on your local network:

- `3339-3438/udp` for DLNA/UPnP device discovery
- `3500-4499/tcp` for the local HTTP media server used by DLNA and Chromecast playback

---

## Notes

**Chromecast receiver**

Go2TV uses a custom Chromecast receiver hosted at https://cast-receiver.go2tv.app/. It is not part of this open-source repository and is not currently published. Functionality matches the default receiver, with minor branding differences.

---

## Building from Source

**Requirements**: Go 1.26

``` console
git clone https://github.com/alexballas/go2tv
cd go2tv
make build
```

### AppImage builds (Linux)

``` console
make appimage          # Without FFmpeg
make appimage-ffmpeg   # With FFmpeg
```

For the FFmpeg build, `APPIMAGE_FFMPEG_MODE` supports `auto` (default), `system`,
`download`, or `none`. Override binary paths with `APPIMAGE_FFMPEG_BIN` and
`APPIMAGE_FFPROBE_BIN`.

### Android builds

``` console
make android
```

`make android` builds the arm64 APK and bundles Android `ffmpeg`/`ffprobe` executables as native libraries. Set `ANDROID_NDK_HOME` and `ANDROID_HOME`.

### Using Docker

Build the image:
``` console
docker build --force-rm -t go2tv .
```

Run the container (Linux with X11):
``` console
xhost +local:docker
docker run -it --network host -e DISPLAY=$DISPLAY -v /tmp/.X11-unix:/tmp/.X11-unix go2tv go2tv
```

---

## Author

Alexandros Ballas <alex@ballas.org>

## License

MIT

Artifacts that bundle FFmpeg inherit the bundled FFmpeg build's license obligations. The Android FFmpeg APK uses an Android NDK-built LGPL FFmpeg package by default; AppImages built with the default bundled FFmpeg use GPL builds.
