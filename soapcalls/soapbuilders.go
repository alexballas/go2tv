package soapcalls

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/alexballas/go2tv/utils"
	"github.com/pkg/errors"
)

var (
	ErrSetMuteInput = errors.New("setMuteSoapBuild input error. Was expecting 0 or 1.")
)

type playEnvelope struct {
	XMLName  xml.Name `xml:"s:Envelope"`
	Schema   string   `xml:"xmlns:s,attr"`
	Encoding string   `xml:"s:encodingStyle,attr"`
	PlayBody playBody `xml:"s:Body"`
}

type playBody struct {
	XMLName    xml.Name   `xml:"s:Body"`
	PlayAction playAction `xml:"u:Play"`
}

type playAction struct {
	XMLName     xml.Name `xml:"u:Play"`
	AVTransport string   `xml:"xmlns:u,attr"`
	InstanceID  string
	Speed       string
}

type pauseEnvelope struct {
	XMLName   xml.Name  `xml:"s:Envelope"`
	Schema    string    `xml:"xmlns:s,attr"`
	Encoding  string    `xml:"s:encodingStyle,attr"`
	PauseBody pauseBody `xml:"s:Body"`
}

type pauseBody struct {
	XMLName     xml.Name    `xml:"s:Body"`
	PauseAction pauseAction `xml:"u:Pause"`
}

type pauseAction struct {
	XMLName     xml.Name `xml:"u:Pause"`
	AVTransport string   `xml:"xmlns:u,attr"`
	InstanceID  string
	Speed       string
}

type stopEnvelope struct {
	XMLName  xml.Name `xml:"s:Envelope"`
	Schema   string   `xml:"xmlns:s,attr"`
	Encoding string   `xml:"s:encodingStyle,attr"`
	StopBody stopBody `xml:"s:Body"`
}

type stopBody struct {
	XMLName    xml.Name   `xml:"s:Body"`
	StopAction stopAction `xml:"u:Stop"`
}

type stopAction struct {
	XMLName     xml.Name `xml:"u:Stop"`
	AVTransport string   `xml:"xmlns:u,attr"`
	InstanceID  string
	Speed       string
}

type setAVTransportEnvelope struct {
	XMLName  xml.Name           `xml:"s:Envelope"`
	Schema   string             `xml:"xmlns:s,attr"`
	Encoding string             `xml:"s:encodingStyle,attr"`
	Body     setAVTransportBody `xml:"s:Body"`
}

type setAVTransportBody struct {
	XMLName           xml.Name          `xml:"s:Body"`
	SetAVTransportURI setAVTransportURI `xml:"u:SetAVTransportURI"`
}

type setAVTransportURI struct {
	XMLName            xml.Name `xml:"u:SetAVTransportURI"`
	AVTransport        string   `xml:"xmlns:u,attr"`
	InstanceID         string
	CurrentURI         string
	CurrentURIMetaData currentURIMetaData `xml:"CurrentURIMetaData"`
}

type currentURIMetaData struct {
	XMLName xml.Name `xml:"CurrentURIMetaData"`
	Value   []byte   `xml:",chardata"`
}

type setNextAVTransportEnvelope struct {
	XMLName  xml.Name               `xml:"s:Envelope"`
	Schema   string                 `xml:"xmlns:s,attr"`
	Encoding string                 `xml:"s:encodingStyle,attr"`
	Body     setNextAVTransportBody `xml:"s:Body"`
}

type setNextAVTransportBody struct {
	XMLName               xml.Name              `xml:"s:Body"`
	SetNextAVTransportURI setNextAVTransportURI `xml:"u:SetNextAVTransportURI"`
}

type setNextAVTransportURI struct {
	XMLName         xml.Name `xml:"u:SetNextAVTransportURI"`
	AVTransport     string   `xml:"xmlns:u,attr"`
	InstanceID      string
	NextURI         string
	NextURIMetaData nextURIMetaData `xml:"NextURIMetaData"`
}

