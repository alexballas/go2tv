package soapcalls

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/pkg/errors"
)

type States struct {
	PreviousState string
	NewState      string
	ProcessStop   bool
}

// TVPayload this is the heart of Go2TV. We pass that type to the
// webserver. We need to explicitly initialize it.
type TVPayload struct {
	MediaFile                   interface{}
	CurrentTimers               map[string]*time.Timer
	MediaRenderersStates        map[string]*States
	InitialMediaRenderersStates map[string]bool
	*sync.RWMutex
	ControlURL          string
	EventURL            string
	CallbackURL         string
	RenderingControlURL string
	MediaURL            string
	MediaType           string
	SubtitlesURL        string
	Transcode           bool
	Seekable            bool
}

type getMuteRespBody struct {
	XMLName       xml.Name `xml:"Envelope"`
	Text          string   `xml:",chardata"`
	EncodingStyle string   `xml:"encodingStyle,attr"`
	S             string   `xml:"s,attr"`
	Body          struct {
		Text            string `xml:",chardata"`
		GetMuteResponse struct {
			Text        string `xml:",chardata"`
			U           string `xml:"u,attr"`
			CurrentMute string `xml:"CurrentMute"`
		} `xml:"GetMuteResponse"`
	} `xml:"Body"`
}

// GetVolumeRespBody builds the GetVolume response body
type getVolumeRespBody struct {
	XMLName       xml.Name `xml:"Envelope"`
	Text          string   `xml:",chardata"`
	EncodingStyle string   `xml:"encodingStyle,attr"`
	S             string   `xml:"s,attr"`
	Body          struct {
		Text              string `xml:",chardata"`
		GetVolumeResponse struct {
			Text          string `xml:",chardata"`
			U             string `xml:"u,attr"`
			CurrentVolume string `xml:"CurrentVolume"`
		} `xml:"GetVolumeResponse"`
	} `xml:"Body"`
}

func (p *TVPayload) setAVTransportSoapCall() error {
	parsedURLtransport, err := url.Parse(p.ControlURL)
	if err != nil {
		return fmt.Errorf("setAVTransportSoapCall parse error: %w", err)
	}

	xml, err := setAVTransportSoapBuild(p.MediaURL, p.MediaType, p.SubtitlesURL, p.Transcode, p.Seekable)
	if err != nil {
		return fmt.Errorf("setAVTransportSoapCall soap build error: %w", err)
	}

	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = 3
	retryClient.Logger = nil
	client := retryClient.StandardClient()

	req, err := http.NewRequest("POST", parsedURLtransport.String(), bytes.NewReader(xml))
	if err != nil {
		return fmt.Errorf("setAVTransportSoapCall POST error: %w", err)
	}

	req.Header = http.Header{
		"SOAPAction":   []string{`"urn:schemas-upnp-org:service:AVTransport:1#SetAVTransportURI"`},
		"content-type": []string{"text/xml"},
		"charset":      []string{"utf-8"},
		"Connection":   []string{"close"},
	}

	_, err = client.Do(req)
	if err != nil {
		return fmt.Errorf("setAVTransportSoapCall Do POST error: %w", err)
	}

	return nil
}

// AVTransportActionSoapCall builds and sends the AVTransport actions
func (p *TVPayload) AVTransportActionSoapCall(action string) error {
	parsedURLtransport, err := url.Parse(p.ControlURL)
	if err != nil {
		return fmt.Errorf("AVTransportActionSoapCall parse error: %w", err)
	}

	var xml []byte
	retry := false

	switch action {
	case "Play":
		xml, err = playSoapBuild()
	case "Stop":
		xml, err = stopSoapBuild()
		retry = true
	case "Pause":
		xml, err = pauseSoapBuild()
	}
	if err != nil {
		return fmt.Errorf("AVTransportActionSoapCall action error: %w", err)
	}

	client := &http.Client{}

	if retry {
		retryClient := retryablehttp.NewClient()
		retryClient.RetryMax = 3
		retryClient.Logger = nil
		client = retryClient.StandardClient()
	}

	req, err := http.NewRequest("POST", parsedURLtransport.String(), bytes.NewReader(xml))
	if err != nil {
		return fmt.Errorf("AVTransportActionSoapCall POST error: %w", err)
	}

	req.Header = http.Header{
		"SOAPAction":   []string{`"urn:schemas-upnp-org:service:AVTransport:1#` + action + `"`},
		"content-type": []string{"text/xml"},
		"charset":      []string{"utf-8"},
		"Connection":   []string{"close"},
	}

	_, err = client.Do(req)
	if err != nil {
		return fmt.Errorf("AVTransportActionSoapCall Do POST error: %w", err)
	}

	return nil
}

