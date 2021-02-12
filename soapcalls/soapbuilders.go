package soapcalls

import (
	"bytes"
	"encoding/xml"
	"fmt"
)

type Envelope struct {
	XMLName  xml.Name `xml:"s:Envelope"`
	Schema   string   `xml:"xmlns:s,attr"`
	Encoding string   `xml:"s:encodingStyle,attr"`
	Body     Body     `xml:"s:Body"`
}

type Body struct {
	XMLName           xml.Name          `xml:"s:Body"`
	SetAVTransportURI SetAVTransportURI `xml:"u:SetAVTransportURI"`
}

type SetAVTransportURI struct {
	XMLName            xml.Name `xml:"u:SetAVTransportURI"`
	AVTransport        string   `xml:"xmlns:u,attr"`
	InstanceID         string
	CurrentURI         string
	CurrentURIMetaData CurrentURIMetaData `xml:"CurrentURIMetaData"`
}

type CurrentURIMetaData struct {
	XMLName xml.Name `xml:"CurrentURIMetaData"`
	Value   []byte   `xml:",chardata"`
}

type DIDLLite struct {
	XMLName      xml.Name     `xml:"DIDL-Lite"`
	SchemaDIDL   string       `xml:"xmlns,attr"`
	DC           string       `xml:"xmlns:dc,attr"`
	Sec          string       `xml:"xmlns:sec,attr"`
	SchemaUPNP   string       `xml:"xmlns:upnp,attr"`
	DIDLLiteItem DIDLLiteItem `xml:"item"`
}

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

type ResNode struct {
	XMLName      xml.Name `xml:"res"`
	ProtocolInfo string   `xml:"protocolInfo,attr"`
	Value        string   `xml:",chardata"`
}

type SecCaptionInfo struct {
	XMLName xml.Name `xml:"sec:CaptionInfo"`
	Type    string   `xml:"sec:type,attr"`
	Value   string   `xml:",chardata"`
}

type SecCaptionInfoEx struct {
	XMLName xml.Name `xml:"sec:CaptionInfoEx"`
	Type    string   `xml:"sec:type,attr"`
	Value   string   `xml:",chardata"`
}

func setAVTransportSoapBuild(videoURL, subtitleURL string) ([]byte, error) {
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
			DCtitle:    videoURL,
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

	d := Envelope{
		XMLName:  xml.Name{},
		Schema:   "http://schemas.xmlsoap.org/soap/envelope/",
		Encoding: "http://schemas.xmlsoap.org/soap/encoding/",
		Body: Body{
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
	// That looks like an issue just with my SamsungTV
	b = bytes.ReplaceAll(b, []byte("&#34;"), []byte(`"`))
	b = bytes.ReplaceAll(b, []byte("&amp;"), []byte("&"))

	return append(xmlStart, b...), nil
}
