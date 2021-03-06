<br/>
<p align="center">
<img src="https://raw.githubusercontent.com/alexballas/Go2TV/feature/gui/assets/go2tv-red.svg" width="225" alt="Go2TV logo">
</a>
</p>
<br/>
<div align="center">
<p>

![Go](https://github.com/alexballas/Go2TV/workflows/Go/badge.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/alexballas/Go2TV)](https://goreportcard.com/report/github.com/alexballas/Go2TV)
[![Release Version](https://img.shields.io/github/v/release/alexballas/Go2TV?label=Release)](https://github.com/alexballas/Go2TV/releases/latest)
</p>
Go2TV lets you cast your videos to any UPnP/DLNA MediaRenderer like Samsumg and LG TVs.
</div>

---
GUI mode
-----
![](https://i.imgur.com/Bw2uthY.png)

CLI mode
-----
![](https://i.imgur.com/BsMevHi.gif)

Parameters
-----
```
$ go2tv -h
Usage of go2tv:
  -l    List all available UPnP/DLNA MediaRenderer models and URLs.
  -s string
        Path to the subtitles file.
  -t string
        Cast to a specific UPnP/DLNA MediaRenderer URL.
  -v string
        Path to the video file. (Triggers the CLI mode)
  -version
        Print version.
```

Build requirements
-----
- Go v1.16+

Quick Start
-----
Download one of the pre-built images here https://github.com/alexballas/Go2TV/releases/latest. A single executable. No installation or external dependencies.

Tested on
-----
- Samsung UE50JU6400
- Samsung UE65KS7000
- LG 28MT49S
- Android - BubbleUPnP app

Author
------

Alexandros Ballas <alex@ballas.org>