// SubscribeSoapCall send a SUBSCRIBE request to the DMR device.
// If we explicitly pass the UUID, then we refresh it instead.
func (p *TVPayload) SubscribeSoapCall(uuidInput string) error {
	delete(p.CurrentTimers, uuidInput)

	parsedURLcontrol, err := url.Parse(p.EventURL)
	if err != nil {
		return fmt.Errorf("SubscribeSoapCall #1 parse error: %w", err)
	}

	parsedURLcallback, err := url.Parse(p.CallbackURL)
	if err != nil {
		return fmt.Errorf("SubscribeSoapCall #2 parse error: %w", err)
	}

	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = 3
	retryClient.Logger = nil

	client := retryClient.StandardClient()

	req, err := http.NewRequest("SUBSCRIBE", parsedURLcontrol.String(), nil)
	if err != nil {
		return fmt.Errorf("SubscribeSoapCall SUBSCRIBE error: %w", err)
	}

	var headers http.Header
	if uuidInput == "" {
		headers = http.Header{
			"USER-AGENT": []string{runtime.GOOS + "  UPnP/1.1 " + "Go2TV"},
			"CALLBACK":   []string{"<" + parsedURLcallback.String() + ">"},
			"NT":         []string{"upnp:event"},
			"TIMEOUT":    []string{"Second-300"},
			"Connection": []string{"close"},
		}
	} else {
		headers = http.Header{
			"SID":        []string{"uuid:" + uuidInput},
			"TIMEOUT":    []string{"Second-300"},
			"Connection": []string{"close"},
		}
	}
	req.Header = headers
	req.Header.Del("User-Agent")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("SubscribeSoapCall Do SUBSCRIBE error: %w", err)
	}

	defer resp.Body.Close()

	var uuid string

	if resp.Status != "200 OK" {
		if uuidInput != "" {
			// We're calling the unsubscribe method to make sure
			// we clean up any remaining states for the specific
			// uuid. The actual UNSUBSCRIBE request to the media
			// renderer may still fail with error 412, but it's fine.
			_ = p.UnsubscribeSoapCall(uuid)
		}
		return nil
	}

	if len(resp.Header["Sid"]) == 0 {
		// This should be an impossible case
		return nil
	}

	uuid = resp.Header["Sid"][0]
	uuid = strings.TrimLeft(uuid, "[")
	uuid = strings.TrimLeft(uuid, "]")
	uuid = strings.TrimPrefix(uuid, "uuid:")

	// We don't really need to initialize or set
	// the State if we're just refreshing the uuid.
	if uuidInput == "" {
		p.CreateMRstate(uuid)
	}

	timeoutReply := "300"
	if len(resp.Header["Timeout"]) > 0 {
		timeoutReply = strings.TrimLeft(resp.Header["Timeout"][0], "Second-")
	}

	p.RefreshLoopUUIDSoapCall(uuid, timeoutReply)

	return nil
}

// UnsubscribeSoapCall sends an UNSUBSCRIBE request to the DMR device
// and cleans up any stored states for the provided UUID.
func (p *TVPayload) UnsubscribeSoapCall(uuid string) error {
	p.DeleteMRstate(uuid)

	parsedURLcontrol, err := url.Parse(p.EventURL)
	if err != nil {
		return fmt.Errorf("UnsubscribeSoapCall parse error: %w", err)
	}

	client := &http.Client{}

	req, err := http.NewRequest("UNSUBSCRIBE", parsedURLcontrol.String(), nil)
	if err != nil {
		return fmt.Errorf("UnsubscribeSoapCall UNSUBSCRIBE error: %w", err)
	}

	req.Header = http.Header{
		"SID":        []string{"uuid:" + uuid},
		"Connection": []string{"close"},
	}

	req.Header.Del("User-Agent")

	_, err = client.Do(req)
	if err != nil {
		return fmt.Errorf("UnsubscribeSoapCall Do UNSUBSCRIBE error: %w", err)
	}

	return nil
}

// RefreshLoopUUIDSoapCall refreshes the UUID.
func (p *TVPayload) RefreshLoopUUIDSoapCall(uuid, timeout string) {
	triggerTime := 5
	timeoutInt, err := strconv.Atoi(timeout)
	if err != nil {
		return
	}

	// Refresh token after after Timeout / 2 seconds.
	if timeoutInt > 20 {
		triggerTime = timeoutInt / 5
	}
	triggerTimefunc := time.Duration(triggerTime) * time.Second

	// We're doing this as time.AfterFunc can't handle
	// function arguments.
	f := p.refreshLoopUUIDAsyncSoapCall(uuid)
	timer := time.AfterFunc(triggerTimefunc, f)
	p.CurrentTimers[uuid] = timer
}

