// Package filechooser allows sandboxed applications to ask the user for access to files outside the sandbox. The portal backend will present the user with a file chooser dialog.
// Upstream API documentation can be found at https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.FileChooser.html.
package filechooser

import (
	"github.com/godbus/dbus/v5"
	"github.com/rymdport/portal"
	"github.com/rymdport/portal/internal/apis"
)

const fileChooserCallName = apis.CallBaseName + ".FileChooser"

func readURIFromResponse(conn *dbus.Conn, call *dbus.Call) ([]string, error) {
	result, err := apis.ReadResponse(conn, call)
	if err != nil {
		return nil, err
	} else if result == nil {
		return nil, nil // Cancelled by the user.
	}

	uris, ok := result["uris"].Value().([]string)
	if !ok {
		return nil, portal.ErrUnexpectedResponse
	}

	return uris, nil
}
