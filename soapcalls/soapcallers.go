package soapcalls

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
)

type States struct {
	PreviousState string
	NewState      string
	ProcessStop   bool
}

var (
	log                   zerolog.Logger
	ErrNoMatchingFileType = errors.New("no matching file type")
	ErrZombieCallbacks    = errors.New("zombie callbacks, we should ignore those")
)

// TVPayload is the heart of Go2TV. We pass that type to the
// webserver. We need to explicitly initialize it.
type TVPayload struct {
	mu                          sync.RWMutex
	initLogOnce                 sync.Once
	Logging                     io.Writer
	CurrentTimers               map[string]*time.Timer
	InitialMediaRenderersStates map[string]bool
	MediaRenderersStates        map[string]*States
	ControlURL                  string
	EventURL                    string
	MediaURL                    string
	MediaType                   string
	MediaPath                   string
	SubtitlesURL                string
	CallbackURL                 string
	ConnectionManagerURL        string
	RenderingControlURL         string
	Transcode                   bool
	Seekable                    bool
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

type protocolInfoResponse struct {
	XMLName       xml.Name `xml:"Envelope"`
	Text          string   `xml:",chardata"`
	S             string   `xml:"s,attr"`
	EncodingStyle string   `xml:"encodingStyle,attr"`
	Body          struct {
		Text                    string `xml:",chardata"`
		GetProtocolInfoResponse struct {
			Text   string `xml:",chardata"`
			U      string `xml:"u,attr"`
			Source string `xml:"Source"`
			Sink   string `xml:"Sink"`
		} `xml:"GetProtocolInfoResponse"`
	} `xml:"Body"`
}

type getMediaInfoResponse struct {
	XMLName       xml.Name `xml:"Envelope"`
	Text          string   `xml:",chardata"`
	EncodingStyle string   `xml:"encodingStyle,attr"`
	S             string   `xml:"s,attr"`
	Body          struct {
		Text                 string `xml:",chardata"`
		GetMediaInfoResponse struct {
			Text               string `xml:",chardata"`
			U                  string `xml:"u,attr"`
			NrTracks           string `xml:"NrTracks"`
			MediaDuration      string `xml:"MediaDuration"`
			CurrentURI         string `xml:"CurrentURI"`
			CurrentURIMetaData string `xml:"CurrentURIMetaData"`
			NextURI            string `xml:"NextURI"`
			NextURIMetaData    string `xml:"NextURIMetaData"`
			PlayMedium         string `xml:"PlayMedium"`
			RecordMedium       string `xml:"RecordMedium"`
			WriteStatus        string `xml:"WriteStatus"`
		} `xml:"GetMediaInfoResponse"`
	} `xml:"Body"`
}

type getTransportInfoResponse struct {
	XMLName       xml.Name `xml:"Envelope"`
	Text          string   `xml:",chardata"`
	S             string   `xml:"s,attr"`
	EncodingStyle string   `xml:"encodingStyle,attr"`
	Body          struct {
		Text                     string `xml:",chardata"`
		GetTransportInfoResponse struct {
			Text                   string `xml:",chardata"`
			U                      string `xml:"u,attr"`
			CurrentTransportState  string `xml:"CurrentTransportState"`
			CurrentTransportStatus string `xml:"CurrentTransportStatus"`
			CurrentSpeed           string `xml:"CurrentSpeed"`
		} `xml:"GetTransportInfoResponse"`
	} `xml:"Body"`
}

type getPositionInfoResponse struct {
	XMLName       xml.Name `xml:"Envelope"`
	Text          string   `xml:",chardata"`
	S             string   `xml:"s,attr"`
	EncodingStyle string   `xml:"encodingStyle,attr"`
	Body          struct {
		Text                    string `xml:",chardata"`
		GetPositionInfoResponse struct {
			Text          string `xml:",chardata"`
			U             string `xml:"u,attr"`
			Track         string `xml:"Track"`
			TrackDuration string `xml:"TrackDuration"`
			TrackMetaData string `xml:"TrackMetaData"`
			TrackURI      string `xml:"TrackURI"`
			RelTime       string `xml:"RelTime"`
			AbsTime       string `xml:"AbsTime"`
			RelCount      string `xml:"RelCount"`
			AbsCount      string `xml:"AbsCount"`
		} `xml:"GetPositionInfoResponse"`
	} `xml:"Body"`
}

func (p *TVPayload) setAVTransportSoapCall() error {
	parsedURLtransport, err := url.Parse(p.ControlURL)
	if err != nil {
		p.Log().Error().Str("Method", "setAVTransportSoapCall").Str("Action", "URL Parse").Err(err).Msg("")
		return fmt.Errorf("setAVTransportSoapCall parse error: %w", err)
	}

	xml, err := setAVTransportSoapBuild(p)
	if err != nil {
		p.Log().Error().Str("Method", "setAVTransportSoapCall").Str("Action", "setAVTransportSoapBuild").Err(err).Msg("")
		return fmt.Errorf("setAVTransportSoapCall soap build error: %w", err)
	}

	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = 3
	retryClient.Logger = nil
	client := retryClient.StandardClient()

	req, err := http.NewRequest("POST", parsedURLtransport.String(), bytes.NewReader(xml))
	if err != nil {
		p.Log().Error().Str("Method", "setAVTransportSoapCall").Str("Action", "Prepare POST").Err(err).Msg("")
		return fmt.Errorf("setAVTransportSoapCall POST error: %w", err)
	}

	req.Header = http.Header{
		"SOAPAction":   []string{`"urn:schemas-upnp-org:service:AVTransport:1#SetAVTransportURI"`},
		"content-type": []string{"text/xml"},
		"charset":      []string{"utf-8"},
		"Connection":   []string{"close"},
	}

	headerBytesReq, err := json.Marshal(req.Header)
	if err != nil {
		p.Log().Error().Str("Method", "setAVTransportSoapCall").Str("Action", "Header Marshaling").Err(err).Msg("")
		return fmt.Errorf("setAVTransportSoapCall Request Marshaling error: %w", err)
	}

	p.Log().Debug().
		Str("Method", "setAVTransportSoapCall").Str("Action", "Request").
		RawJSON("Headers", headerBytesReq).
		Msg(string(xml))

	res, err := client.Do(req)
	if err != nil {
		p.Log().Error().Str("Method", "setAVTransportSoapCall").Str("Action", "Do POST").Err(err).Msg("")
		return fmt.Errorf("setAVTransportSoapCall Do POST error: %w", err)
	}
	defer res.Body.Close()

	resBytes, err := io.ReadAll(res.Body)
	if err != nil {
		p.Log().Error().Str("Method", "setAVTransportSoapCall").Str("Action", "Readall").Err(err).Msg("")
		return fmt.Errorf("setAVTransportSoapCall Failed to read response: %w", err)
	}

	headerBytesRes, err := json.Marshal(res.Header)
	if err != nil {
		p.Log().Error().Str("Method", "setAVTransportSoapCall").Str("Action", "Header Marshaling #2").Err(err).Msg("")
		return fmt.Errorf("setAVTransportSoapCall Response Marshaling error: %w", err)
	}

	p.Log().Debug().
		Str("Method", "setAVTransportSoapCall").Str("Action", "Response").Str("Status Code", strconv.Itoa(res.StatusCode)).
		RawJSON("Headers", headerBytesRes).
		Msg(string(resBytes))

	return nil
}

func (p *TVPayload) setNextAVTransportSoapCall(clear bool) error {
	parsedURLtransport, err := url.Parse(p.ControlURL)
	if err != nil {
		p.Log().Error().Str("Method", "setNextAVTransportSoapCall").Str("Action", "URL Parse").Err(err).Msg("")
		return fmt.Errorf("setNextAVTransportSoapCall parse error: %w", err)
	}

	xml, err := setNextAVTransportSoapBuild(p, clear)
	if err != nil {
		p.Log().Error().Str("Method", "setNextAVTransportSoapCall").Str("Action", "setNextAVTransportSoapBuild").Err(err).Msg("")
		return fmt.Errorf("setNextAVTransportSoapCall soap build error: %w", err)
	}

	client := &http.Client{}

	req, err := http.NewRequest("POST", parsedURLtransport.String(), bytes.NewReader(xml))
	if err != nil {
		p.Log().Error().Str("Method", "setNextAVTransportSoapCall").Str("Action", "Prepare POST").Err(err).Msg("")
		return fmt.Errorf("setNextAVTransportSoapCall POST error: %w", err)
	}

	req.Header = http.Header{
		"SOAPAction":   []string{`"urn:schemas-upnp-org:service:AVTransport:1#SetNextAVTransportURI"`},
		"content-type": []string{"text/xml"},
		"charset":      []string{"utf-8"},
		"Connection":   []string{"close"},
	}

	headerBytesReq, err := json.Marshal(req.Header)
	if err != nil {
		p.Log().Error().Str("Method", "setNextAVTransportSoapCall").Str("Action", "Header Marshaling").Err(err).Msg("")
		return fmt.Errorf("setNextAVTransportSoapCall Request Marshaling error: %w", err)
	}

	p.Log().Debug().
		Str("Method", "setNextAVTransportSoapCall").Str("Action", "Request").
		RawJSON("Headers", headerBytesReq).
		Msg(string(xml))

	res, err := client.Do(req)
	if err != nil {
		p.Log().Error().Str("Method", "setNextAVTransportSoapCall").Str("Action", "Do POST").Err(err).Msg("")
		return fmt.Errorf("setNextAVTransportSoapCall Do POST error: %w", err)
	}
	defer res.Body.Close()

	resBytes, err := io.ReadAll(res.Body)
	if err != nil {
		p.Log().Error().Str("Method", "setNextAVTransportSoapCall").Str("Action", "Readall").Err(err).Msg("")
		return fmt.Errorf("setNextAVTransportSoapCall Failed to read response: %w", err)
	}

	headerBytesRes, err := json.Marshal(res.Header)
	if err != nil {
		p.Log().Error().Str("Method", "setNextAVTransportSoapCall").Str("Action", "Header Marshaling #2").Err(err).Msg("")
		return fmt.Errorf("setNextAVTransportSoapCall Response Marshaling error: %w", err)
	}

	p.Log().Debug().
		Str("Method", "setNextAVTransportSoapCall").Str("Action", "Response").Str("Status Code", strconv.Itoa(res.StatusCode)).
		RawJSON("Headers", headerBytesRes).
		Msg(string(resBytes))

	return nil
}

// AVTransportActionSoapCall builds and sends the AVTransport actions
func (p *TVPayload) AVTransportActionSoapCall(action string) error {
	parsedURLtransport, err := url.Parse(p.ControlURL)
	if err != nil {
		p.Log().Error().Str("Method", "AVTransportActionSoapCall").Str("Action", "URL Parse").Err(err).Msg("")
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
		p.Log().Error().Str("Method", "AVTransportActionSoapCall").Str("Action", "Action Error").Err(err).Msg("")
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
		p.Log().Error().Str("Method", "AVTransportActionSoapCall").Str("Action", "Prepare POST").Err(err).Msg("")
		return fmt.Errorf("AVTransportActionSoapCall POST error: %w", err)
	}

	req.Header = http.Header{
		"SOAPAction":   []string{`"urn:schemas-upnp-org:service:AVTransport:1#` + action + `"`},
		"content-type": []string{"text/xml"},
		"charset":      []string{"utf-8"},
		"Connection":   []string{"close"},
	}

	headerBytesReq, err := json.Marshal(req.Header)
	if err != nil {
		p.Log().Error().Str("Method", "AVTransportActionSoapCall").Str("Action", "Header Marshaling").Err(err).Msg("")
		return fmt.Errorf("AVTransportActionSoapCall Request Marshaling error: %w", err)
	}

	p.Log().Debug().
		Str("Method", "AVTransportActionSoapCall").Str("Action", action+" Request").
		RawJSON("Headers", headerBytesReq).
		Msg(string(xml))

	res, err := client.Do(req)
	if err != nil {
		p.Log().Error().Str("Method", "AVTransportActionSoapCall").Str("Action", "Do POST").Err(err).Msg("")
		return fmt.Errorf("AVTransportActionSoapCall Do POST error: %w", err)
	}
	defer res.Body.Close()

	resBytes, err := io.ReadAll(res.Body)
	if err != nil {
		p.Log().Error().Str("Method", "AVTransportActionSoapCall").Str("Action", "Readall").Err(err).Msg("")
		return fmt.Errorf("AVTransportActionSoapCall Failed to read response: %w", err)
	}

	headerBytesRes, err := json.Marshal(res.Header)
	if err != nil {
		p.Log().Error().Str("Method", "AVTransportActionSoapCall").Str("Action", "Header Marshaling #2").Err(err).Msg("")
		return fmt.Errorf("AVTransportActionSoapCall Response Marshaling error: %w", err)
	}

	p.Log().Debug().
		Str("Method", "AVTransportActionSoapCall").Str("Action", action+" Response").Str("Status Code", strconv.Itoa(res.StatusCode)).
		RawJSON("Headers", headerBytesRes).
		Msg(string(resBytes))

	return nil
}

// SubscribeSoapCall send a SUBSCRIBE request to the DMR device.
// If we explicitly pass the UUID, then we refresh it instead.
func (p *TVPayload) SubscribeSoapCall(uuidInput string) error {
	delete(p.CurrentTimers, uuidInput)

	parsedURLcontrol, err := url.Parse(p.EventURL)
	if err != nil {
		p.Log().Error().Str("Method", "SubscribeSoapCall").Str("Action", "URL Parse #1").Err(err).Msg("")
		return fmt.Errorf("SubscribeSoapCall #1 parse error: %w", err)
	}

	parsedURLcallback, err := url.Parse(p.CallbackURL)
	if err != nil {
		p.Log().Error().Str("Method", "SubscribeSoapCall").Str("Action", "URL Parse #2").Err(err).Msg("")
		return fmt.Errorf("SubscribeSoapCall #2 parse error: %w", err)
	}

	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = 3
	retryClient.Logger = nil

	client := retryClient.StandardClient()

	req, err := http.NewRequest("SUBSCRIBE", parsedURLcontrol.String(), nil)
	if err != nil {
		p.Log().Error().Str("Method", "SubscribeSoapCall").Str("Action", "Prepare SUBSCRIBE").Err(err).Msg("")
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

	headerBytesReq, err := json.Marshal(req.Header)
	if err != nil {
		p.Log().Error().Str("Method", "SubscribeSoapCall").Str("Action", "Header Marshaling").Err(err).Msg("")
		return fmt.Errorf("SubscribeSoapCall Request Marshaling error: %w", err)
	}

	p.Log().Debug().
		Str("Method", "SubscribeSoapCall").Str("Action", "Subscribe Request").
		RawJSON("Headers", headerBytesReq).
		Msg("")

	res, err := client.Do(req)
	if err != nil {
		p.Log().Error().Str("Method", "SubscribeSoapCall").Str("Action", "Do SUBSCRIBE").Err(err).Msg("")
		return fmt.Errorf("SubscribeSoapCall Do SUBSCRIBE error: %w", err)
	}
	defer res.Body.Close()

	resBytes, err := io.ReadAll(res.Body)
	if err != nil {
		p.Log().Error().Str("Method", "SubscribeSoapCall").Str("Action", "Readall").Err(err).Msg("")
		return fmt.Errorf("SubscribeSoapCall Failed to read response: %w", err)
	}

	headerBytesRes, err := json.Marshal(res.Header)
	if err != nil {
		p.Log().Error().Str("Method", "SubscribeSoapCall").Str("Action", "Header Marshaling #2").Err(err).Msg("")
		return fmt.Errorf("SubscribeSoapCall Response Marshaling error: %w", err)
	}

	p.Log().Debug().
		Str("Method", "SubscribeSoapCall").Str("Action", "Subscribe Response").Str("Status Code", strconv.Itoa(res.StatusCode)).
		RawJSON("Headers", headerBytesRes).
		Msg(string(resBytes))

	var uuid string

	if res.Status != "200 OK" {
		if uuidInput != "" {
			// We're calling the unsubscribe method to make sure
			// we clean up any remaining states for the specific
			// uuid. The actual UNSUBSCRIBE request to the media
			// renderer may still fail with error 412, but it's fine.
			_ = p.UnsubscribeSoapCall(uuid)
		}
		return nil
	}

	if len(res.Header["Sid"]) == 0 {
		// This should be an impossible case
		return nil
	}

	uuid = res.Header["Sid"][0]
	uuid = strings.TrimLeft(uuid, "[")
	uuid = strings.TrimLeft(uuid, "]")
	uuid = strings.TrimPrefix(uuid, "uuid:")

	// We don't really need to initialize or set
	// the State if we're just refreshing the uuid.
	if uuidInput == "" {
		p.CreateMRstate(uuid)
	}

	timeoutReply := "300"
	if len(res.Header["Timeout"]) > 0 {
		timeoutReply = strings.TrimLeft(res.Header["Timeout"][0], "Second-")
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

	// Refresh token after Timeout / 2 seconds.
	if timeoutInt > 20 {
		triggerTime = timeoutInt / 5
	}
	triggerTimefunc := time.Duration(triggerTime) * time.Second

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
		p.Log().Error().Str("Method", "GetMuteSoapCall").Str("Action", "URL Parse").Err(err).Msg("")
		return "", fmt.Errorf("GetMuteSoapCall parse error: %w", err)
	}

	var xmlbuilder []byte

	xmlbuilder, err = getMuteSoapBuild()
	if err != nil {
		p.Log().Error().Str("Method", "GetMuteSoapCall").Str("Action", "Build").Err(err).Msg("")
		return "", fmt.Errorf("GetMuteSoapCall build error: %w", err)
	}

	client := &http.Client{}
	req, err := http.NewRequest("POST", parsedRenderingControlURL.String(), bytes.NewReader(xmlbuilder))
	if err != nil {
		p.Log().Error().Str("Method", "GetMuteSoapCall").Str("Action", "Prepare POST").Err(err).Msg("")
		return "", fmt.Errorf("GetMuteSoapCall POST error: %w", err)
	}

	req.Header = http.Header{
		"SOAPAction":   []string{`"urn:schemas-upnp-org:service:RenderingControl:1#GetMute"`},
		"content-type": []string{"text/xml"},
		"charset":      []string{"utf-8"},
		"Connection":   []string{"close"},
	}

	headerBytesReq, err := json.Marshal(req.Header)
	if err != nil {
		p.Log().Error().Str("Method", "GetMuteSoapCall").Str("Action", "Header Marshaling").Err(err).Msg("")
		return "", fmt.Errorf("GetMuteSoapCall Request Marshaling error: %w", err)
	}

	p.Log().Debug().
		Str("Method", "GetMuteSoapCall").Str("Action", "Request").
		RawJSON("Headers", headerBytesReq).
		Msg(string(xmlbuilder))

	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("GetMuteSoapCall Do POST error: %w", err)
	}
	defer res.Body.Close()

	var buf bytes.Buffer

	tresp := io.TeeReader(res.Body, &buf)

	var respGetMute getMuteRespBody
	if err = xml.NewDecoder(tresp).Decode(&respGetMute); err != nil {
		p.Log().Error().Str("Method", "GetMuteSoapCall").Str("Action", "XML Decode").Err(err).Msg("")
		return "", fmt.Errorf("GetMuteSoapCall XML Decode error: %w", err)
	}

	headerBytesRes, err := json.Marshal(res.Header)
	if err != nil {
		p.Log().Error().Str("Method", "GetMuteSoapCall").Str("Action", "Header Marshaling #2").Err(err).Msg("")
		return "", fmt.Errorf("GetMuteSoapCall Response Marshaling error: %w", err)
	}

	resBytes, err := io.ReadAll(&buf)
	if err != nil {
		p.Log().Error().Str("Method", "GetMuteSoapCall").Str("Action", "Readall").Err(err).Msg("")
		return "", fmt.Errorf("GetMuteSoapCall Failed to read response: %w", err)
	}

	p.Log().Debug().
		Str("Method", "GetMuteSoapCall").Str("Action", "Response").Str("Status Code", strconv.Itoa(res.StatusCode)).
		RawJSON("Headers", headerBytesRes).
		Msg(string(resBytes))

	return respGetMute.Body.GetMuteResponse.CurrentMute, nil
}

// SetMuteSoapCall returns true if muted and false if not muted.
func (p *TVPayload) SetMuteSoapCall(number string) error {
	parsedRenderingControlURL, err := url.Parse(p.RenderingControlURL)
	if err != nil {
		p.Log().Error().Str("Method", "SetMuteSoapCall").Str("Action", "URL Parse").Err(err).Msg("")
		return fmt.Errorf("SetMuteSoapCall parse error: %w", err)
	}

	var xmlbuilder []byte

	xmlbuilder, err = setMuteSoapBuild(number)
	if err != nil {
		p.Log().Error().Str("Method", "SetMuteSoapCall").Str("Action", "Build").Err(err).Msg("")
		return fmt.Errorf("SetMuteSoapCall build error: %w", err)
	}

	client := &http.Client{}
	req, err := http.NewRequest("POST", parsedRenderingControlURL.String(), bytes.NewReader(xmlbuilder))
	if err != nil {
		p.Log().Error().Str("Method", "SetMuteSoapCall").Str("Action", "Prepare POST").Err(err).Msg("")
		return fmt.Errorf("SetMuteSoapCall POST error: %w", err)
	}

	req.Header = http.Header{
		"SOAPAction":   []string{`"urn:schemas-upnp-org:service:RenderingControl:1#SetMute"`},
		"content-type": []string{"text/xml"},
		"charset":      []string{"utf-8"},
		"Connection":   []string{"close"},
	}

	headerBytesReq, err := json.Marshal(req.Header)
	if err != nil {
		p.Log().Error().Str("Method", "SetMuteSoapCall").Str("Action", "Header Marshaling").Err(err).Msg("")
		return fmt.Errorf("SetMuteSoapCall Request Marshaling error: %w", err)
	}

	p.Log().Debug().
		Str("Method", "SetMuteSoapCall").Str("Action", "Request").
		RawJSON("Headers", headerBytesReq).
		Msg(string(xmlbuilder))

	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("SetMuteSoapCall Do POST error: %w", err)
	}
	defer res.Body.Close()

	headerBytesRes, err := json.Marshal(res.Header)
	if err != nil {
		p.Log().Error().Str("Method", "SetMuteSoapCall").Str("Action", "Header Marshaling #2").Err(err).Msg("")
		return fmt.Errorf("SetMuteSoapCall Response Marshaling error: %w", err)
	}

	resBytes, err := io.ReadAll(res.Body)
	if err != nil {
		p.Log().Error().Str("Method", "SetMuteSoapCall").Str("Action", "Readall").Err(err).Msg("")
		return fmt.Errorf("SetMuteSoapCall Failed to read response: %w", err)
	}

	p.Log().Debug().
		Str("Method", "SetMuteSoapCall").Str("Action", "Response").Str("Status Code", strconv.Itoa(res.StatusCode)).
		RawJSON("Headers", headerBytesRes).
		Msg(string(resBytes))

	return nil
}

