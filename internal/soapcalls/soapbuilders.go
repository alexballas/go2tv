package soapcalls

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/url"
	"strings"
)

// PlayEnvelope - As in Play Pause Stop.
type PlayEnvelope struct {
	XMLName  xml.Name `xml:"s:Envelope"`
	Schema   string   `xml:"xmlns:s,attr"`
	Encoding string   `xml:"s:encodingStyle,attr"`
	PlayBody PlayBody `xml:"s:Body"`
}

// PlayBody .
type PlayBody struct {
	XMLName    xml.Name   `xml:"s:Body"`
	PlayAction PlayAction `xml:"u:Play"`
}

// PlayAction .
type PlayAction struct {
	XMLName     xml.Name `xml:"u:Play"`
	AVTransport string   `xml:"xmlns:u,attr"`
	InstanceID  string
	Speed       string
}

// PauseEnvelope - As in Play Pause Stop.
type PauseEnvelope struct {
	XMLName   xml.Name  `xml:"s:Envelope"`
	Schema    string    `xml:"xmlns:s,attr"`
	Encoding  string    `xml:"s:encodingStyle,attr"`
	PauseBody PauseBody `xml:"s:Body"`
}

// PauseBody .
type PauseBody struct {
	XMLName     xml.Name    `xml:"s:Body"`
	PauseAction PauseAction `xml:"u:Pause"`
}

// PauseAction .
type PauseAction struct {
	XMLName     xml.Name `xml:"u:Pause"`
	AVTransport string   `xml:"xmlns:u,attr"`
	InstanceID  string
	Speed       string
}

// StopEnvelope - As in Play Pause Stop.
type StopEnvelope struct {
	XMLName  xml.Name `xml:"s:Envelope"`
	Schema   string   `xml:"xmlns:s,attr"`
	Encoding string   `xml:"s:encodingStyle,attr"`
	StopBody StopBody `xml:"s:Body"`
}

// StopBody .
type StopBody struct {
	XMLName    xml.Name   `xml:"s:Body"`
	StopAction StopAction `xml:"u:Stop"`
}

// StopAction .
type StopAction struct {
	XMLName     xml.Name `xml:"u:Stop"`
	AVTransport string   `xml:"xmlns:u,attr"`
	InstanceID  string
	Speed       string
}

// SetAVTransportEnvelope .
type SetAVTransportEnvelope struct {
	XMLName  xml.Name           `xml:"s:Envelope"`
	Schema   string             `xml:"xmlns:s,attr"`
	Encoding string             `xml:"s:encodingStyle,attr"`
	Body     SetAVTransportBody `xml:"s:Body"`
}

// SetAVTransportBody .
type SetAVTransportBody struct {
	XMLName           xml.Name          `xml:"s:Body"`
	SetAVTransportURI SetAVTransportURI `xml:"u:SetAVTransportURI"`
}

// SetAVTransportURI .
type SetAVTransportURI struct {
	XMLName            xml.Name `xml:"u:SetAVTransportURI"`
	AVTransport        string   `xml:"xmlns:u,attr"`
	InstanceID         string
	CurrentURI         string
	CurrentURIMetaData CurrentURIMetaData `xml:"CurrentURIMetaData"`
}

// CurrentURIMetaData .
type CurrentURIMetaData struct {
	XMLName xml.Name `xml:"CurrentURIMetaData"`
	Value   []byte   `xml:",chardata"`
}

// DIDLLite .
type DIDLLite struct {
	XMLName      xml.Name     `xml:"DIDL-Lite"`
	SchemaDIDL   string       `xml:"xmlns,attr"`
	DC           string       `xml:"xmlns:dc,attr"`
	Sec          string       `xml:"xmlns:sec,attr"`
	SchemaUPNP   string       `xml:"xmlns:upnp,attr"`
	DIDLLiteItem DIDLLiteItem `xml:"item"`
}

// DIDLLiteItem .
type DIDLLiteItem struct {
	XMLName          xml.Name         `xml:"item"`
	ID               string           `xml:"id,attr"`
	ParentID         string           `xml:"parentID,attr"`
	Restricted       string           `xml:"restricted,attr"`
	UPNPClass        string           `xml:"upnp:class"`
	DCtitle          string           `xml:"dc:title"`
	ResNode          []ResNode        `xml:"res"`
	SecCaptionInfo   SecCaptionInfo   `xml:"sec:CaptionInfo"`
	SecCaptionInfoEx SecCaptionInfoEx `xml:"sec:CaptionInfoEx"`
}

// ResNode .
type ResNode struct {
	XMLName      xml.Name `xml:"res"`
	ProtocolInfo string   `xml:"protocolInfo,attr"`
	Value        string   `xml:",chardata"`
}