func (p *TVPayload) refreshLoopUUIDAsyncSoapCall(uuid string) func() {
	return func() {
		_ = p.SubscribeSoapCall(uuid)
	}
}

// GetMuteSoapCall returns the mute status for our device
func (p *TVPayload) GetMuteSoapCall() (string, error) {
	parsedRenderingControlURL, err := url.Parse(p.RenderingControlURL)
	if err != nil {
		return "", fmt.Errorf("GetMuteSoapCall parse error: %w", err)
	}

	var xmlbuilder []byte

	xmlbuilder, err = getMuteSoapBuild()
	if err != nil {
		return "", fmt.Errorf("GetMuteSoapCall build error: %w", err)
	}

	client := &http.Client{}
	req, err := http.NewRequest("POST", parsedRenderingControlURL.String(), bytes.NewReader(xmlbuilder))
	if err != nil {
		return "", fmt.Errorf("GetMuteSoapCall POST error: %w", err)
	}

	req.Header = http.Header{
		"SOAPAction":   []string{`"urn:schemas-upnp-org:service:RenderingControl:1#GetMute"`},
		"content-type": []string{"text/xml"},
		"charset":      []string{"utf-8"},
		"Connection":   []string{"close"},
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("GetMuteSoapCall Do POST error: %w", err)
	}

	defer resp.Body.Close()

	var respGetMute getMuteRespBody
	if err = xml.NewDecoder(resp.Body).Decode(&respGetMute); err != nil {
		return "", fmt.Errorf("GetMuteSoapCall XML Decode error: %w", err)
	}

	return respGetMute.Body.GetMuteResponse.CurrentMute, nil
}

// SetMuteSoapCall returns true if muted and false if not muted.
func (p *TVPayload) SetMuteSoapCall(number string) error {
	parsedRenderingControlURL, err := url.Parse(p.RenderingControlURL)
	if err != nil {
		return fmt.Errorf("SetMuteSoapCall parse error: %w", err)
	}

	var xmlbuilder []byte

	xmlbuilder, err = setMuteSoapBuild(number)
	if err != nil {
		return fmt.Errorf("SetMuteSoapCall build error: %w", err)
	}

	client := &http.Client{}
	req, err := http.NewRequest("POST", parsedRenderingControlURL.String(), bytes.NewReader(xmlbuilder))
	if err != nil {
		return fmt.Errorf("SetMuteSoapCall POST error: %w", err)
	}

	req.Header = http.Header{
		"SOAPAction":   []string{`"urn:schemas-upnp-org:service:RenderingControl:1#SetMute"`},
		"content-type": []string{"text/xml"},
		"charset":      []string{"utf-8"},
		"Connection":   []string{"close"},
	}

	_, err = client.Do(req)
	if err != nil {
		return fmt.Errorf("SetMuteSoapCall Do POST error: %w", err)
	}

	return nil
}

// GetVolumeSoapCall returns tue volume level for our device.
func (p *TVPayload) GetVolumeSoapCall() (int, error) {
	parsedRenderingControlURL, err := url.Parse(p.RenderingControlURL)
	if err != nil {
		return 0, fmt.Errorf("GetVolumeSoapCall parse error: %w", err)
	}

	var xmlbuilder []byte

	xmlbuilder, err = getVolumeSoapBuild()
	if err != nil {
		return 0, fmt.Errorf("GetVolumeSoapCall build error: %w", err)
	}

	client := &http.Client{}
	req, err := http.NewRequest("POST", parsedRenderingControlURL.String(), bytes.NewReader(xmlbuilder))
	if err != nil {
		return 0, fmt.Errorf("GetVolumeSoapCall POST error: %w", err)
	}

	req.Header = http.Header{
		"SOAPAction":   []string{`"urn:schemas-upnp-org:service:RenderingControl:1#GetVolume"`},
		"content-type": []string{"text/xml"},
		"charset":      []string{"utf-8"},
		"Connection":   []string{"close"},
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("GetVolumeSoapCall Do POST error: %w", err)
	}

	defer resp.Body.Close()

	var respGetVolume getVolumeRespBody
	if err = xml.NewDecoder(resp.Body).Decode(&respGetVolume); err != nil {
		return 0, fmt.Errorf("GetVolumeSoapCall XML Decode error: %w", err)
	}

	intVolume, err := strconv.Atoi(respGetVolume.Body.GetVolumeResponse.CurrentVolume)
	if err != nil {
		return 0, fmt.Errorf("GetVolumeSoapCall failed to parse volume value: %w", err)
	}

	if intVolume < 0 {
		intVolume = 0
	}

	return intVolume, nil
}

