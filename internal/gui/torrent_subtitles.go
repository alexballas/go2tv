package gui

import (
	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/castsubtitles"
	"go2tv.app/go2tv/v2/internal/mediasource"
)

func registerTorrentSubtitles(server *httphandlers.HTTPserver, host, path string, automatic bool, offset int, transcoded bool) string {
	return castsubtitles.RegisterTorrent(server, host, path, automatic, offset, transcoded)
}

func torrentSubtitleSource(path string, automatic bool) mediasource.Source {
	return castsubtitles.TorrentSource(path, automatic)
}