type nextURIMetaData struct {
	XMLName xml.Name `xml:"NextURIMetaData"`
	Value   []byte   `xml:",chardata"`
}

type didLLite struct {
	XMLName      xml.Name     `xml:"DIDL-Lite"`
	SchemaDIDL   string       `xml:"xmlns,attr"`
	DC           string       `xml:"xmlns:dc,attr"`
	Sec          string       `xml:"xmlns:sec,attr"`
	SchemaUPNP   string       `xml:"xmlns:upnp,attr"`
	DIDLLiteItem didLLiteItem `xml:"item"`
}

type didLLiteItem struct {
	SecCaptionInfo   *secCaptionInfo   `xml:"sec:CaptionInfo,omitempty"`
	SecCaptionInfoEx *secCaptionInfoEx `xml:"sec:CaptionInfoEx,omitempty"`
	XMLName          xml.Name          `xml:"item"`
	DCtitle          string            `xml:"dc:title"`
	UPNPClass        string            `xml:"upnp:class"`
	ID               string            `xml:"id,attr"`
	ParentID         string            `xml:"parentID,attr"`
	Restricted       string            `xml:"restricted,attr"`
	ResNode          []resNode         `xml:"res"`
}

type resNode struct {
	XMLName      xml.Name `xml:"res"`
	Duration     string   `xml:"duration,attr,omitempty"`
	ProtocolInfo string   `xml:"protocolInfo,attr"`
	Value        string   `xml:",chardata"`
}

type secCaptionInfo struct {
	XMLName xml.Name `xml:"sec:CaptionInfo"`
	Type    string   `xml:"sec:type,attr"`
	Value   string   `xml:",chardata"`
}

type secCaptionInfoEx struct {
	XMLName xml.Name `xml:"sec:CaptionInfoEx"`
	Type    string   `xml:"sec:type,attr"`
	Value   string   `xml:",chardata"`
}

type setMuteEnvelope struct {
	XMLName     xml.Name    `xml:"s:Envelope"`
	Schema      string      `xml:"xmlns:s,attr"`
	Encoding    string      `xml:"s:encodingStyle,attr"`
	SetMuteBody setMuteBody `xml:"s:Body"`
}

type setMuteBody struct {
	XMLName       xml.Name      `xml:"s:Body"`
	SetMuteAction setMuteAction `xml:"u:SetMute"`
}

type setMuteAction struct {
	XMLName          xml.Name `xml:"u:SetMute"`
	RenderingControl string   `xml:"xmlns:u,attr"`
	InstanceID       string
	Channel          string
	DesiredMute      string
}

type getMuteEnvelope struct {
	XMLName     xml.Name    `xml:"s:Envelope"`
	Schema      string      `xml:"xmlns:s,attr"`
	Encoding    string      `xml:"s:encodingStyle,attr"`
	GetMuteBody getMuteBody `xml:"s:Body"`
}

type getMuteBody struct {
	XMLName       xml.Name      `xml:"s:Body"`
	GetMuteAction getMuteAction `xml:"u:GetMute"`
}

type getMuteAction struct {
	XMLName          xml.Name `xml:"u:GetMute"`
	RenderingControl string   `xml:"xmlns:u,attr"`
	InstanceID       string
	Channel          string
}

type getVolumeEnvelope struct {
	XMLName       xml.Name      `xml:"s:Envelope"`
	Schema        string        `xml:"xmlns:s,attr"`
	Encoding      string        `xml:"s:encodingStyle,attr"`
	GetVolumeBody getVolumeBody `xml:"s:Body"`
}

type getVolumeBody struct {
	XMLName         xml.Name        `xml:"s:Body"`
	GetVolumeAction getVolumeAction `xml:"u:GetVolume"`
}

type getVolumeAction struct {
	XMLName          xml.Name `xml:"u:GetVolume"`
	RenderingControl string   `xml:"xmlns:u,attr"`
	InstanceID       string
	Channel          string
}