// GetVolumeSoapCall returns tue volume level for our device.
func (p *TVPayload) GetVolumeSoapCall() (int, error) {
	parsedRenderingControlURL, err := url.Parse(p.RenderingControlURL)
	if err != nil {
		p.Log().Error().Str("Method", "GetVolumeSoapCall").Str("Action", "URL Parse").Err(err).Msg("")
		return 0, fmt.Errorf("GetVolumeSoapCall parse error: %w", err)
	}

	var xmlbuilder []byte

	xmlbuilder, err = getVolumeSoapBuild()
	if err != nil {
		p.Log().Error().Str("Method", "GetVolumeSoapCall").Str("Action", "Build").Err(err).Msg("")
		return 0, fmt.Errorf("GetVolumeSoapCall build error: %w", err)
	}

	client := &http.Client{}
	req, err := http.NewRequest("POST", parsedRenderingControlURL.String(), bytes.NewReader(xmlbuilder))
	if err != nil {
		p.Log().Error().Str("Method", "GetVolumeSoapCall").Str("Action", "Prepare POST").Err(err).Msg("")
		return 0, fmt.Errorf("GetVolumeSoapCall POST error: %w", err)
	}

	req.Header = http.Header{
		"SOAPAction":   []string{`"urn:schemas-upnp-org:service:RenderingControl:1#GetVolume"`},
		"content-type": []string{"text/xml"},
		"charset":      []string{"utf-8"},
		"Connection":   []string{"close"},
	}

	headerBytesReq, err := json.Marshal(req.Header)
	if err != nil {
		p.Log().Error().Str("Method", "GetVolumeSoapCall").Str("Action", "Header Marshaling").Err(err).Msg("")
		return 0, fmt.Errorf("GetVolumeSoapCall Request Marshaling error: %w", err)
	}

	p.Log().Debug().
		Str("Method", "GetVolumeSoapCall").Str("Action", "Request").
		RawJSON("Headers", headerBytesReq).
		Msg(string(xmlbuilder))

	res, err := client.Do(req)
	if err != nil {
		p.Log().Error().Str("Method", "GetVolumeSoapCall").Str("Action", "Do POST").Err(err).Msg("")
		return 0, fmt.Errorf("GetVolumeSoapCall Do POST error: %w", err)
	}
	defer res.Body.Close()

	var buf bytes.Buffer

	tresp := io.TeeReader(res.Body, &buf)

	var respGetVolume getVolumeRespBody
	if err = xml.NewDecoder(tresp).Decode(&respGetVolume); err != nil {
		p.Log().Error().Str("Method", "GetVolumeSoapCall").Str("Action", "XML Decode").Err(err).Msg("")
		return 0, fmt.Errorf("GetVolumeSoapCall XML Decode error: %w", err)
	}

	intVolume, err := strconv.Atoi(respGetVolume.Body.GetVolumeResponse.CurrentVolume)
	if err != nil {
		p.Log().Error().Str("Method", "GetVolumeSoapCall").Str("Action", "Parse Volume").Err(err).Msg("")
		return 0, fmt.Errorf("GetVolumeSoapCall failed to parse volume value: %w", err)
	}

	if intVolume < 0 {
		intVolume = 0
	}

	headerBytesRes, err := json.Marshal(res.Header)
	if err != nil {
		p.Log().Error().Str("Method", "GetVolumeSoapCall").Str("Action", "Header Marshaling #2").Err(err).Msg("")
		return 0, fmt.Errorf("GetVolumeSoapCall Response Marshaling error: %w", err)
	}

	resBytes, err := io.ReadAll(&buf)
	if err != nil {
		p.Log().Error().Str("Method", "GetVolumeSoapCall").Str("Action", "Readall").Err(err).Msg("")
		return 0, fmt.Errorf("GetVolumeSoapCall Failed to read response: %w", err)
	}

	p.Log().Debug().
		Str("Method", "GetVolumeSoapCall").Str("Action", "Response").Str("Status Code", strconv.Itoa(res.StatusCode)).
		RawJSON("Headers", headerBytesRes).
		Msg(string(resBytes))

	return intVolume, nil
}

