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

type serviceNode struct {
	Type        string `xml:"serviceType"`
	ID          string `xml:"serviceId"`
	ControlURL  string `xml:"controlURL"`
	EventSubURL string `xml:"eventSubURL"`
}

type serviceListNode struct {
	Services []serviceNode `xml:"service"`
}

type deviceNode struct {
	DeviceType   string          `xml:"deviceType"`
	FriendlyName string          `xml:"friendlyName"`
	UDN          string          `xml:"UDN"`
	ServiceList  serviceListNode `xml:"serviceList"`
	DeviceList   []deviceNode    `xml:"deviceList>device"`
}

type rootNode struct {
	XMLName xml.Name   `xml:"root"`
	URLBase string     `xml:"URLBase"`
	Device  deviceNode `xml:"device"`
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

type EventNotify struct {
	TransportState          string
	CurrentTransportActions string
}

// DMRextracted stores the services urls and device identification
type DMRextracted struct {
	AvtransportControlURL  string
	AvtransportEventSubURL string
	RenderingControlURL    string
	ConnectionManagerURL   string
	FriendlyName           string
	UDN                    string
}

// DMRextractor extracts the services URLs from the main DMR xml.
func DMRextractor(ctx context.Context, dmrurl string) (*DMRextracted, error) {
	parsedURL, err := url.Parse(dmrurl)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return nil, fmt.Errorf("DMRextractor parse error: %w", err)
	}

	client := newHTTPClient()
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
	return ParseDMRFromXML(xmlbody, parsedURL)
}

// LoadDevicesFromLocation fetches XML from a UPnP location URL and returns
// all devices that have AVTransport service (for multi-device setups).
func LoadDevicesFromLocation(ctx context.Context, dmrurl string) ([]*DMRextracted, error) {
	parsedURL, err := url.Parse(dmrurl)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return nil, fmt.Errorf("LoadDevicesFromLocation parse error: %w", err)
	}

	client := newHTTPClient()
	req, err := http.NewRequestWithContext(ctx, "GET", dmrurl, nil)
	if err != nil {
		return nil, fmt.Errorf("LoadDevicesFromLocation GET error: %w", err)
	}

	req.Header.Set("Connection", "close")

	xmlresp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("LoadDevicesFromLocation Do GET error: %w", err)
	}
	defer xmlresp.Body.Close()

	xmlbody, err := io.ReadAll(xmlresp.Body)
	if err != nil {
		return nil, fmt.Errorf("LoadDevicesFromLocation read error: %w", err)
	}

	return ParseAllDMRFromXML(xmlbody, parsedURL)
}

// ParseDMRFromXML parses DMR XML data and extracts service URLs.
// It searches the root device and all embedded devices recursively
// to find the device with AVTransport service.
func ParseDMRFromXML(xmlbody []byte, baseURL *url.URL) (*DMRextracted, error) {
	var root rootNode
	err := xml.Unmarshal(xmlbody, &root)
	if err != nil {
		return nil, fmt.Errorf("ParseDMRFromXML unmarshal error: %w", err)
	}

	ex := extractServicesFromDevice(&root.Device, resolveDescriptionBaseURL(baseURL, root.URLBase))
	if ex != nil && ex.AvtransportControlURL != "" {
		return ex, nil
	}

	return nil, ErrWrongDMR
}

// ParseAllDMRFromXML parses DMR XML data and extracts service URLs from ALL
// devices that have AVTransport service. This handles multi-device setups
// where a single UPnP root may expose multiple MediaRenderers.
func ParseAllDMRFromXML(xmlbody []byte, baseURL *url.URL) ([]*DMRextracted, error) {
	var root rootNode
	err := xml.Unmarshal(xmlbody, &root)
	if err != nil {
		return nil, fmt.Errorf("ParseAllDMRFromXML unmarshal error: %w", err)
	}

	var results []*DMRextracted
	extractAllServicesFromDevice(&root.Device, resolveDescriptionBaseURL(baseURL, root.URLBase), &results)

	if len(results) == 0 {
		return nil, ErrWrongDMR
	}

	return results, nil
}

// extractServicesFromDevice recursively searches a device and its embedded
// devices for AVTransport, RenderingControl, and ConnectionManager services.
// It returns the first device's services that has AVTransport.
func extractServicesFromDevice(device *deviceNode, baseURL *url.URL) *DMRextracted {
	ex := buildDMRExtracted(device, baseURL)
	if ex != nil {
		return ex
	}

	// Recursively check embedded devices
	for i := range device.DeviceList {
		if result := extractServicesFromDevice(&device.DeviceList[i], baseURL); result != nil {
			return result
		}
	}

	return nil
}