type setVolumeEnvelope struct {
	XMLName       xml.Name      `xml:"s:Envelope"`
	Schema        string        `xml:"xmlns:s,attr"`
	Encoding      string        `xml:"s:encodingStyle,attr"`
	SetVolumeBody setVolumeBody `xml:"s:Body"`
}

type setVolumeBody struct {
	XMLName         xml.Name        `xml:"s:Body"`
	SetVolumeAction setVolumeAction `xml:"u:SetVolume"`
}

type setVolumeAction struct {
	XMLName          xml.Name `xml:"u:SetVolume"`
	RenderingControl string   `xml:"xmlns:u,attr"`
	InstanceID       string
	Channel          string
	DesiredVolume    string
}

type getProtocolInfoEnvelope struct {
	XMLName             xml.Name            `xml:"s:Envelope"`
	Schema              string              `xml:"xmlns:s,attr"`
	Encoding            string              `xml:"s:encodingStyle,attr"`
	GetProtocolInfoBody getProtocolInfoBody `xml:"s:Body"`
}

type getProtocolInfoBody struct {
	XMLName               xml.Name              `xml:"s:Body"`
	GetProtocolInfoAction getProtocolInfoAction `xml:"u:GetProtocolInfo"`
}

type getProtocolInfoAction struct {
	XMLName           xml.Name `xml:"u:GetProtocolInfo"`
	ConnectionManager string   `xml:"xmlns:u,attr"`
}

type getMediaInfoEnvelope struct {
	XMLName          xml.Name         `xml:"s:Envelope"`
	Schema           string           `xml:"xmlns:s,attr"`
	Encoding         string           `xml:"s:encodingStyle,attr"`
	GetMediaInfoBody getMediaInfoBody `xml:"s:Body"`
}

type getMediaInfoBody struct {
	XMLName            xml.Name           `xml:"s:Body"`
	GetMediaInfoAction getMediaInfoAction `xml:"u:GetMediaInfo"`
}

type getMediaInfoAction struct {
	XMLName     xml.Name `xml:"u:GetMediaInfo"`
	AVTransport string   `xml:"xmlns:u,attr"`
	InstanceID  string
}

