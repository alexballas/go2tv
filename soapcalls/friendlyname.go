package soapcalls

import (
	"encoding/xml"
	"fmt"
	"net/http"
)

// GetFriendlyName returns the friendly name value for a the specific DMR url.
func GetFriendlyName(dmr string) (string, error) {
	client := &http.Client{}
	req, err := http.NewRequest(http.MethodGet, dmr, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create NewRequest for GetFriendlyName: %w", err)
	}

	req.Header.Set("Connection", "close")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send HTTP request for GetFriendlyName: %w", err)
	}
	defer resp.Body.Close()

	var fn struct {
		FriendlyName string `xml:"device>friendlyName"`
	}

	if err = xml.NewDecoder(resp.Body).Decode(&fn); err != nil {
		return "", fmt.Errorf("failed to read response body for GetFriendlyName: %w", err)
	}

	return fn.FriendlyName, nil
}
