package soapcalls

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/pkg/errors"
)

type rootNode struct {
	XMLName xml.Name `xml:"root"`
	Device  struct {
		XMLName     xml.Name `xml:"device"`
		ServiceList struct {
			XMLName  xml.Name `xml:"serviceList"`
			Services []struct {
				XMLName     xml.Name `xml:"service"`
				Type        string   `xml:"serviceType"`
				ID          string   `xml:"serviceId"`
				ControlURL  string   `xml:"controlURL"`
				EventSubURL string   `xml:"eventSubURL"`
			} `xml:"service"`
		} `xml:"serviceList"`
	} `xml:"device"`
}

type eventPropertySet struct {
	XMLName       xml.Name `xml:"propertyset"`
	EventInstance struct {
		XMLName                      xml.Name `xml:"InstanceID"`
		Value                        string   `xml:"val,attr"`
		EventCurrentTransportActions struct {
			Value string `xml:"val,attr"`
		} `xml:"CurrentTransportActions"`
		EventTransportState struct {
			Value string `xml:"val,attr"`
		} `xml:"TransportState"`
	} `xml:"property>LastChange>Event>InstanceID"`
}

// DMRextracted stored the services urls
type DMRextracted struct {
	AvtransportControlURL  string
	AvtransportEventSubURL string
	RenderingControlURL    string
}

// DMRextractor extracts the services URLs from the main DMR xml.
func DMRextractor(dmrurl string) (*DMRextracted, error) {
	var root rootNode
	ex := &DMRextracted{}

	parsedURL, err := url.Parse(dmrurl)
	if err != nil {
		return nil, fmt.Errorf("DMRextractor parse error: %w", err)
	}

	client := &http.Client{}
	req, err := http.NewRequest("GET", dmrurl, nil)
	if err != nil {
		return nil, fmt.Errorf("DMRextractor GET error: %w", err)
	}

	req.Header.Set("Connection", "close")

	xmlresp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("DMRextractor Do GET error: %w", err)
	}
	defer xmlresp.Body.Close()

	xmlbody, err := io.ReadAll(xmlresp.Body)
	if err != nil {
		return nil, fmt.Errorf("DMRextractor read error: %w", err)
	}

	err = xml.Unmarshal(xmlbody, &root)
	if err != nil {
		return nil, fmt.Errorf("DMRextractor unmarshal error: %w", err)
	}

	for i := 0; i < len(root.Device.ServiceList.Services); i++ {
		service := root.Device.ServiceList.Services[i]
		if !strings.HasPrefix(service.EventSubURL, "/") {
			service.EventSubURL = "/" + service.EventSubURL
		}
		if !strings.HasPrefix(service.ControlURL, "/") {
			service.ControlURL = "/" + service.ControlURL
		}

		if service.ID == "urn:upnp-org:serviceId:AVTransport" {
			ex.AvtransportControlURL = parsedURL.Scheme + "://" + parsedURL.Host + service.ControlURL
			ex.AvtransportEventSubURL = parsedURL.Scheme + "://" + parsedURL.Host + service.EventSubURL
		}
		if service.ID == "urn:upnp-org:serviceId:RenderingControl" {
			ex.RenderingControlURL = parsedURL.Scheme + "://" + parsedURL.Host + service.ControlURL
		}
	}

	if ex.AvtransportControlURL != "" {
		return ex, nil
	}

	return nil, errors.New("something broke somewhere - wrong DMR URL?")
}

// EventNotifyParser parses the Notify messages from the DMR device.
func EventNotifyParser(xmlbody string) (string, string, error) {
	var root eventPropertySet
	err := xml.Unmarshal([]byte(xmlbody), &root)
	if err != nil {
		return "", "", fmt.Errorf("EventNotifyParser unmarshal error: %w", err)
	}
	previousstate := root.EventInstance.EventCurrentTransportActions.Value
	newstate := root.EventInstance.EventTransportState.Value

	return previousstate, newstate, nil
}