// SetVolumeSoapCall sets the desired volume level.
func (p *TVPayload) SetVolumeSoapCall(v string) error {
	parsedRenderingControlURL, err := url.Parse(p.RenderingControlURL)
	if err != nil {
		p.Log().Error().Str("Method", "SetVolumeSoapCall").Str("Action", "URL Parse").Err(err).Msg("")
		return fmt.Errorf("SetVolumeSoapCall parse error: %w", err)
	}

	var xmlbuilder []byte

	xmlbuilder, err = setVolumeSoapBuild(v)
	if err != nil {
		p.Log().Error().Str("Method", "SetVolumeSoapCall").Str("Action", "Build").Err(err).Msg("")
		return fmt.Errorf("SetVolumeSoapCall build error: %w", err)
	}

	client := &http.Client{}
	req, err := http.NewRequest("POST", parsedRenderingControlURL.String(), bytes.NewReader(xmlbuilder))
	if err != nil {
		p.Log().Error().Str("Method", "SetVolumeSoapCall").Str("Action", "Prepare POST").Err(err).Msg("")
		return fmt.Errorf("SetVolumeSoapCall POST error: %w", err)
	}

	req.Header = http.Header{
		"SOAPAction":   []string{`"urn:schemas-upnp-org:service:RenderingControl:1#SetVolume"`},
		"content-type": []string{"text/xml"},
		"charset":      []string{"utf-8"},
		"Connection":   []string{"close"},
	}

	headerBytesReq, err := json.Marshal(req.Header)
	if err != nil {
		p.Log().Error().Str("Method", "SetVolumeSoapCall").Str("Action", "Header Marshaling").Err(err).Msg("")
		return fmt.Errorf("SetVolumeSoapCall Request Marshaling error: %w", err)
	}

	p.Log().Debug().
		Str("Method", "SetVolumeSoapCall").Str("Action", "Request").
		RawJSON("Headers", headerBytesReq).
		Msg(string(xmlbuilder))

	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("SetVolumeSoapCall Do POST error: %w", err)
	}
	defer res.Body.Close()

	headerBytesRes, err := json.Marshal(res.Header)
	if err != nil {
		p.Log().Error().Str("Method", "SetVolumeSoapCall").Str("Action", "Header Marshaling #2").Err(err).Msg("")
		return fmt.Errorf("SetVolumeSoapCall Response Marshaling error: %w", err)
	}

	resBytes, err := io.ReadAll(res.Body)
	if err != nil {
		p.Log().Error().Str("Method", "SetVolumeSoapCall").Str("Action", "Readall").Err(err).Msg("")
		return fmt.Errorf("SetVolumeSoapCall Failed to read response: %w", err)
	}

	p.Log().Debug().
		Str("Method", "SetVolumeSoapCall").Str("Action", "Response").Str("Status Code", strconv.Itoa(res.StatusCode)).
		RawJSON("Headers", headerBytesRes).
		Msg(string(resBytes))

	return nil
}

