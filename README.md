<br/>
<p align="center">
<img src="https://raw.githubusercontent.com/alexballas/Go2TV/feature/gui/assets/go2tv-red.svg" width="225" alt="Go2TV logo">
</a>
</p>
<br/>

![Go](https://github.com/alexballas/Go2TV/workflows/Go/badge.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/alexballas/Go2TV)](https://goreportcard.com/report/github.com/alexballas/Go2TV)
[![Release Version](https://img.shields.io/github/v/release/alexballas/Go2TV?label=Release)](https://github.com/alexballas/Go2TV/releases/latest)

Go2TV lets you cast your videos to any UPnP/DLNA MediaRenderer like Samsumg TVs.

![](https://i.imgur.com/BsMevHi.gif)

Usage
-----
```
$ go2tv -h
Usage of go2tv:
  -i    non-GUI interactive mode.
  -l    List all available UPnP/DLNA MediaRenderer models and URLs.
  -s string
        Path to the subtitles file. (only works in interactive mode) 
  -t string
        Cast to a specific UPnP/DLNA MediaRenderer URL. (only works in interactive mode)
  -v string
        Path to the video file. (only works in interactive mode)
  -version
        Print version.
```

Build requirements
-----
- Go v1.16+

Tested on
-----
- Samsung UE50JU6400
- Samsung UE65KS7000
- Android - BubbleUPnP app

Author
------

Alexandros Ballas <alex@ballas.org>
