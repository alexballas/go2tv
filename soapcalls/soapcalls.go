package soapcalls

import (
	"time"

	"github.com/alexballas/go2tv/utils"
)

type Options struct {
	DMR, Media, Subs, Mtype, ListenAddr string
	Transcode, Seek                     bool
}

var listenAddress string

func NewTVPayload(o Options) (*TVPayload, error) {
	upnpServicesURLs, err := DMRextractor(o.DMR)
	if err != nil {
		return nil, err
	}

	callbackPath, err := utils.RandomString()
	if err != nil {
		return nil, err
	}

	ListenAddr, err := utils.URLtoListenIPandPort(o.DMR)
	if err != nil {
		return nil, err
	}

	return &TVPayload{
		ControlURL:                  upnpServicesURLs.AvtransportControlURL,
		EventURL:                    upnpServicesURLs.AvtransportEventSubURL,
		RenderingControlURL:         upnpServicesURLs.RenderingControlURL,
		ConnectionManagerURL:        upnpServicesURLs.ConnectionManagerURL,
		CallbackURL:                 "http://" + ListenAddr + "/" + callbackPath,
		MediaURL:                    "http://" + ListenAddr + "/" + utils.ConvertFilename(o.Media),
		SubtitlesURL:                "http://" + ListenAddr + "/" + utils.ConvertFilename(o.Subs),
		MediaType:                   o.Mtype,
		CurrentTimers:               make(map[string]*time.Timer),
		MediaRenderersStates:        make(map[string]*States),
		InitialMediaRenderersStates: make(map[string]bool),
		Transcode:                   o.Transcode,
		Seekable:                    o.Seek,
	}, nil
}

func (tv *TVPayload) ListenAddress() string {
	return listenAddress
}