// GetProtocolInfo requests our device's protocol info.
func (p *TVPayload) GetProtocolInfo() error {
	parsedConnectionManagerURL, err := url.Parse(p.ConnectionManagerURL)
	if err != nil {
		p.Log().Error().Str("Method", "GetProtocolInfo").Str("Action", "URL Parse").Err(err).Msg("")
		return fmt.Errorf("GetProtocolInfo parse error: %w", err)
	}

	var xmlbuilder []byte

	xmlbuilder, err = getProtocolInfoSoapBuild()
	if err != nil {
		p.Log().Error().Str("Method", "GetProtocolInfo").Str("Action", "Build").Err(err).Msg("")
		return fmt.Errorf("GetProtocolInfo build error: %w", err)
	}

	client := &http.Client{}
	req, err := http.NewRequest("POST", parsedConnectionManagerURL.String(), bytes.NewReader(xmlbuilder))
	if err != nil {
		p.Log().Error().Str("Method", "GetProtocolInfo").Str("Action", "Prepare POST").Err(err).Msg("")
		return fmt.Errorf("GetProtocolInfo POST error: %w", err)
	}
	req.Header = http.Header{
		"SOAPAction":   []string{`"urn:schemas-upnp-org:service:ConnectionManager:1#GetProtocolInfo"`},
		"content-type": []string{"text/xml"},
		"charset":      []string{"utf-8"},
		"Connection":   []string{"close"},
	}

	headerBytesReq, err := json.Marshal(req.Header)
	if err != nil {
		p.Log().Error().Str("Method", "GetProtocolInfo").Str("Action", "Header Marshaling").Err(err).Msg("")
		return fmt.Errorf("GetProtocolInfo Request Marshaling error: %w", err)
	}

	p.Log().Debug().
		Str("Method", "GetProtocolInfo").Str("Action", "Request").
		RawJSON("Headers", headerBytesReq).
		Msg(string(xmlbuilder))

	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GetProtocolInfo Do POST error: %w", err)
	}
	defer res.Body.Close()

	headerBytesRes, err := json.Marshal(res.Header)
	if err != nil {
		p.Log().Error().Str("Method", "GetProtocolInfo").Str("Action", "Header Marshaling #2").Err(err).Msg("")
		return fmt.Errorf("GetProtocolInfo Response Marshaling error: %w", err)
	}

	resBytes, err := io.ReadAll(res.Body)
	if err != nil {
		p.Log().Error().Str("Method", "GetProtocolInfo").Str("Action", "Readall").Err(err).Msg("")
		return fmt.Errorf("GetProtocolInfo Failed to read response: %w", err)
	}

	p.Log().Debug().
		Str("Method", "GetProtocolInfo").Str("Action", "Response").Str("Status Code", strconv.Itoa(res.StatusCode)).
		RawJSON("Headers", headerBytesRes).
		Msg(string(resBytes))

	if err := parseProtocolInfo(resBytes, p.MediaType); err != nil {
		return fmt.Errorf("GetProtocolInfo Selected device does not support the media type: %w", err)
	}

	return nil
}

