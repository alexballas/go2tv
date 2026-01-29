package devices

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"sort"
	"time"

	"github.com/alexballas/go-ssdp"
	"github.com/pkg/errors"
	"go2tv.app/go2tv/v2/soapcalls"
)

const (
	DeviceTypeDLNA       = "DLNA"
	DeviceTypeChromecast = "Chromecast"
)

type Device struct {
	Name string
	Addr string
	Type string
}

var (
	ErrNoDeviceAvailable  = errors.New("loadSSDPservices: No available Media Renderers")
	ErrDeviceNotAvailable = errors.New("devicePicker: Requested device not available")
	ErrSomethingWentWrong = errors.New("devicePicker: Something went terribly wrong")
)

// IsChromecastURL returns true if the URL points to a Chromecast device (port 8009).
func IsChromecastURL(deviceURL string) bool {
	u, err := url.Parse(deviceURL)
	if err != nil {
		return false
	}
	return u.Port() == "8009"
}

// LoadSSDPservices returns a slice of DLNA devices that support the
// AVTransport service.
func LoadSSDPservices(delay int) ([]Device, error) {
	// Reset device list every time we call this.
	urlList := make(map[string]string)

	port := 3339

	var (
		address *net.UDPAddr
		tries   int
	)

	for address == nil && tries < 100 {
		addr := net.UDPAddr{
			Port: port,
			IP:   net.ParseIP("0.0.0.0"),
		}

		if err := checkInterfacesForPort(port); err != nil {
			port++
			tries++
			continue
		}

		address = &addr
	}

	var addrString string
	if address != nil {
		addrString = address.String()
	}

	list, err := ssdp.Search(ssdp.All, delay, addrString)
	if err != nil {
		return nil, fmt.Errorf("LoadSSDPservices search error: %w", err)
	}

	for _, srv := range list {
		// We only care about the AVTransport services for basic actions
		// (stop,play,pause). If we need support other functionalities
		// like volume control we need to use the RenderingControl service.
		if srv.Type == "urn:schemas-upnp-org:service:AVTransport:1" {
			friendlyName, err := soapcalls.GetFriendlyName(context.Background(), srv.Location)
			if err != nil {
				continue
			}

			urlList[srv.Location] = friendlyName
		}
	}

	deviceList := make(map[string]string)
	dupNames := make(map[string]int)
	for loc, fn := range urlList {
		_, exists := dupNames[fn]
		dupNames[fn]++
		if exists {
			fn = fn + " (" + loc + ")"
		}

		deviceList[fn] = loc
	}

	for fn, c := range dupNames {
		if c > 1 {
			loc := deviceList[fn]
			delete(deviceList, fn)
			fn = fn + " (" + loc + ")"
			deviceList[fn] = loc
		}
	}

	if len(deviceList) == 0 {
		return nil, ErrNoDeviceAvailable
	}

	// Convert map to Device slice with proper type
	result := make([]Device, 0, len(deviceList))
	for name, addr := range deviceList {
		result = append(result, Device{
			Name: name,
			Addr: addr,
			Type: DeviceTypeDLNA,
		})
	}

	return result, nil
}

// LoadAllDevices returns a combined slice of DLNA and Chromecast devices.
// It runs both discovery mechanisms concurrently and returns partial results
// immediately without waiting for both to complete. This ensures delays in
// one protocol don't block the other.
func LoadAllDevices(delay int) ([]Device, error) {
	type deviceResult struct {
		devices []Device
		err     error
	}

	dlnaChan := make(chan deviceResult, 1)
	ccChan := make(chan deviceResult, 1)

	// Launch DLNA discovery in background
	go func() {
		devices, err := LoadSSDPservices(delay)
		dlnaChan <- deviceResult{devices: devices, err: err}
	}()

	// Launch Chromecast discovery in background (instant, reads from cache)
	go func() {
		devices := GetChromecastDevices()
		ccChan <- deviceResult{devices: devices, err: nil}
	}()

	// Collect results as they arrive, with timeout
	combined := make([]Device, 0)
	timeout := time.After(time.Duration(delay+1) * time.Second)
	resultsReceived := 0

	for resultsReceived < 2 {
		select {
		case result := <-dlnaChan:
			if result.err == nil {
				combined = append(combined, result.devices...)
			}
			resultsReceived++

		case result := <-ccChan:
			if result.devices != nil {
				combined = append(combined, result.devices...)
			}
			resultsReceived++

		case <-timeout:
			// Return partial results if timeout occurs
			if len(combined) > 0 {
				sortDevices(combined)
				return combined, nil
			}
			return nil, ErrNoDeviceAvailable
		}
	}

	if len(combined) > 0 {
		sortDevices(combined)
		return combined, nil
	}

	return nil, ErrNoDeviceAvailable
}

// sortDevices sorts devices by type (DLNA first, then Chromecast)
// and alphabetically within each type
func sortDevices(devices []Device) {
	sort.Slice(devices, func(i, j int) bool {
		// If types differ, DLNA comes first
		if devices[i].Type != devices[j].Type {
			return devices[i].Type < devices[j].Type
		}
		// Within same type, sort alphabetically by name
		return devices[i].Name < devices[j].Name
	})
}

// DevicePicker will pick the nth device from the devices input slice.
func DevicePicker(devices []Device, n int) (string, error) {
	if n > len(devices) || len(devices) == 0 || n <= 0 {
		return "", ErrDeviceNotAvailable
	}

	if n > len(devices) {
		return "", ErrDeviceNotAvailable
	}

	return devices[n-1].Addr, nil
}

func checkInterfacesForPort(port int) error {
	interfaces, err := net.Interfaces()
	if err != nil {
		return err
	}

	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			return err
		}

		for _, addr := range addrs {
			ip, _, _ := net.ParseCIDR(addr.String())

			if ip.IsLoopback() || ip.To4() == nil {
				continue
			}

			udpAddr := net.UDPAddr{
				Port: port,
				IP:   ip,
			}

			udpConn, err := net.ListenUDP("udp4", &udpAddr)
			if err != nil {
				return err
			}

			udpConn.Close()
			return nil

		}
	}

	return nil
}