// SecCaptionInfo .
type SecCaptionInfo struct {
	XMLName xml.Name `xml:"sec:CaptionInfo"`
	Type    string   `xml:"sec:type,attr"`
	Value   string   `xml:",chardata"`
}

// SecCaptionInfoEx .
type SecCaptionInfoEx struct {
	XMLName xml.Name `xml:"sec:CaptionInfoEx"`
	Type    string   `xml:"sec:type,attr"`
	Value   string   `xml:",chardata"`
}

// SetMuteEnvelope - As in Play Pause Stop.
type SetMuteEnvelope struct {
	XMLName     xml.Name    `xml:"s:Envelope"`
	Schema      string      `xml:"xmlns:s,attr"`
	Encoding    string      `xml:"s:encodingStyle,attr"`
	SetMuteBody SetMuteBody `xml:"s:Body"`
}

// SetMuteBody .
type SetMuteBody struct {
	XMLName       xml.Name      `xml:"s:Body"`
	SetMuteAction SetMuteAction `xml:"u:SetMute"`
}

// SetMuteAction .
type SetMuteAction struct {
	XMLName          xml.Name `xml:"u:SetMute"`
	RenderingControl string   `xml:"xmlns:u,attr"`
	InstanceID       string
	Channel          string
	DesiredMute      string
}

// GetMuteEnvelope - As in Play Pause Stop.
type GetMuteEnvelope struct {
	XMLName     xml.Name    `xml:"s:Envelope"`
	Schema      string      `xml:"xmlns:s,attr"`
	Encoding    string      `xml:"s:encodingStyle,attr"`
	GetMuteBody GetMuteBody `xml:"s:Body"`
}

// GetMuteBody .
type GetMuteBody struct {
	XMLName       xml.Name      `xml:"s:Body"`
	GetMuteAction GetMuteAction `xml:"u:GetMute"`
}

// GetMuteAction .
type GetMuteAction struct {
	XMLName          xml.Name `xml:"u:GetMute"`
	RenderingControl string   `xml:"xmlns:u,attr"`
	InstanceID       string
	Channel          string
}

func setAVTransportSoapBuild(mediaURL, mediaType, subtitleURL string) ([]byte, error) {
	var mediaTitle string

	mediaTypeSlice := strings.Split(mediaType, "/")

	var class string
	switch mediaTypeSlice[0] {
	case "audio":
		class = "object.item.audioItem.musicTrack"
	default:
		class = "object.item.videoItem.movie"
	}

	mediaTitle = mediaURL
	mediaTitlefromURL, err := url.Parse(mediaURL)
	if err == nil {
		mediaTitle = strings.TrimLeft(mediaTitlefromURL.Path, "/")
	}

	l := DIDLLite{
		XMLName:    xml.Name{},
		SchemaDIDL: "urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/",
		DC:         "http://purl.org/dc/elements/1.1/",
		Sec:        "http://www.sec.co.kr/",
		SchemaUPNP: "urn:schemas-upnp-org:metadata-1-0/upnp/",
		DIDLLiteItem: DIDLLiteItem{
			XMLName:    xml.Name{},
			ID:         "0",
			ParentID:   "-1",
			Restricted: "false",
			UPNPClass:  class,
			DCtitle:    mediaTitle,
			ResNode: []ResNode{{
				XMLName:      xml.Name{},
				ProtocolInfo: fmt.Sprintf("http-get:*:%s:*", mediaType),
				Value:        mediaURL,
			}, {
				XMLName:      xml.Name{},
				ProtocolInfo: "http-get:*:text/srt:*",
				Value:        subtitleURL,
			},
			},
			SecCaptionInfo: SecCaptionInfo{
				XMLName: xml.Name{},
				Type:    "srt",
				Value:   subtitleURL,
			},
			SecCaptionInfoEx: SecCaptionInfoEx{
				XMLName: xml.Name{},
				Type:    "srt",
				Value:   subtitleURL,
			},
		},
	}
	a, err := xml.Marshal(l)
	if err != nil {
		return nil, fmt.Errorf("setAVTransportSoapBuild #1 Marshal error: %w", err)
	}

	d := SetAVTransportEnvelope{
		XMLName:  xml.Name{},
		Schema:   "http://schemas.xmlsoap.org/soap/envelope/",
		Encoding: "http://schemas.xmlsoap.org/soap/encoding/",
		Body: SetAVTransportBody{
			XMLName: xml.Name{},
			SetAVTransportURI: SetAVTransportURI{
				XMLName:     xml.Name{},
				AVTransport: "urn:schemas-upnp-org:service:AVTransport:1",
				InstanceID:  "0",
				CurrentURI:  mediaURL,
				CurrentURIMetaData: CurrentURIMetaData{
					XMLName: xml.Name{},
					Value:   a,
				},
			},
		},
	}
	xmlStart := []byte("<?xml version='1.0' encoding='utf-8'?>")
	b, err := xml.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("setAVTransportSoapBuild #2 Marshal error: %w", err)
	}

	// Samsung TV hack.
	b = bytes.ReplaceAll(b, []byte("&#34;"), []byte(`"`))
	b = bytes.ReplaceAll(b, []byte("&amp;"), []byte("&"))

	return append(xmlStart, b...), nil
}