// Gapless requests our device's media info and returns the Next URI.
func (p *TVPayload) Gapless() (string, error) {
	if p == nil {
		return "", errors.New("Gapless, nil tvdata")
	}

	parsedURLtransport, err := url.Parse(p.ControlURL)
	if err != nil {
		p.Log().Error().Str("Method", "Gapless").Str("Action", "URL Parse").Err(err).Msg("")
		return "", fmt.Errorf("Gapless parse error: %w", err)
	}

	var xmlbuilder []byte

	xmlbuilder, err = getMediaInfoSoapBuild()
	if err != nil {
		p.Log().Error().Str("Method", "Gapless").Str("Action", "Build").Err(err).Msg("")
		return "", fmt.Errorf("Gapless build error: %w", err)
	}

	client := &http.Client{}
	req, err := http.NewRequest("POST", parsedURLtransport.String(), bytes.NewReader(xmlbuilder))
	if err != nil {
		p.Log().Error().Str("Method", "Gapless").Str("Action", "Prepare POST").Err(err).Msg("")
		return "", fmt.Errorf("Gapless POST error: %w", err)
	}
	req.Header = http.Header{
		"SOAPAction":   []string{`"urn:schemas-upnp-org:service:AVTransport:1#GetMediaInfo"`},
		"content-type": []string{"text/xml"},
		"charset":      []string{"utf-8"},
		"Connection":   []string{"close"},
	}

	headerBytesReq, err := json.Marshal(req.Header)
	if err != nil {
		p.Log().Error().Str("Method", "Gapless").Str("Action", "Header Marshaling").Err(err).Msg("")
		return "", fmt.Errorf("Gapless Request Marshaling error: %w", err)
	}

	p.Log().Debug().
		Str("Method", "Gapless").Str("Action", "Request").
		RawJSON("Headers", headerBytesReq).
		Msg(string(xmlbuilder))

	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Gapless Do POST error: %w", err)
	}
	defer res.Body.Close()

	headerBytesRes, err := json.Marshal(res.Header)
	if err != nil {
		p.Log().Error().Str("Method", "Gapless").Str("Action", "Header Marshaling #2").Err(err).Msg("")
		return "", fmt.Errorf("Gapless Response Marshaling error: %w", err)
	}

	resBytes, err := io.ReadAll(res.Body)
	if err != nil {
		p.Log().Error().Str("Method", "Gapless").Str("Action", "Readall").Err(err).Msg("")
		return "", fmt.Errorf("Gapless Failed to read response: %w", err)
	}

	p.Log().Debug().
		Str("Method", "Gapless").Str("Action", "Response").Str("Status Code", strconv.Itoa(res.StatusCode)).
		RawJSON("Headers", headerBytesRes).
		Msg(string(resBytes))

	var respMedialInfo getMediaInfoResponse

	if err := xml.Unmarshal(resBytes, &respMedialInfo); err != nil {
		p.Log().Error().Str("Method", "Gapless").Str("Action", "Unmarshal").Err(err).Msg("")
		return "", fmt.Errorf("Gapless Failed to unmarshal response: %w", err)
	}

	nextURI := respMedialInfo.Body.GetMediaInfoResponse.NextURI

	return nextURI, nil
}

