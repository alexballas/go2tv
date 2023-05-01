package soapcalls

import (
	"io"
	"net/url"
	"time"

	"github.com/alexballas/go2tv/soapcalls/utils"
)

type Options struct {
	Logging    io.Writer
	DMR        string
	Media      string
	Subs       string
	Mtype      string
	ListenAddr string
	Transcode  bool
	Seek       bool
}

func NewTVPayload(o Options) (*TVPayload, error) {
	upnpServicesURLs, err := DMRextractor(o.DMR)
	if err != nil {
		return nil, err
	}

	callbackPath, err := utils.RandomString()
	if err != nil {
		return nil, err
	}

	listenAddress, err := utils.URLtoListenIPandPort(o.DMR)
	if err != nil {
		return nil, err
	}

	return &TVPayload{
		ControlURL:                  upnpServicesURLs.AvtransportControlURL,
		EventURL:                    upnpServicesURLs.AvtransportEventSubURL,
		RenderingControlURL:         upnpServicesURLs.RenderingControlURL,
		ConnectionManagerURL:        upnpServicesURLs.ConnectionManagerURL,
		CallbackURL:                 "http://" + listenAddress + "/" + callbackPath,
		MediaURL:                    "http://" + listenAddress + "/" + utils.ConvertFilename(o.Media),
		SubtitlesURL:                "http://" + listenAddress + "/" + utils.ConvertFilename(o.Subs),
		MediaType:                   o.Mtype,
		MediaPath:                   o.Media,
		CurrentTimers:               make(map[string]*time.Timer),
		MediaRenderersStates:        make(map[string]*States),
		InitialMediaRenderersStates: make(map[string]bool),
		Transcode:                   o.Transcode,
		Seekable:                    o.Seek,
		Logging:                     o.Logging,
	}, nil
}

func (tv *TVPayload) ListenAddress() string {
	url, _ := url.Parse(tv.MediaURL)
	return url.Host
}