// SetVolumeSoapCall sets the desired volume level.
func (p *TVPayload) SetVolumeSoapCall(v string) error {
	parsedRenderingControlURL, err := url.Parse(p.RenderingControlURL)
	if err != nil {
		return fmt.Errorf("SetMuteSoapCall parse error: %w", err)
	}

	var xmlbuilder []byte

	xmlbuilder, err = setVolumeSoapBuild(v)
	if err != nil {
		return fmt.Errorf("SetMuteSoapCall build error: %w", err)
	}

	client := &http.Client{}
	req, err := http.NewRequest("POST", parsedRenderingControlURL.String(), bytes.NewReader(xmlbuilder))
	if err != nil {
		return fmt.Errorf("SetMuteSoapCall POST error: %w", err)
	}

	req.Header = http.Header{
		"SOAPAction":   []string{`"urn:schemas-upnp-org:service:RenderingControl:1#SetVolume"`},
		"content-type": []string{"text/xml"},
		"charset":      []string{"utf-8"},
		"Connection":   []string{"close"},
	}

	_, err = client.Do(req)
	if err != nil {
		return fmt.Errorf("SetMuteSoapCall Do POST error: %w", err)
	}

	return nil
}

// SendtoTV is a higher level method that gracefully handles the various
// states when communicating with the DMR devices.
func (p *TVPayload) SendtoTV(action string) error {
	if action == "Play1" {
		if err := p.SubscribeSoapCall(""); err != nil {
			return fmt.Errorf("SendtoTV subscribe call error: %w", err)
		}
		if err := p.setAVTransportSoapCall(); err != nil {
			return fmt.Errorf("SendtoTV set AVT Transport error: %w", err)
		}
		action = "Play"
	}

	if action == "Stop" {
		p.RLock()
		localStates := make(map[string]*States)
		for key, value := range p.MediaRenderersStates {
			localStates[key] = value
		}
		p.RUnlock()

		// Cleaning up all uuids on force stop.
		for uuids := range localStates {
			if err := p.UnsubscribeSoapCall(uuids); err != nil {
				return fmt.Errorf("SendtoTV unsubscribe call error: %w", err)
			}
		}

		// Clear timers on Stop to avoid errors responses
		// from the media renderers. If we don't clear those, we
		// might receive a "412 Precondition Failed" error.
		for uuid, timer := range p.CurrentTimers {
			timer.Stop()
			delete(p.CurrentTimers, uuid)
		}
	}
	err := p.AVTransportActionSoapCall(action)
	if err != nil {
		return fmt.Errorf("SendtoTV Play/Stop/Pause action error: %w", err)
	}

	return nil
}

// UpdateMRstate updates the mediaRenderersStates map with the state.
// Returns true or false to verify that the actual update took place.
func (p *TVPayload) UpdateMRstate(previous, new, uuid string) bool {
	if previous == "" || new == "" {
		return false
	}

	p.Lock()
	defer p.Unlock()
	// If the UUID is not available in p.InitialMediaRenderersStates,
	// it probably expired and there is not much we can do with it.
	// Trying to send an UNSUBSCRIBE call for that UUID will result
	// in a 412 error as per the UPNP documentation
	// https://openconnectivity.org/upnp-specs/UPnP-arch-DeviceArchitecture-v1.1.pdf
	// (page 94).
	if p.InitialMediaRenderersStates[uuid] {
		p.MediaRenderersStates[uuid].PreviousState = previous
		p.MediaRenderersStates[uuid].NewState = new
		return true
	}

	return false
}

// CreateMRstate .
func (p *TVPayload) CreateMRstate(uuid string) {
	p.Lock()
	defer p.Unlock()
	p.InitialMediaRenderersStates[uuid] = true
	p.MediaRenderersStates[uuid] = &States{}
}

// DeleteMRstate deletes the state entries for the specific UUID.
func (p *TVPayload) DeleteMRstate(uuid string) {
	p.Lock()
	defer p.Unlock()
	delete(p.InitialMediaRenderersStates, uuid)
	delete(p.MediaRenderersStates, uuid)
}

// SetProcessStopTrue set the stop process to true
func (p *TVPayload) SetProcessStopTrue(uuid string) {
	p.Lock()
	defer p.Unlock()
	p.MediaRenderersStates[uuid].ProcessStop = true
}

// GetProcessStop returns the processStop value of the specific UUID.
func (p *TVPayload) GetProcessStop(uuid string) (bool, error) {
	p.RLock()
	defer p.RUnlock()
	if p.InitialMediaRenderersStates[uuid] {
		return p.MediaRenderersStates[uuid].ProcessStop, nil
	}

	return true, errors.New("zombie callbacks, we should ignore those")
}