func setAVTransportSoapBuild(tvdata *TVPayload) ([]byte, error) {
	mediaTypeSlice := strings.Split(tvdata.MediaType, "/")
	seekflag := "00"
	if tvdata.Seekable {
		seekflag = "01"
	}

	contentFeatures, err := utils.BuildContentFeatures(tvdata.MediaType, seekflag, tvdata.Transcode)
	if err != nil {
		return nil, fmt.Errorf("setAVTransportSoapBuild failed to build contentFeatures: %w", err)
	}

	var class string
	switch mediaTypeSlice[0] {
	case "audio":
		class = "object.item.audioItem.musicTrack"
	case "image":
		class = "object.item.imageItem.photo"
	default:
		class = "object.item.videoItem.movie"
	}

	mediaTitlefromURL, err := url.Parse(tvdata.MediaURL)
	if err != nil {
		return nil, fmt.Errorf("setAVTransportSoapBuild url parse error: %w", err)
	}

	mediaTitle := strings.TrimLeft(mediaTitlefromURL.Path, "/")

	re, err := regexp.Compile(`[&<>\\]+`)
	if err != nil {
		return nil, fmt.Errorf("setAVTransportSoapBuild regex compile error: %w", err)
	}
	mediaTitle = re.ReplaceAllString(mediaTitle, "")

	var didl didLLiteItem
	resNodeData := []resNode{}
	duration, _ := utils.DurationForMedia(tvdata.MediaPath)

	switch duration {
	case "":
		resNodeData = append(resNodeData, resNode{
			XMLName:      xml.Name{},
			ProtocolInfo: fmt.Sprintf("http-get:*:%s:%s", tvdata.MediaType, contentFeatures),
			Value:        tvdata.MediaURL,
		})
	default:
		resNodeData = append(resNodeData, resNode{
			XMLName:      xml.Name{},
			Duration:     duration,
			ProtocolInfo: fmt.Sprintf("http-get:*:%s:%s", tvdata.MediaType, contentFeatures),
			Value:        tvdata.MediaURL,
		})
	}

	didl = didLLiteItem{
		XMLName:    xml.Name{},
		ID:         "1",
		ParentID:   "0",
		Restricted: "1",
		UPNPClass:  class,
		DCtitle:    mediaTitle,
		ResNode:    resNodeData,
	}

	if strings.Contains(tvdata.SubtitlesURL, "srt") {
		resNodeData = append(resNodeData, resNode{
			XMLName:      xml.Name{},
			ProtocolInfo: "http-get:*:text/srt:*",
			Value:        tvdata.SubtitlesURL,
		})

		didl = didLLiteItem{
			XMLName:    xml.Name{},
			ID:         "1",
			ParentID:   "0",
			Restricted: "1",
			DCtitle:    mediaTitle,
			UPNPClass:  class,
			ResNode:    resNodeData,
			SecCaptionInfo: &secCaptionInfo{
				XMLName: xml.Name{},
				Type:    "srt",
				Value:   tvdata.SubtitlesURL,
			},
			SecCaptionInfoEx: &secCaptionInfoEx{
				XMLName: xml.Name{},
				Type:    "srt",
				Value:   tvdata.SubtitlesURL,
			},
		}
	}

	l := didLLite{
		XMLName:      xml.Name{},
		SchemaDIDL:   "urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/",
		DC:           "http://purl.org/dc/elements/1.1/",
		Sec:          "http://www.sec.co.kr/",
		SchemaUPNP:   "urn:schemas-upnp-org:metadata-1-0/upnp/",
		DIDLLiteItem: didl,
	}

	a, err := xml.Marshal(l)
	if err != nil {
		return nil, fmt.Errorf("setAVTransportSoapBuild #1 Marshal error: %w", err)
	}

	d := setAVTransportEnvelope{
		XMLName:  xml.Name{},
		Schema:   "http://schemas.xmlsoap.org/soap/envelope/",
		Encoding: "http://schemas.xmlsoap.org/soap/encoding/",
		Body: setAVTransportBody{
			XMLName: xml.Name{},
			SetAVTransportURI: setAVTransportURI{
				XMLName:     xml.Name{},
				AVTransport: "urn:schemas-upnp-org:service:AVTransport:1",
				InstanceID:  "0",
				CurrentURI:  tvdata.MediaURL,
				CurrentURIMetaData: currentURIMetaData{
					XMLName: xml.Name{},
					Value:   a,
				},
			},
		},
	}
	xmlStart := []byte(`<?xml version="1.0" encoding="utf-8"?>`)
	b, err := xml.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("setAVTransportSoapBuild #2 Marshal error: %w", err)
	}

	// Samsung TV hack.
	b = bytes.ReplaceAll(b, []byte("&#34;"), []byte(`"`))
	b = bytes.ReplaceAll(b, []byte("&amp;"), []byte("&"))

	return append(xmlStart, b...), nil
}