// extractAllServicesFromDevice recursively searches a device and its embedded
// devices for ALL devices with AVTransport service, appending them to results.
func extractAllServicesFromDevice(device *deviceNode, baseURL *url.URL, results *[]*DMRextracted) {
	ex := buildDMRExtracted(device, baseURL)
	if ex != nil {
		*results = append(*results, ex)
	}

	// Recursively check ALL embedded devices
	for i := range device.DeviceList {
		extractAllServicesFromDevice(&device.DeviceList[i], baseURL, results)
	}
}

// buildDMRExtracted extracts service URLs from a single device if it has AVTransport.
// Returns nil if the device doesn't have AVTransport service.
func buildDMRExtracted(device *deviceNode, baseURL *url.URL) *DMRextracted {
	ex := &DMRextracted{
		FriendlyName: device.FriendlyName,
		UDN:          device.UDN,
	}
	hasAVTransport := false

	// Check this device's services
	for _, service := range device.ServiceList.Services {
		eventSubURL := toAbsoluteServiceURL(baseURL, service.EventSubURL)
		controlURL := toAbsoluteServiceURL(baseURL, service.ControlURL)
		switch service.ID {
		case "urn:upnp-org:serviceId:AVTransport":
			ex.AvtransportControlURL = controlURL
			ex.AvtransportEventSubURL = eventSubURL
			hasAVTransport = true

		case "urn:upnp-org:serviceId:RenderingControl":
			ex.RenderingControlURL = controlURL

		case "urn:upnp-org:serviceId:ConnectionManager":
			ex.ConnectionManagerURL = controlURL
		}
	}

	// If this device has AVTransport, validate and return its services
	if hasAVTransport {
		// Validate URLs
		if _, err := url.ParseRequestURI(ex.AvtransportControlURL); err != nil {
			return nil
		}
		if _, err := url.ParseRequestURI(ex.AvtransportEventSubURL); err != nil {
			return nil
		}
		if ex.RenderingControlURL != "" {
			if _, err := url.ParseRequestURI(ex.RenderingControlURL); err != nil {
				return nil
			}
		}
		if ex.ConnectionManagerURL != "" {
			if _, err := url.ParseRequestURI(ex.ConnectionManagerURL); err != nil {
				return nil
			}
		}
		return ex
	}

	return nil
}

func toAbsoluteServiceURL(baseURL *url.URL, rawServiceURL string) string {
	serviceURL := strings.TrimSpace(rawServiceURL)
	if serviceURL == "" {
		return ""
	}

	// If the serviceURL is not absolute and contains a colon in the first segment,
	// url.Parse will fail with "first path segment in URL cannot contain colon".
	// We can workaround this by prepending "./" if it doesn't have a scheme.
	if !strings.Contains(serviceURL, "://") && strings.Contains(strings.Split(serviceURL, "/")[0], ":") {
		serviceURL = "./" + serviceURL
	}

	parsedServiceURL, err := url.Parse(serviceURL)
	if err != nil {
		return ""
	}

	if parsedServiceURL.IsAbs() {
		return parsedServiceURL.String()
	}

	if baseURL == nil {
		return ""
	}

	return baseURL.ResolveReference(parsedServiceURL).String()
}

func resolveDescriptionBaseURL(locationBase *url.URL, rawURLBase string) *url.URL {
	urlBase := strings.TrimSpace(rawURLBase)
	if urlBase == "" {
		return locationBase
	}

	parsedURLBase, err := url.Parse(urlBase)
	if err != nil {
		return locationBase
	}

	if parsedURLBase.IsAbs() {
		if (parsedURLBase.Scheme == "http" || parsedURLBase.Scheme == "https") && parsedURLBase.Host != "" {
			return parsedURLBase
		}
		return locationBase
	}

	if locationBase == nil {
		return nil
	}

	resolvedURLBase := locationBase.ResolveReference(parsedURLBase)
	if resolvedURLBase == nil || resolvedURLBase.Scheme == "" || resolvedURLBase.Host == "" {
		return locationBase
	}

	return resolvedURLBase
}

// ParseEventNotify parses the Notify messages from the DMR device.
// Transport state drives playback transitions; actions are optional.
func ParseEventNotify(xmlbody string) (EventNotify, error) {
	var root eventPropertySet
	err := xml.Unmarshal([]byte(xmlbody), &root)
	if err != nil {
		return EventNotify{}, fmt.Errorf("ParseEventNotify unmarshal error: %w", err)
	}

	return EventNotify{
		TransportState:          root.EventInstance.EventTransportState.Value,
		CurrentTransportActions: root.EventInstance.EventCurrentTransportActions.Value,
	}, nil
}