// GetTransportInfo .
func (p *TVPayload) GetTransportInfo() ([]string, error) {
	if p == nil {
		return nil, errors.New("GetTransportInfo, nil tvdata")
	}

	parsedURLtransport, err := url.Parse(p.ControlURL)
	if err != nil {
		p.Log().Error().Str("Method", "GetTransportInfo").Str("Action", "URL Parse").Err(err).Msg("")
		return nil, fmt.Errorf("GetTransportInfo parse error: %w", err)
	}

	var xmlbuilder []byte

	xmlbuilder, err = getTransportInfoSoapBuild()
	if err != nil {
		p.Log().Error().Str("Method", "GetTransportInfo").Str("Action", "Build").Err(err).Msg("")
		return nil, fmt.Errorf("GetTransportInfo build error: %w", err)
	}

	client := &http.Client{}
	req, err := http.NewRequest("POST", parsedURLtransport.String(), bytes.NewReader(xmlbuilder))
	if err != nil {
		p.Log().Error().Str("Method", "GetTransportInfo").Str("Action", "Prepare POST").Err(err).Msg("")
		return nil, fmt.Errorf("GetTransportInfo POST error: %w", err)
	}
	req.Header = http.Header{
		"SOAPAction":   []string{`"urn:schemas-upnp-org:service:AVTransport:1#GetTransportInfo"`},
		"content-type": []string{"text/xml"},
		"charset":      []string{"utf-8"},
		"Connection":   []string{"close"},
	}

	headerBytesReq, err := json.Marshal(req.Header)
	if err != nil {
		p.Log().Error().Str("Method", "GetTransportInfo").Str("Action", "Header Marshaling").Err(err).Msg("")
		return nil, fmt.Errorf("GetTransportInfo Request Marshaling error: %w", err)
	}

	p.Log().Debug().
		Str("Method", "GetTransportInfo").Str("Action", "Request").
		RawJSON("Headers", headerBytesReq).
		Msg(string(xmlbuilder))

	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GetTransportInfo Do POST error: %w", err)
	}
	defer res.Body.Close()

	headerBytesRes, err := json.Marshal(res.Header)
	if err != nil {
		p.Log().Error().Str("Method", "GetTransportInfo").Str("Action", "Header Marshaling #2").Err(err).Msg("")
		return nil, fmt.Errorf("GetTransportInfo Response Marshaling error: %w", err)
	}

	resBytes, err := io.ReadAll(res.Body)
	if err != nil {
		p.Log().Error().Str("Method", "GetTransportInfo").Str("Action", "Readall").Err(err).Msg("")
		return nil, fmt.Errorf("GetTransportInfo Failed to read response: %w", err)
	}

	p.Log().Debug().
		Str("Method", "GetTransportInfo").Str("Action", "Response").Str("Status Code", strconv.Itoa(res.StatusCode)).
		RawJSON("Headers", headerBytesRes).
		Msg(string(resBytes))

	var respTransportInfo getTransportInfoResponse

	if err := xml.Unmarshal(resBytes, &respTransportInfo); err != nil {
		p.Log().Error().Str("Method", "GetTransportInfo").Str("Action", "Unmarshal").Err(err).Msg("")
		return nil, fmt.Errorf("GetTransportInfo Failed to unmarshal response: %w", err)
	}

	r := respTransportInfo.Body.GetTransportInfoResponse
	state := r.CurrentTransportState
	status := r.CurrentTransportStatus
	speed := r.CurrentSpeed

	return []string{state, status, speed}, nil
}

