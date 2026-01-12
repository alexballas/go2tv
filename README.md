<br/>
<p align="center">
<img src="assets/go2tv-logo-color.svg" width="225" alt="Go2TV logo">
</a>
</p>
<br/>
<div align="center">
<p>

[![Go Report Card](https://goreportcard.com/badge/github.com/alexballas/Go2TV)](https://goreportcard.com/report/github.com/alexballas/Go2TV)
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

Go2TV lets you play video, audio, and image files on your TV or streaming device directly from your computer or phone. It works with:

- **Smart TVs** - Samsung, LG, Sony, and others that support DLNA/UPnP
- **Chromecast** - Google Chromecast, Chromecast with Google TV, and compatible devices
- **Streaming boxes** - Roku, Fire TV, Android TV boxes with DLNA support
- **Apps** - BubbleUPnP, VLC, and other media receiver apps

No need to copy files to a USB drive or set up a media server. Just select your file, pick your device, and play.

---

## Installation

**Download the latest release** from the [releases page](https://github.com/alexballas/Go2TV/releases/latest).

- **Windows**: Download `go2tv-windows.zip`, extract, and run `go2tv.exe`
- **macOS**: Download `go2tv-macos.zip`, extract, and run the app
- **Linux**: Download `go2tv-linux.zip`, extract, and run `go2tv`
- **Android**: Download the APK

Go2TV is a single executable with no installation required. Just download and run.

### Optional: FFmpeg for Transcoding

For maximum compatibility with all devices and file formats, install [FFmpeg](https://ffmpeg.org/download.html). Go2TV will automatically use it when needed.

- **Linux**: `sudo apt install ffmpeg` or equivalent for your distro
- **macOS**: `brew install ffmpeg`
- **Windows**: Download from [ffmpeg.org](https://ffmpeg.org/download.html) and add to PATH
- **Flatpak**: FFmpeg is bundled automatically

---

## Screenshots

**GUI Mode**

![](https://i.imgur.com/Ga3hLJM.gif)

![](https://i.imgur.com/Pw44BYD.png)
![](https://i.imgur.com/JeUxGGd.png)

**CLI Mode**

![](https://i.imgur.com/BsMevHi.gif)

---

## CLI Usage

``` console
$ go2tv -h
Usage of go2tv:
  -l    List available devices (Smart TVs and Chromecasts).
  -s string
        Path to subtitles file (.srt or .vtt).
  -t string
        Device URL to cast to (from -l output).
  -tc
        Force transcoding with ffmpeg.
  -u string
        URL to media file (triggers CLI mode).
  -v string
        Path to video/audio file (triggers CLI mode).
  -version
        Print version.
```

**Examples**

``` console
# List available devices
go2tv -l

# Play a video on your TV
go2tv -v movie.mp4 -t http://192.168.1.100:8060/

# Play with subtitles
go2tv -v movie.mp4 -s movie.srt -t http://192.168.1.100:8060/

# Force transcoding for incompatible formats
go2tv -v video.avi -tc -t http://192.168.1.100:8060/

# Cast to Chromecast
go2tv -v movie.mp4 -t http://192.168.1.50:8009

# Stream from stdin
cat video.mp4 | go2tv -t http://192.168.1.50:8009

# Stream from command output
yt-dlp -o - "https://youtu.be/..." | go2tv -t http://192.168.1.50:8009
```

---

## Features

- **Auto-discovery** - Automatically finds Smart TVs and Chromecast devices on your network
- **Transcoding** - Converts incompatible video formats on-the-fly (requires FFmpeg)
- **Subtitles** - Supports external SRT/VTT files and embedded MKV subtitles
- **Seek support** - Jump to any position in the video
- **Loop and auto-play** - Loop a single file or auto-play the next file in folder
- **GUI and CLI** - Use the graphical interface or command line

### Supported File Types (GUI)

mp4, avi, mkv, mpeg, mov, webm, m4v, mpv, mp3, flac, wav, jpg, jpeg, png

The CLI accepts any file type.

---

## Notes

**Firewall Configuration**

Go2TV uses ports 3339-3438 for device discovery. If you're behind a firewall, allow inbound UDP traffic on these ports.

**macOS Security**

If you see "cannot be opened because the developer cannot be verified":
1. Control-click the app, then choose Open from the menu
2. Click Open

If you see "go2tv is damaged and can't be opened":
- Run: `xattr -cr /path/to/go2tv.app`

---

## Building from Source

**Requirements**: Go 1.23+

``` console
git clone https://github.com/alexballas/go2tv
cd go2tv
make build
```

**Using Docker**

``` console
docker build --force-rm -t go2tv github.com/alexballas/go2tv#main
```

---

## Author

Alexandros Ballas <alex@ballas.org>

## License

MIT
