package soapcalls

import (
	"encoding/xml"
	"errors"
	"io/ioutil"
	"net/http"
	"net/url"
)

// Root - root node.
type Root struct {
	XMLName xml.Name `xml:"root"`
	Device  Device   `xml:"device"`
}

// Device - device node (we should only expect one?).
type Device struct {
	XMLName     xml.Name    `xml:"device"`
	ServiceList ServiceList `xml:"serviceList"`
}

// ServiceList - serviceList node
type ServiceList struct {
	XMLName  xml.Name  `xml:"serviceList"`
	Services []Service `xml:"service"`
}

// Service - service node.
type Service struct {
	XMLName     xml.Name `xml:"service"`
	Type        string   `xml:"serviceType"`
	ID          string   `xml:"serviceId"`
	ControlURL  string   `xml:"controlURL"`
	EventSubURL string   `xml:"eventSubURL"`
}

// EventPropertySet .
type EventPropertySet struct {
	XMLName       xml.Name      `xml:"propertyset"`
	EventInstance EventInstance `xml:"property>LastChange>Event>InstanceID"`
}

// EventInstance .
type EventInstance struct {
	XMLName                      xml.Name                     `xml:"InstanceID"`
	Value                        string                       `xml:"val,attr"`
	EventCurrentTransportActions EventCurrentTransportActions `xml:"CurrentTransportActions"`
	EventTransportState          EventTransportState          `xml:"TransportState"`
}

// EventCurrentTransportActions .
type EventCurrentTransportActions struct {
	Value string `xml:"val,attr"`
}

// EventTransportState .
type EventTransportState struct {
	Value string `xml:"val,attr"`
}

// DMRextractor - Get the AVTransport URL from the main DMR xml.
func DMRextractor(dmrurl string) (string, string, error) {
	var root Root

	parsedURL, err := url.Parse(dmrurl)
	if err != nil {
		return "", "", err
	}

	client := &http.Client{}
	req, err := http.NewRequest("GET", dmrurl, nil)
	if err != nil {
		return "", "", err
	}

	xmlresp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer xmlresp.Body.Close()

	xmlbody, err := ioutil.ReadAll(xmlresp.Body)
	if err != nil {
		return "", "", err
	}
	xml.Unmarshal(xmlbody, &root)
	for i := 0; i < len(root.Device.ServiceList.Services); i++ {
		if root.Device.ServiceList.Services[i].ID == "urn:upnp-org:serviceId:AVTransport" {
			avtransportControlURL := parsedURL.Scheme + "://" + parsedURL.Host + root.Device.ServiceList.Services[i].ControlURL
			avtransportEventSubURL := parsedURL.Scheme + "://" + parsedURL.Host + root.Device.ServiceList.Services[i].EventSubURL
			return avtransportControlURL, avtransportEventSubURL, nil
		}
	}
	return "", "", errors.New("Something broke somewhere - wrong DMR URL?")
}

// EventNotifyParser - Parse the Notify messages from the media renderer.
func EventNotifyParser(xmlbody string) (string, string, error) {
	var root EventPropertySet
	err := xml.Unmarshal([]byte(xmlbody), &root)
	if err != nil {
		return "", "", err
	}
	previousstate := root.EventInstance.EventCurrentTransportActions.Value
	newstate := root.EventInstance.EventTransportState.Value

	return previousstate, newstate, nil
}
