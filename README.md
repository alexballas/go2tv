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
![](https://i.imgur.com/Eq3UkuD.png)
![](https://i.imgur.com/B7wF14V.png)

CLI mode
-----
![](https://i.imgur.com/BsMevHi.gif)

Parameters
-----
```
$ go2tv -h
Usage of go2tv:
  -l    List all available UPnP/DLNA Media Renderer models and URLs.
  -s string
        Local path to the subtitles file.
  -t string
        Cast to a specific UPnP/DLNA Media Renderer URL.
  -u string
        HTTP URL to the media file. URL streaming does not support seek operations. (Triggers the CLI mode)
  -v string
        Local path to the video/audio file. (Triggers the CLI mode)
  -version
        Print version.
```

Allowed media files in the GUI
-----
- mp4, avi, mkv, mpeg, mov, webm, m4v, mpv, mp3, flac, wav

This is a GUI only limitation.

Build requirements
-----
- Go v1.16+

Quick Start
-----
Download the app here https://github.com/alexballas/Go2TV/releases/latest. A single executable. No installation or external dependencies.

Tested on
-----
- Samsung UE50JU6400
- Samsung UE65KS7000
- Android - BubbleUPnP app

Author
------

Alexandros Ballas <alex@ballas.org>