func setNextAVTransportSoapBuild(tvdata *TVPayload, clear bool) ([]byte, error) {
	mediaTypeSlice := strings.Split(tvdata.MediaType, "/")
	seekflag := "00"
	if tvdata.Seekable {
		seekflag = "01"
	}

	contentFeatures, err := utils.BuildContentFeatures(tvdata.MediaType, seekflag, tvdata.Transcode)
	if err != nil {
		return nil, fmt.Errorf("setNextAVTransportSoapBuild failed to build contentFeatures: %w", err)
	}

	var class string
	switch mediaTypeSlice[0] {
	case "audio":
		class = "object.item.audioItem.musicTrack"
	case "image":
		class = "object.item.imageItem.photo"
	default:
		class = "object.item.videoItem.movie"
	}

	murl := tvdata.MediaURL
	if clear {
		murl = ""
	}

	mediaTitlefromURL, err := url.Parse(murl)
	if err != nil {
		return nil, fmt.Errorf("setNextAVTransportSoapBuild url parse error: %w", err)
	}

	mediaTitle := strings.TrimLeft(mediaTitlefromURL.Path, "/")

	re, err := regexp.Compile(`[&<>\\]+`)
	if err != nil {
		return nil, fmt.Errorf("setNextAVTransportSoapBuild regex compile error: %w", err)
	}
	mediaTitle = re.ReplaceAllString(mediaTitle, "")

	var didl didLLiteItem
	resNodeData := []resNode{}
	duration, _ := utils.DurationForMedia(tvdata.MediaPath)

	switch duration {
	case "":
		resNodeData = append(resNodeData, resNode{
			XMLName:      xml.Name{},
			ProtocolInfo: fmt.Sprintf("http-get:*:%s:%s", tvdata.MediaType, contentFeatures),
			Value:        murl,
		})
	default:
		resNodeData = append(resNodeData, resNode{
			XMLName:      xml.Name{},
			Duration:     duration,
			ProtocolInfo: fmt.Sprintf("http-get:*:%s:%s", tvdata.MediaType, contentFeatures),
			Value:        murl,
		})
	}

	didl = didLLiteItem{
		XMLName:    xml.Name{},
		ID:         "1",
		ParentID:   "0",
		Restricted: "1",
		UPNPClass:  class,
		DCtitle:    mediaTitle,
		ResNode:    resNodeData,
	}

	if strings.Contains(tvdata.SubtitlesURL, "srt") {
		resNodeData = append(resNodeData, resNode{
			XMLName:      xml.Name{},
			ProtocolInfo: "http-get:*:text/srt:*",
			Value:        tvdata.SubtitlesURL,
		})

		didl = didLLiteItem{
			XMLName:    xml.Name{},
			ID:         "1",
			ParentID:   "0",
			Restricted: "1",
			DCtitle:    mediaTitle,
			UPNPClass:  class,
			ResNode:    resNodeData,
			SecCaptionInfo: &secCaptionInfo{
				XMLName: xml.Name{},
				Type:    "srt",
				Value:   tvdata.SubtitlesURL,
			},
			SecCaptionInfoEx: &secCaptionInfoEx{
				XMLName: xml.Name{},
				Type:    "srt",
				Value:   tvdata.SubtitlesURL,
			},
		}
	}

	l := didLLite{
		XMLName:      xml.Name{},
		SchemaDIDL:   "urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/",
		DC:           "http://purl.org/dc/elements/1.1/",
		Sec:          "http://www.sec.co.kr/",
		SchemaUPNP:   "urn:schemas-upnp-org:metadata-1-0/upnp/",
		DIDLLiteItem: didl,
	}

	a, err := xml.Marshal(l)
	if err != nil {
		return nil, fmt.Errorf("setNextAVTransportSoapBuild #1 Marshal error: %w", err)
	}

	d := setNextAVTransportEnvelope{
		XMLName:  xml.Name{},
		Schema:   "http://schemas.xmlsoap.org/soap/envelope/",
		Encoding: "http://schemas.xmlsoap.org/soap/encoding/",
		Body: setNextAVTransportBody{
			XMLName: xml.Name{},
			SetNextAVTransportURI: setNextAVTransportURI{
				XMLName:     xml.Name{},
				AVTransport: "urn:schemas-upnp-org:service:AVTransport:1",
				InstanceID:  "0",
				NextURI:     murl,
				NextURIMetaData: nextURIMetaData{
					XMLName: xml.Name{},
					Value:   a,
				},
			},
		},
	}
	xmlStart := []byte(`<?xml version="1.0" encoding="utf-8"?>`)
	b, err := xml.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("setNextAVTransportSoapBuild #2 Marshal error: %w", err)
	}

	// Samsung TV hack.
	b = bytes.ReplaceAll(b, []byte("&#34;"), []byte(`"`))
	b = bytes.ReplaceAll(b, []byte("&amp;"), []byte("&"))

	return append(xmlStart, b...), nil
}

