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
Cast your media files to UPnP/DLNA Media Renderers and Smart TVs.
</div>

---
GUI mode
-----
![](https://i.imgur.com/Ga3hLJM.gif)

![](https://i.imgur.com/Pw44BYD.png)
![](https://i.imgur.com/JeUxGGd.png)

CLI mode
-----
![](https://i.imgur.com/BsMevHi.gif)

Parameters
-----
``` console
$ go2tv -h
Usage of go2tv:
  -l    List all available UPnP/DLNA Media Renderer models and URLs.
  -s string
        Local path to the subtitles file.
  -t string
        Cast to a specific UPnP/DLNA Media Renderer URL.
  -tc
        Use ffmpeg to transcode input video file.
  -u string
        HTTP URL to the media file. URL streaming does not support seek operations. (Triggers the CLI mode)
  -v string
        Local path to the video/audio file. (Triggers the CLI mode)
  -version
        Print version.
```

Allowed media files in the GUI
-----
- mp4, avi, mkv, mpeg, mov, webm, m4v, mpv, mp3, flac, wav, jpg, jpeg, png

This is a GUI only limitation.

Build requirements and dependencies
-----
- Go v1.23+
- ffmpeg (optional)

**Build using Docker**

Since the repo provides a [Dockerfile](./Dockerfile), you can build a Go2TV Docker image and run it with just Docker installed (no build requirements and deps above needed). Also, no Git repo cloning is needed (Docker will do it behind the scenes). Just issue:
``` console
$ docker build --force-rm [--pull] -t go2tv github.com/alexballas/go2tv#main
```
Notice the branch name after the `#`, as the above will build `main`. You can also build `devel` if you want to build the latest code. Usage under Docker is outside this document's scope, check Docker docs for more information, specially volume mounts and networking. [x11docker](https://github.com/mviereck/x11docker) might come handy to run GUI mode, although it's not tested, since main Docker usage is CLI.

**Running using Docker**

Example:
``` console
$ xhost +local:docker  # Allows Docker containers to connect to the X server
$ docker run -it --network host -e DISPLAY=$DISPLAY -v /tmp/.X11-unix:/tmp/.X11-unix go2tv go2tv
```

Quick Start
-----
Download the app here https://github.com/alexballas/Go2TV/releases/latest. A single executable. No installation or external dependencies.

**Transcoding (ffmpeg required)**

Go2TV supports live video transcoding, if ffmpeg is installed. When transcoding, SEEK operations are not available. Transcoding offers the maximum compatibility with the various file formats and devices. Only works with video files. *Note:* The Flatpak version of Go2TV bundles ffmpeg.

**MKV/MP4 Subtitle Selection Support (ffmpeg required)**

Go2TV also supports selecting subtitles for video files that have embedded subtitle tracks. This functionality requires ffmpeg to be installed. You can choose the desired subtitle track when casting your media files, enhancing your viewing experience on UPnP/DLNA Media Renderers and Smart TVs.

**SSDP Listen Port Range**

Go2TV now binds the SSDP listener to a port within the fixed range 1900-2000, instead of relying on the system's automatic port assignment. The application will automatically select the first available port from this range. If you are running Go2TV behind a firewall, ensure that inbound UDP traffic is allowed on ports 1900-2000 to enable proper SSDP discovery and device communication.

**MacOS potential issues**

If you get the "cannot be opened because the developer cannot be verified" error, you can apply the following workaround.
- Control-click the app icon, then choose Open from the shortcut menu.
- Click Open.

If you get the "go2tv is damaged and can't be opened. You should move it to the Bin." error you can apply the following workaround.
- Launch Terminal and then issue the following command: `xattr -cr /path/to/go2tv.app`.

Tested on
-----
- Samsung UE50JU6400
- Samsung UE65KS7000
- Android - BubbleUPnP app

Author
------

Alexandros Ballas <alex@ballas.org>
