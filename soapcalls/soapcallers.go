package soapcalls

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
)

// MediaRenderersStates - We hold the states and uuids here
var MediaRenderersStates = make(map[string]map[string]string)

// InitialMediaRenderersStates - Just storing the subscription uuids here
var InitialMediaRenderersStates = make(map[string]interface{})

// TVPayload - we need this populated in order
type TVPayload struct {
	TransportURL string
	VideoURL     string
	SubtitlesURL string
	ControlURL   string
	CallbackURL  string
	Mu           *sync.Mutex
}

func (p *TVPayload) setAVTransportSoapCall() error {
	parsedURLtransport, err := url.Parse(p.TransportURL)
	if err != nil {
		return err
	}

	xml, err := setAVTransportSoapBuild(p.VideoURL, p.SubtitlesURL)
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
func (p *TVPayload) playStopSoapCall(action string) error {
	parsedURLtransport, err := url.Parse(p.TransportURL)
	if err != nil {
		return err
	}

	var xml []byte
	if action == "Play" {
		xml, err = playSoapBuild()
	}

	if action == "Stop" {
		xml, err = stopSoapBuild()
	}

	client := &http.Client{}
	req, err := http.NewRequest("POST", parsedURLtransport.String(), bytes.NewReader(xml))
	if err != nil {
		return err
	}

	headers := http.Header{
		"SOAPAction":   []string{`"urn:schemas-upnp-org:service:AVTransport:1#` + action + `"`},
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

// SubscribeSoapCall - Let's export that too,
// why not
func (p *TVPayload) SubscribeSoapCall() error {

	parsedURLcontrol, err := url.Parse(p.ControlURL)
	if err != nil {
		return err
	}
	parsedURLcallback, err := url.Parse(p.CallbackURL)
	if err != nil {
		return err
	}
	client := &http.Client{}

	req, err := http.NewRequest("SUBSCRIBE", parsedURLcontrol.String(), nil)
	if err != nil {
		return err
	}

	headers := http.Header{
		"USER-AGENT": []string{runtime.GOOS + "  UPnP/1.1 " + "Go2TV"},
		"CALLBACK":   []string{"<" + parsedURLcallback.String() + ">"},
		"NT":         []string{"upnp:event"},
		"TIMEOUT":    []string{"Second-300"},
		"Connection": []string{"close"},
	}
	req.Header = headers
	req.Header.Del("User-Agent")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	uuid := resp.Header["Sid"][0]
	uuid = strings.TrimLeft(uuid, "[")
	uuid = strings.TrimLeft(uuid, "]")
	uuid = strings.TrimLeft(uuid, "uuid:")

	p.Mu.Lock()
	InitialMediaRenderersStates[uuid] = true
	MediaRenderersStates[uuid] = map[string]string{
		"PreviousState": "",
		"NewState":      "",
	}
	p.Mu.Unlock()

	fmt.Println("ALEX SUBSCRIBE")
	fmt.Println(MediaRenderersStates)
	fmt.Println("-------")
	return nil
}

// UnsubscribeSoapCall - exported that as we use it for the callback stuff
// in the httphandlers package
func (p *TVPayload) UnsubscribeSoapCall(uuid string) error {
	parsedURLcontrol, err := url.Parse(p.ControlURL)
	if err != nil {
		return err
	}

	client := &http.Client{}

	req, err := http.NewRequest("UNSUBSCRIBE", parsedURLcontrol.String(), nil)
	if err != nil {
		return err
	}

	headers := http.Header{
		"SID":        []string{"uuid:" + uuid},
		"Connection": []string{"close"},
	}

	req.Header = headers
	req.Header.Del("User-Agent")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	p.Mu.Lock()
	delete(InitialMediaRenderersStates, uuid)
	delete(MediaRenderersStates, uuid)
	p.Mu.Unlock()

	fmt.Println("ALEX UNSUBSCRIBE")
	fmt.Println(MediaRenderersStates)
	fmt.Println(resp.StatusCode)
	fmt.Println("-------")

	return nil
}

// SendtoTV - Send to tv
func (p *TVPayload) SendtoTV(action string) error {
	if action == "Play" {
		if err := p.SubscribeSoapCall(); err != nil {
			return err
		}
		if err := p.setAVTransportSoapCall(); err != nil {
			return err
		}
		fmt.Println("Streaming to Device..")
	}

	if action == "Stop" {
		// Cleaning up all uuids until we start
		// supporting multiple streaming devices

		for uuids := range MediaRenderersStates {
			p.UnsubscribeSoapCall(uuids)
		}

		fmt.Println("Stopping stream..")

	}
	if err := p.playStopSoapCall(action); err != nil {
		return err
	}
	return nil
}