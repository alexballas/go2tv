package soapcalls

import (
	"encoding/xml"
	"errors"
	"io/ioutil"
	"net/http"
	"net/url"
)

// Root - root node
type Root struct {
	XMLName xml.Name `xml:"root"`
	Device  Device   `xml:"device"`
}

// Device - device node (we should only expect one?)
type Device struct {
	XMLName     xml.Name    `xml:"device"`
	ServiceList ServiceList `xml:"serviceList"`
}

// ServiceList - serviceList node
type ServiceList struct {
	XMLName  xml.Name  `xml:"serviceList"`
	Services []Service `xml:"service"`
}

// Service - service node
type Service struct {
	XMLName    xml.Name `xml:"service"`
	Type       string   `xml:"serviceType"`
	ID         string   `xml:"serviceId"`
	ControlURL string   `xml:"controlURL"`
}

// AVTransportFromDMRextractor - Get the AVTransport URL from the main DMR xml
func AVTransportFromDMRextractor(dmrurl string) (string, error) {
	var root Root

	parsedURL, err := url.Parse(dmrurl)
	if err != nil {
		return "", err
	}

	xmlresp, err := http.Get(dmrurl)
	if err != nil {
		return "", err
	}
	xmlbody, err := ioutil.ReadAll(xmlresp.Body)
	if err != nil {
		return "", err
	}
	xml.Unmarshal(xmlbody, &root)
	for i := 0; i < len(root.Device.ServiceList.Services); i++ {
		if root.Device.ServiceList.Services[i].ID == "urn:upnp-org:serviceId:AVTransport" {
			avtransportURL := parsedURL.Scheme + "://" + parsedURL.Host + root.Device.ServiceList.Services[i].ControlURL
			return avtransportURL, nil
		}
	}
	return "", errors.New("Something broke somewhere - wrong DMR URL?")
}
