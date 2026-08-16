package soapcalls

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"math"
	"net/url"
	"strings"

	"github.com/pkg/errors"

	"go2tv.app/go2tv/v2/metadata"
	"go2tv.app/go2tv/v2/utils"
)

var ErrSetMuteInput = errors.New("setMuteSoapBuild input error. Was expecting 0 or 1.")

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
}

type seekEnvelope struct {
	XMLName  xml.Name `xml:"s:Envelope"`
	Schema   string   `xml:"xmlns:s,attr"`
	Encoding string   `xml:"s:encodingStyle,attr"`
	SeekBody seekBody `xml:"s:Body"`
}

type seekBody struct {
	XMLName    xml.Name   `xml:"s:Body"`
	SeekAction seekAction `xml:"u:Seek"`
}

type seekAction struct {
	XMLName     xml.Name `xml:"u:Seek"`
	AVTransport string   `xml:"xmlns:u,attr"`
	InstanceID  string
	Unit        string
	Target      string
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
	Artist           string            `xml:"upnp:artist,omitempty"`
	Album            string            `xml:"upnp:album,omitempty"`
	UPNPClass        string            `xml:"upnp:class"`
	AlbumArtURI      string            `xml:"upnp:albumArtURI,omitempty"`
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

type getTransportInfoEnvelope struct {
	XMLName              xml.Name             `xml:"s:Envelope"`
	Schema               string               `xml:"xmlns:s,attr"`
	Encoding             string               `xml:"s:encodingStyle,attr"`
	GetTransportInfoBody getTransportInfoBody `xml:"s:Body"`
}

type getTransportInfoBody struct {
	XMLName                xml.Name               `xml:"s:Body"`
	GetTransportInfoAction getTransportInfoAction `xml:"u:GetTransportInfo"`
}

type getTransportInfoAction struct {
	XMLName     xml.Name `xml:"u:GetTransportInfo"`
	AVTransport string   `xml:"xmlns:u,attr"`
	InstanceID  string
}

type getPositionInfoEnvelope struct {
	XMLName             xml.Name            `xml:"s:Envelope"`
	Schema              string              `xml:"xmlns:s,attr"`
	Encoding            string              `xml:"s:encodingStyle,attr"`
	GetPositionInfoBody getPositionInfoBody `xml:"s:Body"`
}

type getPositionInfoBody struct {
	XMLName               xml.Name              `xml:"s:Body"`
	GetPositionInfoAction getPositionInfoAction `xml:"u:GetPositionInfo"`
}

type getPositionInfoAction struct {
	XMLName     xml.Name `xml:"u:GetPositionInfo"`
	AVTransport string   `xml:"xmlns:u,attr"`
	InstanceID  string
}

func setAVTransportSoapBuild(tvdata *TVPayload) ([]byte, error) {
	return setAVTransportSoapBuildWithCompat(tvdata, false)
}

func setAVTransportSoapBuildWithCompat(tvdata *TVPayload, legacyMetadataCompat bool) ([]byte, error) {
	a, err := buildDIDLLite(tvdata, tvdata.MediaURL, tvdata.Metadata)
	if err != nil {
		return nil, fmt.Errorf("setAVTransportSoapBuild DIDL error: %w", err)
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

	if legacyMetadataCompat {
		// Some Samsung renderers reject fully escaped nested DIDL metadata.
		b = applyLegacyDIDLCompatToCurrentURI(b)
	}

	return append(xmlStart, b...), nil
}

func applyLegacyDIDLCompatToCurrentURI(input []byte) []byte {
	startTag := []byte("<CurrentURIMetaData>")
	endTag := []byte("</CurrentURIMetaData>")

	startIdx := bytes.Index(input, startTag)
	endIdx := bytes.Index(input, endTag)
	if startIdx == -1 || endIdx == -1 {
		return input
	}

	startIdx += len(startTag)
	if startIdx >= endIdx {
		return input
	}

	metadata := append([]byte(nil), input[startIdx:endIdx]...)
	metadata = bytes.ReplaceAll(metadata, []byte("&#34;"), []byte(`"`))
	metadata = bytes.ReplaceAll(metadata, []byte("&amp;"), []byte("&"))

	out := make([]byte, 0, len(input)-(endIdx-startIdx)+len(metadata))
	out = append(out, input[:startIdx]...)
	out = append(out, metadata...)
	out = append(out, input[endIdx:]...)

	return out
}

func setNextAVTransportSoapBuild(tvdata *TVPayload, clear bool) ([]byte, error) {
	murl := tvdata.MediaURL
	mediaMetadata := tvdata.Metadata
	if clear {
		murl = ""
		mediaMetadata.Artwork = nil
	}

	a, err := buildDIDLLite(tvdata, murl, mediaMetadata)
	if err != nil {
		return nil, fmt.Errorf("setNextAVTransportSoapBuild DIDL error: %w", err)
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

	return append(xmlStart, b...), nil
}

func buildDIDLLite(tvdata *TVPayload, mediaURL string, mediaMetadata metadata.Media) ([]byte, error) {
	wireMediaType := utils.DLNAResourceMediaType(tvdata.MediaType, tvdata.Transcode)
	contentFeatures := utils.BuildDLNAContentFeatures(utils.DLNAContentFeaturesOptions{
		ByteSeek:  tvdata.Seekable && !tvdata.Transcode,
		Converted: tvdata.Transcode,
	})

	class := "object.item.videoItem.movie"
	switch strings.Split(wireMediaType, "/")[0] {
	case "audio":
		class = "object.item.audioItem.musicTrack"
	case "image":
		class = "object.item.imageItem.photo"
	}

	parsedMediaURL, err := url.Parse(mediaURL)
	if err != nil {
		return nil, fmt.Errorf("parse media URL: %w", err)
	}

	mediaTitle := mediaMetadata.Title
	if mediaTitle == "" {
		mediaTitle = strings.TrimLeft(parsedMediaURL.Path, "/")
	}

	resNodeData := []resNode{{
		ProtocolInfo: fmt.Sprintf("http-get:*:%s:%s", wireMediaType, contentFeatures),
		Value:        mediaURL,
	}}
	if tvdata.MediaDuration > 0 {
		resNodeData[0].Duration = utils.SecondsToClockTime(int(math.Round(tvdata.MediaDuration)))
	} else if duration, _ := utils.DurationForMedia(tvdata.FFmpegPath, tvdata.MediaPath); duration != "" {
		resNodeData[0].Duration = duration
	}

	didl := didLLiteItem{
		ID:         "1",
		ParentID:   "0",
		Restricted: "1",
		DCtitle:    mediaTitle,
		Artist:     mediaMetadata.Artist,
		Album:      mediaMetadata.Album,
		UPNPClass:  class,
		ResNode:    resNodeData,
	}
	if mediaMetadata.Artwork != nil {
		didl.AlbumArtURI = mediaMetadata.Artwork.URL
	}

	if strings.Contains(tvdata.SubtitlesURL, "srt") {
		didl.ResNode = append(didl.ResNode, resNode{
			ProtocolInfo: "http-get:*:text/srt:*",
			Value:        tvdata.SubtitlesURL,
		})
		didl.SecCaptionInfo = &secCaptionInfo{
			Type:  "srt",
			Value: tvdata.SubtitlesURL,
		}
		didl.SecCaptionInfoEx = &secCaptionInfoEx{
			Type:  "srt",
			Value: tvdata.SubtitlesURL,
		}
	}

	return xml.Marshal(didLLite{
		SchemaDIDL:   "urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/",
		DC:           "http://purl.org/dc/elements/1.1/",
		Sec:          "http://www.sec.co.kr/",
		SchemaUPNP:   "urn:schemas-upnp-org:metadata-1-0/upnp/",
		DIDLLiteItem: didl,
	})
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

func getTransportInfoSoapBuild() ([]byte, error) {
	d := getTransportInfoEnvelope{
		XMLName:  xml.Name{},
		Schema:   "http://schemas.xmlsoap.org/soap/envelope/",
		Encoding: "http://schemas.xmlsoap.org/soap/encoding/",
		GetTransportInfoBody: getTransportInfoBody{
			XMLName: xml.Name{},
			GetTransportInfoAction: getTransportInfoAction{
				XMLName:     xml.Name{},
				AVTransport: "urn:schemas-upnp-org:service:AVTransport:1",
				InstanceID:  "0",
			},
		},
	}
	xmlStart := []byte(`<?xml version="1.0" encoding="utf-8"?>`)
	b, err := xml.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("getTransportInfoSoapBuild Marshal error: %w", err)
	}

	return append(xmlStart, b...), nil
}

func getPositionInfoSoapBuild() ([]byte, error) {
	d := getPositionInfoEnvelope{
		XMLName:  xml.Name{},
		Schema:   "http://schemas.xmlsoap.org/soap/envelope/",
		Encoding: "http://schemas.xmlsoap.org/soap/encoding/",
		GetPositionInfoBody: getPositionInfoBody{
			XMLName: xml.Name{},
			GetPositionInfoAction: getPositionInfoAction{
				XMLName:     xml.Name{},
				AVTransport: "urn:schemas-upnp-org:service:AVTransport:1",
				InstanceID:  "0",
			},
		},
	}
	xmlStart := []byte(`<?xml version="1.0" encoding="utf-8"?>`)
	b, err := xml.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("getPositionInfoSoapBuild Marshal error: %w", err)
	}

	return append(xmlStart, b...), nil
}

func seekSoapBuild(reltime string) ([]byte, error) {
	d := seekEnvelope{
		XMLName:  xml.Name{},
		Schema:   "http://schemas.xmlsoap.org/soap/envelope/",
		Encoding: "http://schemas.xmlsoap.org/soap/encoding/",
		SeekBody: seekBody{
			XMLName: xml.Name{},
			SeekAction: seekAction{
				XMLName:     xml.Name{},
				AVTransport: "urn:schemas-upnp-org:service:AVTransport:1",
				InstanceID:  "0",
				Unit:        "REL_TIME",
				Target:      reltime,
			},
		},
	}
	xmlStart := []byte(`<?xml version="1.0" encoding="utf-8"?>`)
	b, err := xml.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("seekSoapBuild Marshal error: %w", err)
	}

	return append(xmlStart, b...), nil
}
