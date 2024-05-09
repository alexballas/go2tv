package soapcalls

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/pkg/errors"
)

var (
	ErrWrongDMR = errors.New("something broke somewhere - wrong DMR URL?")
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
	ConnectionManagerURL   string
}

// DMRextractor extracts the services URLs from the main DMR xml.
func DMRextractor(ctx context.Context, dmrurl string) (*DMRextracted, error) {
	var root rootNode
	ex := &DMRextracted{}

	parsedURL, err := url.Parse(dmrurl)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return nil, fmt.Errorf("DMRextractor parse error: %w", err)
	}

	client := &http.Client{}
	req, err := http.NewRequestWithContext(ctx, "GET", dmrurl, nil)
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

			_, err := url.ParseRequestURI(ex.AvtransportControlURL)
			if err != nil {
				return nil, fmt.Errorf("DMRextractor invalid AvtransportControlURL: %w", err)
			}

			_, err = url.ParseRequestURI(ex.AvtransportEventSubURL)
			if err != nil {
				return nil, fmt.Errorf("DMRextractor invalid AvtransportEventSubURL: %w", err)
			}
		}

		if service.ID == "urn:upnp-org:serviceId:RenderingControl" {
			ex.RenderingControlURL = parsedURL.Scheme + "://" + parsedURL.Host + service.ControlURL

			_, err = url.ParseRequestURI(ex.RenderingControlURL)
			if err != nil {
				return nil, fmt.Errorf("DMRextractor invalid RenderingControlURL: %w", err)
			}
		}

		if service.ID == "urn:upnp-org:serviceId:ConnectionManager" {
			ex.ConnectionManagerURL = parsedURL.Scheme + "://" + parsedURL.Host + service.ControlURL

			_, err = url.ParseRequestURI(ex.ConnectionManagerURL)
			if err != nil {
				return nil, fmt.Errorf("DMRextractor invalid ConnectionManagerURL: %w", err)
			}
		}
	}

	if ex.AvtransportControlURL != "" {
		return ex, nil
	}

	return nil, ErrWrongDMR
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
