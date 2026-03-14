package application

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"go2tv.app/go2tv/v2/castprotocol/v2/cast"
)

// getInfo uses the http://<ip>:8008/setup/eureka_endpoint to obtain more
// information about the cast-device.
// OBS: The 8008 seems to be pure http, whereas 8009 is typically the port
// to use for protobuf-communication,

func GetInfo(ip string) (info *cast.DeviceInfo, err error) {
	// Note: Services exposed not on 8009 port are "Google Cast Group"s
	// The only way to find the true device (group) name, is using mDNS outside of this function.
	url := fmt.Sprintf("http://%v:8008/setup/eureka_info", ip)
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	info = new(cast.DeviceInfo)
	if err := json.Unmarshal(data, info); err != nil {
		return nil, err
	}
	return info, nil
}
