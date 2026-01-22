package soapcalls

import (
	"context"
	"io"
	"net/url"
	"time"

	"github.com/alexballas/go2tv/v2/utils"
)

type Options struct {
	LogOutput      io.Writer
	ctx            context.Context
	DMR            string
	Media          string
	Subs           string
	Mtype          string
	ListenAddr     string
	FFmpegPath     string
	FFmpegSubsPath string
	Transcode      bool
	Seek           bool
	FFmpegSeek     int
}

// NewTVPayload creates a new TVPayload based on the provided options.
// It initializes the context if not already set, extracts UPnP service URLs,
// generates a random callback path, and determines the listen address.
// It returns a pointer to the created TVPayload or an error if any step fails.
func NewTVPayload(o *Options) (*TVPayload, error) {
	if o.ctx == nil {
		o.ctx = context.Background()
	}

	upnpServicesURLs, err := DMRextractor(o.ctx, o.DMR)
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
		FFmpegPath:                  o.FFmpegPath,
		FFmpegSubsPath:              o.FFmpegSubsPath,
		FFmpegSeek:                  o.FFmpegSeek,
		Seekable:                    o.Seek,
		LogOutput:                   o.LogOutput,
	}, nil
}

// ListenAddress extracts and returns the host component from the MediaURL field of the TVPayload struct.
// It parses the MediaURL as a URL and retrieves the host part of it.
// If the MediaURL is not a valid URL, it returns an empty string.
func (p *TVPayload) ListenAddress() string {
	mediaUrl, _ := url.Parse(p.MediaURL)
	return mediaUrl.Host
}
