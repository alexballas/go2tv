package gui

import (
	"errors"
	"fmt"

	"github.com/alexballas/refyne/v2/lang"
	"go2tv.app/go2tv/v2/devices"
)

func discoveryStatusText(deviceCount int, searched bool, err error) string {
	if err != nil && !errors.Is(err, devices.ErrNoDeviceAvailable) {
		return lang.L("Device discovery error")
	}
	if !searched {
		return lang.L("Searching for devices…")
	}

	switch deviceCount {
	case 0:
		return lang.L("No devices found")
	case 1:
		return lang.L("1 device found")
	default:
		return fmt.Sprintf(lang.L("%d devices found"), deviceCount)
	}
}