// GetPositionInfo .
func (p *TVPayload) GetPositionInfo() ([]string, error) {
	if p == nil {
		return nil, errors.New("GetPositionInfo, nil tvdata")
	}

	parsedURLtransport, err := url.Parse(p.ControlURL)
	if err != nil {
		p.Log().Error().Str("Method", "GetPositionInfo").Str("Action", "URL Parse").Err(err).Msg("")
		return nil, fmt.Errorf("GetPositionInfo parse error: %w", err)
	}

	var xmlbuilder []byte

	xmlbuilder, err = getPositionInfoSoapBuild()
	if err != nil {
		p.Log().Error().Str("Method", "GetPositionInfo").Str("Action", "Build").Err(err).Msg("")
		return nil, fmt.Errorf("GetPositionInfo build error: %w", err)
	}

	client := &http.Client{}
	req, err := http.NewRequest("POST", parsedURLtransport.String(), bytes.NewReader(xmlbuilder))
	if err != nil {
		p.Log().Error().Str("Method", "GetPositionInfo").Str("Action", "Prepare POST").Err(err).Msg("")
		return nil, fmt.Errorf("GetPositionInfo POST error: %w", err)
	}
	req.Header = http.Header{
		"SOAPAction":   []string{`"urn:schemas-upnp-org:service:AVTransport:1#GetPositionInfo"`},
		"content-type": []string{"text/xml"},
		"charset":      []string{"utf-8"},
		"Connection":   []string{"close"},
	}

	headerBytesReq, err := json.Marshal(req.Header)
	if err != nil {
		p.Log().Error().Str("Method", "GetPositionInfo").Str("Action", "Header Marshaling").Err(err).Msg("")
		return nil, fmt.Errorf("GetPositionInfo Request Marshaling error: %w", err)
	}

	p.Log().Debug().
		Str("Method", "GetPositionInfo").Str("Action", "Request").
		RawJSON("Headers", headerBytesReq).
		Msg(string(xmlbuilder))

	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GetPositionInfo Do POST error: %w", err)
	}
	defer res.Body.Close()

	headerBytesRes, err := json.Marshal(res.Header)
	if err != nil {
		p.Log().Error().Str("Method", "GetPositionInfo").Str("Action", "Header Marshaling #2").Err(err).Msg("")
		return nil, fmt.Errorf("GetPositionInfo Response Marshaling error: %w", err)
	}

	resBytes, err := io.ReadAll(res.Body)
	if err != nil {
		p.Log().Error().Str("Method", "GetPositionInfo").Str("Action", "Readall").Err(err).Msg("")
		return nil, fmt.Errorf("GetPositionInfo Failed to read response: %w", err)
	}

	p.Log().Debug().
		Str("Method", "GetPositionInfo").Str("Action", "Response").Str("Status Code", strconv.Itoa(res.StatusCode)).
		RawJSON("Headers", headerBytesRes).
		Msg(string(resBytes))

	var respPositionInfo getPositionInfoResponse

	if err := xml.Unmarshal(resBytes, &respPositionInfo); err != nil {
		p.Log().Error().Str("Method", "GetPositionInfo").Str("Action", "Unmarshal").Err(err).Msg("")
		return nil, fmt.Errorf("GetPositionInfo Failed to unmarshal response: %w", err)
	}

	r := respPositionInfo.Body.GetPositionInfoResponse
	duration := r.TrackDuration
	reltime := r.RelTime

	return []string{duration, reltime}, nil
}