func playSoapBuild() ([]byte, error) {
	d := playEnvelope{
		XMLName:  xml.Name{},
		Schema:   "http://schemas.xmlsoap.org/soap/envelope/",
		Encoding: "http://schemas.xmlsoap.org/soap/encoding/",
		PlayBody: playBody{
			XMLName: xml.Name{},
			PlayAction: playAction{
				XMLName:     xml.Name{},
				AVTransport: "urn:schemas-upnp-org:service:AVTransport:1",
				InstanceID:  "0",
				Speed:       "1",
			},
		},
	}
	xmlStart := []byte(`<?xml version="1.0" encoding="utf-8"?>`)
	b, err := xml.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("playSoapBuild Marshal error: %w", err)
	}

	return append(xmlStart, b...), nil
}

func stopSoapBuild() ([]byte, error) {
	d := stopEnvelope{
		XMLName:  xml.Name{},
		Schema:   "http://schemas.xmlsoap.org/soap/envelope/",
		Encoding: "http://schemas.xmlsoap.org/soap/encoding/",
		StopBody: stopBody{
			XMLName: xml.Name{},
			StopAction: stopAction{
				XMLName:     xml.Name{},
				AVTransport: "urn:schemas-upnp-org:service:AVTransport:1",
				InstanceID:  "0",
				Speed:       "1",
			},
		},
	}
	xmlStart := []byte(`<?xml version="1.0" encoding="utf-8"?>`)
	b, err := xml.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("stopSoapBuild Marshal error: %w", err)
	}

	return append(xmlStart, b...), nil
}

func pauseSoapBuild() ([]byte, error) {
	d := pauseEnvelope{
		XMLName:  xml.Name{},
		Schema:   "http://schemas.xmlsoap.org/soap/envelope/",
		Encoding: "http://schemas.xmlsoap.org/soap/encoding/",
		PauseBody: pauseBody{
			XMLName: xml.Name{},
			PauseAction: pauseAction{
				XMLName:     xml.Name{},
				AVTransport: "urn:schemas-upnp-org:service:AVTransport:1",
				InstanceID:  "0",
				Speed:       "1",
			},
		},
	}
	xmlStart := []byte(`<?xml version="1.0" encoding="utf-8"?>`)
	b, err := xml.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("pauseSoapBuild Marshal error: %w", err)
	}

	return append(xmlStart, b...), nil
}

func setMuteSoapBuild(m string) ([]byte, error) {
	if m != "0" && m != "1" {
		return nil, ErrSetMuteInput
	}

	d := setMuteEnvelope{
		XMLName:  xml.Name{},
		Schema:   "http://schemas.xmlsoap.org/soap/envelope/",
		Encoding: "http://schemas.xmlsoap.org/soap/encoding/",
		SetMuteBody: setMuteBody{
			XMLName: xml.Name{},
			SetMuteAction: setMuteAction{
				XMLName:          xml.Name{},
				RenderingControl: "urn:schemas-upnp-org:service:RenderingControl:1",
				InstanceID:       "0",
				Channel:          "Master",
				DesiredMute:      m,
			},
		},
	}
	xmlStart := []byte(`<?xml version="1.0" encoding="utf-8"?>`)
	b, err := xml.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("setMuteSoapBuild Marshal error: %w", err)
	}

	return append(xmlStart, b...), nil
}

