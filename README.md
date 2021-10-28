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
</p>
Cast your media files to UPnP/DLNA Media Renderers and Smart TVs.
</div>

---
GUI mode
-----
![](https://i.imgur.com/jCJZbxW.png)

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
        Path to the subtitles file.
  -t string
        Cast to a specific UPnP/DLNA Media Renderer URL.
  -v string
        Path to the video/audio file. (Triggers the CLI mode)
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