func playSoapBuild() ([]byte, error) {
	d := PlayEnvelope{
		XMLName:  xml.Name{},
		Schema:   "http://schemas.xmlsoap.org/soap/envelope/",
		Encoding: "http://schemas.xmlsoap.org/soap/encoding/",
		PlayBody: PlayBody{
			XMLName: xml.Name{},
			PlayAction: PlayAction{
				XMLName:     xml.Name{},
				AVTransport: "urn:schemas-upnp-org:service:AVTransport:1",
				InstanceID:  "0",
				Speed:       "1",
			},
		},
	}
	xmlStart := []byte("<?xml version='1.0' encoding='utf-8'?>")
	b, err := xml.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("playSoapBuild Marshal error: %w", err)
	}

	return append(xmlStart, b...), nil
}

func stopSoapBuild() ([]byte, error) {
	d := StopEnvelope{
		XMLName:  xml.Name{},
		Schema:   "http://schemas.xmlsoap.org/soap/envelope/",
		Encoding: "http://schemas.xmlsoap.org/soap/encoding/",
		StopBody: StopBody{
			XMLName: xml.Name{},
			StopAction: StopAction{
				XMLName:     xml.Name{},
				AVTransport: "urn:schemas-upnp-org:service:AVTransport:1",
				InstanceID:  "0",
				Speed:       "1",
			},
		},
	}
	xmlStart := []byte("<?xml version='1.0' encoding='utf-8'?>")
	b, err := xml.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("stopSoapBuild Marshal error: %w", err)
	}

	return append(xmlStart, b...), nil
}

func pauseSoapBuild() ([]byte, error) {
	d := PauseEnvelope{
		XMLName:  xml.Name{},
		Schema:   "http://schemas.xmlsoap.org/soap/envelope/",
		Encoding: "http://schemas.xmlsoap.org/soap/encoding/",
		PauseBody: PauseBody{
			XMLName: xml.Name{},
			PauseAction: PauseAction{
				XMLName:     xml.Name{},
				AVTransport: "urn:schemas-upnp-org:service:AVTransport:1",
				InstanceID:  "0",
				Speed:       "1",
			},
		},
	}
	xmlStart := []byte("<?xml version='1.0' encoding='utf-8'?>")
	b, err := xml.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("pauseSoapBuild Marshal error: %w", err)
	}

	return append(xmlStart, b...), nil
}

func setMuteSoapBuild(m string) ([]byte, error) {
	d := SetMuteEnvelope{
		XMLName:  xml.Name{},
		Schema:   "http://schemas.xmlsoap.org/soap/envelope/",
		Encoding: "http://schemas.xmlsoap.org/soap/encoding/",
		SetMuteBody: SetMuteBody{
			XMLName: xml.Name{},
			SetMuteAction: SetMuteAction{
				XMLName:          xml.Name{},
				RenderingControl: "urn:schemas-upnp-org:service:RenderingControl:1",
				InstanceID:       "0",
				Channel:          "Master",
				DesiredMute:      m,
			},
		},
	}
	xmlStart := []byte("<?xml version='1.0' encoding='utf-8'?>")
	b, err := xml.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("setMuteSoapBuild Marshal error: %w", err)
	}

	return append(xmlStart, b...), nil
}

func getMuteSoapBuild() ([]byte, error) {
	d := GetMuteEnvelope{
		XMLName:  xml.Name{},
		Schema:   "http://schemas.xmlsoap.org/soap/envelope/",
		Encoding: "http://schemas.xmlsoap.org/soap/encoding/",
		GetMuteBody: GetMuteBody{
			XMLName: xml.Name{},
			GetMuteAction: GetMuteAction{
				XMLName:          xml.Name{},
				RenderingControl: "urn:schemas-upnp-org:service:RenderingControl:1",
				InstanceID:       "0",
				Channel:          "Master",
			},
		},
	}
	xmlStart := []byte("<?xml version='1.0' encoding='utf-8'?>")
	b, err := xml.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("getMuteSoapBuild Marshal error: %w", err)
	}

	return append(xmlStart, b...), nil
}