func getMuteSoapBuild() ([]byte, error) {
	d := getMuteEnvelope{
		XMLName:  xml.Name{},
		Schema:   "http://schemas.xmlsoap.org/soap/envelope/",
		Encoding: "http://schemas.xmlsoap.org/soap/encoding/",
		GetMuteBody: getMuteBody{
			XMLName: xml.Name{},
			GetMuteAction: getMuteAction{
				XMLName:          xml.Name{},
				RenderingControl: "urn:schemas-upnp-org:service:RenderingControl:1",
				InstanceID:       "0",
				Channel:          "Master",
			},
		},
	}
	xmlStart := []byte(`<?xml version="1.0" encoding="utf-8"?>`)
	b, err := xml.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("getMuteSoapBuild Marshal error: %w", err)
	}

	return append(xmlStart, b...), nil
}

func getVolumeSoapBuild() ([]byte, error) {
	d := getVolumeEnvelope{
		XMLName:  xml.Name{},
		Schema:   "http://schemas.xmlsoap.org/soap/envelope/",
		Encoding: "http://schemas.xmlsoap.org/soap/encoding/",
		GetVolumeBody: getVolumeBody{
			XMLName: xml.Name{},
			GetVolumeAction: getVolumeAction{
				XMLName:          xml.Name{},
				RenderingControl: "urn:schemas-upnp-org:service:RenderingControl:1",
				InstanceID:       "0",
				Channel:          "Master",
			},
		},
	}
	xmlStart := []byte(`<?xml version="1.0" encoding="utf-8"?>`)
	b, err := xml.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("getVolumeSoapBuild Marshal error: %w", err)
	}

	return append(xmlStart, b...), nil
}

func setVolumeSoapBuild(v string) ([]byte, error) {
	d := setVolumeEnvelope{
		XMLName:  xml.Name{},
		Schema:   "http://schemas.xmlsoap.org/soap/envelope/",
		Encoding: "http://schemas.xmlsoap.org/soap/encoding/",
		SetVolumeBody: setVolumeBody{
			XMLName: xml.Name{},
			SetVolumeAction: setVolumeAction{
				XMLName:          xml.Name{},
				RenderingControl: "urn:schemas-upnp-org:service:RenderingControl:1",
				InstanceID:       "0",
				Channel:          "Master",
				DesiredVolume:    v,
			},
		},
	}
	xmlStart := []byte(`<?xml version="1.0" encoding="utf-8"?>`)
	b, err := xml.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("setVolumeSoapBuild Marshal error: %w", err)
	}

	return append(xmlStart, b...), nil
}

func getProtocolInfoSoapBuild() ([]byte, error) {
	d := getProtocolInfoEnvelope{
		XMLName:  xml.Name{},
		Schema:   "http://schemas.xmlsoap.org/soap/envelope/",
		Encoding: "http://schemas.xmlsoap.org/soap/encoding/",
		GetProtocolInfoBody: getProtocolInfoBody{
			XMLName: xml.Name{},
			GetProtocolInfoAction: getProtocolInfoAction{
				XMLName:           xml.Name{},
				ConnectionManager: "urn:schemas-upnp-org:service:ConnectionManager:1",
			},
		},
	}
	xmlStart := []byte(`<?xml version="1.0" encoding="utf-8"?>`)
	b, err := xml.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("getProtocolInfoSoapBuild Marshal error: %w", err)
	}

	return append(xmlStart, b...), nil
}

func getMediaInfoSoapBuild() ([]byte, error) {
	d := getMediaInfoEnvelope{
		XMLName:  xml.Name{},
		Schema:   "http://schemas.xmlsoap.org/soap/envelope/",
		Encoding: "http://schemas.xmlsoap.org/soap/encoding/",
		GetMediaInfoBody: getMediaInfoBody{
			XMLName: xml.Name{},
			GetMediaInfoAction: getMediaInfoAction{
				XMLName:     xml.Name{},
				AVTransport: "urn:schemas-upnp-org:service:AVTransport:1",
				InstanceID:  "0",
			},
		},
	}
	xmlStart := []byte(`<?xml version="1.0" encoding="utf-8"?>`)
	b, err := xml.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("getMediaInfoSoapBuild Marshal error: %w", err)
	}

	return append(xmlStart, b...), nil
}
