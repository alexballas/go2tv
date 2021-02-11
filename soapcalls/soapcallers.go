package soapcalls

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"

	"github.com/huin/goupnp/soap"
)

type playStopRequest struct {
	InstanceID string
	Speed      string
}

type playStopResponse struct {
}

// TVPayload - we need this populated in order
type TVPayload struct {
	TransportURL string
	VideoURL     string
	SubtitlesURL string
}

func setAVTransportSoapCall(videoURL, subtitleURL, transporturl string) error {
	parsedURLtransport, err := url.Parse(transporturl)
	if err != nil {
		return err
	}

	xml, err := setAVTransportSoapBuild(videoURL, subtitleURL)
	client := &http.Client{}
	req, err := http.NewRequest("POST", parsedURLtransport.String(), bytes.NewReader(xml))
	if err != nil {
		return err
	}

	headers := http.Header{
		"SOAPAction":   []string{`"urn:schemas-upnp-org:service:AVTransport:1#SetAVTransportURI"`},
		"content-type": []string{"text/xml"},
		"charset":      []string{"utf-8"},
		"Connection":   []string{"close"},
	}
	req.Header = headers

	_, err = client.Do(req)
	if err != nil {
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
		if err := setAVTransportSoapCall(p.VideoURL, p.SubtitlesURL, p.TransportURL); err != nil {
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
		fmt.Println("Stopping stream..")
	}

	return nil
}
