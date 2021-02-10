package soapcalls

import (
	"fmt"
	"net/url"

	"github.com/huin/goupnp/soap"
)

type playStopRequest struct {
	InstanceID string
	Speed      string
}

type playStopResponse struct {
}

type setAVTransportRequest struct {
	InstanceID         string
	CurrentURI         string
	CurrentURIMetaData string
}

type setAVTransportResponse struct {
}

// TVPayload - we need this populated in order
type TVPayload struct {
	TransportURL string
	VideoURL     string
	SubtitlesURL string
}

func setAVTransportSoapCall(videoURL, transporturl string) error {
	setAVTransportRequest := &setAVTransportRequest{InstanceID: "0", CurrentURI: videoURL}
	setAVTransportResponse := &setAVTransportResponse{}

	parsedURL, err := url.Parse(transporturl)
	if err != nil {
		return err
	}

	newSetAVTransportURIcall := soap.NewSOAPClient(*parsedURL)
	if err := newSetAVTransportURIcall.PerformAction("urn:schemas-upnp-org:service:AVTransport:1",
		"SetAVTransportURI", setAVTransportRequest, setAVTransportResponse); err != nil {
		return err
	}
	return nil
}

// PlayStopSoapCall - Build and call the play soap call
func playStopSoapCall(action, transporturl string) error {

	psRequest := &playStopRequest{InstanceID: "0", Speed: "1"}
	psResponse := &playStopResponse{}

	parsedURL, err := url.Parse(transporturl)
	if err != nil {
		return err
	}

	newPlaycall := soap.NewSOAPClient(*parsedURL)
	if err := newPlaycall.PerformAction("urn:schemas-upnp-org:service:AVTransport:1",
		action, psRequest, psResponse); err != nil {
		return err
	}
	return nil
}

// SendtoTV - Send to tv
func (p *TVPayload) SendtoTV(action string) error {
	if action == "Play" {
		if err := setAVTransportSoapCall(p.VideoURL, p.TransportURL); err != nil {
			return err
		}
	}
	if err := playStopSoapCall(action, p.TransportURL); err != nil {
		return err
	}
	if action == "Play" {
		fmt.Println("Streaming to Device..")
	}
	if action == "Stop" {
		fmt.Println("Stopping streaming..")
	}

	return nil
}
