<br/>
<p align="center">
<img src="https://raw.githubusercontent.com/alexballas/go2tv/main/assets/go2tv-red.svg" width="225" alt="Go2TV logo">
</a>
</p>
<br/>
<div align="center">
<p>

![Go](https://github.com/alexballas/Go2TV/workflows/Go/badge.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/alexballas/Go2TV)](https://goreportcard.com/report/github.com/alexballas/Go2TV)
[![Release Version](https://img.shields.io/github/v/release/alexballas/Go2TV?label=Release)](https://github.com/alexballas/Go2TV/releases/latest)
[![Tests](https://github.com/alexballas/go2tv/actions/workflows/go.yml/badge.svg)](https://github.com/alexballas/go2tv/actions/workflows/go.yml)

[![Build for ARM](https://github.com/alexballas/go2tv/actions/workflows/build-arm.yml/badge.svg)](https://github.com/alexballas/go2tv/actions/workflows/build-arm.yml)
[![Build for Android](https://github.com/alexballas/go2tv/actions/workflows/build-android.yml/badge.svg)](https://github.com/alexballas/go2tv/actions/workflows/build-android.yml)
[![Build for Linux](https://github.com/alexballas/go2tv/actions/workflows/build-linux.yml/badge.svg)](https://github.com/alexballas/go2tv/actions/workflows/build-linux.yml)
[![Build for MacOS](https://github.com/alexballas/go2tv/actions/workflows/build-mac.yml/badge.svg)](https://github.com/alexballas/go2tv/actions/workflows/build-mac.yml)
[![Build for Windows](https://github.com/alexballas/go2tv/actions/workflows/build-windows.yml/badge.svg)](https://github.com/alexballas/go2tv/actions/workflows/build-windows.yml)
</p>
Cast your media files to UPnP/DLNA Media Renderers and Smart TVs.
</div>

---
GUI mode
-----
![](https://i.imgur.com/nrfIc81.png)
![](https://i.imgur.com/ksCaCFl.png)

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
- Go v1.16+
- ffmpeg (optional)

**Build using Docker**

Since the repo provides a [Dockerfile](./Dockerfile), you can build a Go2TV Docker image and run it with just Docker installed (no build requirements and deps above needed). Also, no Git repo cloning is needed (Docker will do it behind the scenes). Just issue:
``` console
$ docker build --force-rm [--pull] -t go2tv github.com/alexballas/go2tv#main
```
Notice the branch name after the `#`, as the above will build `main`. You can also build `devel` if you want to build the latest code. Usage under Docker is outside this document's scope, check Docker docs for more information, specially volume mounts and networking. [x11docker](https://github.com/mviereck/x11docker) might come handy to run GUI mode, although it's not tested, since main Docker usage is CLI.

Quick Start
-----
Download the app here https://github.com/alexballas/Go2TV/releases/latest. A single executable. No installation or external dependencies.

**Transcoding**

Go2TV supports live video transcoding, if ffmpeg is installed. When transcoding, SEEK operations are not available. Transcoding offers the maximum compatibility with the various file formats and devices. Only works with video files.

**MacOS potential issues**

If you get the "cannot be opened because the developer cannot be verified" error, you can apply the following workaround.
- Control-click the app icon, then choose Open from the shortcut menu.
- Click Open.

If you get the "go2tv is damaged and can't be opened. You should move it to the Bin." error you can apply the following workaround.
- Launch Terminal and then issue the following command: `xattr -cr /path/to/go2tv.app`.W

Tested on
-----
- Samsung UE50JU6400
- Samsung UE65KS7000
- Android - BubbleUPnP app

Author
------

Alexandros Ballas <alex@ballas.org>