// SendtoTV is a higher level method that gracefully handles the various
// states when communicating with the DMR devices.
func (p *TVPayload) SendtoTV(action string) error {
	if action == "ClearQueue" {
		if err := p.setNextAVTransportSoapCall(true); err != nil {
			return fmt.Errorf("SendtoTV setNextAVTransportSoapCall call error: %w", err)
		}
		return nil
	}

	if action == "Queue" {
		if err := p.setNextAVTransportSoapCall(false); err != nil {
			return fmt.Errorf("SendtoTV setNextAVTransportSoapCall call error: %w", err)
		}
		return nil
	}

	if action == "Play1" {
		if err := p.GetProtocolInfo(); err != nil {
			return fmt.Errorf("SendtoTV getProtocolInfo call error: %w", err)
		}
		if err := p.SubscribeSoapCall(""); err != nil {
			return fmt.Errorf("SendtoTV subscribe call error: %w", err)
		}
		if err := p.setAVTransportSoapCall(); err != nil {
			return fmt.Errorf("SendtoTV set AVT Transport error: %w", err)
		}
		action = "Play"
	}

	if action == "Stop" {
		p.mu.RLock()
		localStates := make(map[string]*States)
		for key, value := range p.MediaRenderersStates {
			localStates[key] = value
		}
		p.mu.RUnlock()

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

	if err := p.AVTransportActionSoapCall(action); err != nil {
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

	p.mu.Lock()
	defer p.mu.Unlock()
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
	p.mu.Lock()
	defer p.mu.Unlock()
	p.InitialMediaRenderersStates[uuid] = true
	p.MediaRenderersStates[uuid] = &States{}
}

// DeleteMRstate deletes the state entries for the specific UUID.
func (p *TVPayload) DeleteMRstate(uuid string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.InitialMediaRenderersStates, uuid)
	delete(p.MediaRenderersStates, uuid)
}

// SetProcessStopTrue set the stop process to true
func (p *TVPayload) SetProcessStopTrue(uuid string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.MediaRenderersStates[uuid].ProcessStop = true
}

// GetProcessStop returns the processStop value of the specific UUID.
func (p *TVPayload) GetProcessStop(uuid string) (bool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.InitialMediaRenderersStates[uuid] {
		return p.MediaRenderersStates[uuid].ProcessStop, nil
	}

	return true, ErrZombieCallbacks
}

func (p *TVPayload) Log() *zerolog.Logger {
	if p.Logging != nil {
		p.initLogOnce.Do(func() {
			log = zerolog.New(p.Logging).With().Timestamp().Logger()
		})
	} else {
		return &zerolog.Logger{}
	}

	return &log
}

func parseProtocolInfo(b []byte, mt string) error {
	var respProtocolInfo protocolInfoResponse

	// We were unable to detect the media type, so we
	// should allow this to go through by default and
	// hope the DMR accepts it.
	if mt == "/" {
		return nil
	}

	if strings.Contains(mt, "/") {
		mt = strings.Split(mt, "/")[0]
	}

	if err := xml.Unmarshal(b, &respProtocolInfo); err != nil {
		return err
	}

	protocols := strings.Split(respProtocolInfo.Body.GetProtocolInfoResponse.Sink, ",")
	for _, i := range protocols {
		items := strings.Split(i, ":")
		// Here we hardcode check the http-get protocol. We would need to change that
		// if we were to support rtp/rtsp/udp.
		if len(items) == 4 && items[0] == "http-get" && strings.Contains(items[2], "/") {
			ftype := strings.Split(items[2], "/")[0]
			if ftype == mt {
				return nil
			}
		}
	}

	return ErrNoMatchingFileType
}
