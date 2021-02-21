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

func setAVTransportSoapBuild(videoURL, subtitleURL string) ([]byte, error) {
	var videoTitle string

	videoTitlefromURL, err := url.Parse(videoURL)
	if err != nil {
		videoTitle = videoURL
	} else {
		videoTitle = strings.TrimLeft(videoTitlefromURL.Path, "/")
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
			UPNPClass:  "object.item.videoItem.movie",
			DCtitle:    videoTitle,
			ResNode: []ResNode{{
				XMLName:      xml.Name{},
				ProtocolInfo: "http-get:*:video/mp4:*",
				Value:        videoURL,
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
		fmt.Println(err)
		return make([]byte, 0), err
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
				CurrentURI:  videoURL,
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
		fmt.Println(err)
		return make([]byte, 0), err
	}
	// That looks like an issue just with my Samsung TV.
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
		fmt.Println(err)
		return make([]byte, 0), err
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
		fmt.Println(err)
		return make([]byte, 0), err
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
		fmt.Println(err)
		return make([]byte, 0), err
	}

	return append(xmlStart, b...), nil
}
