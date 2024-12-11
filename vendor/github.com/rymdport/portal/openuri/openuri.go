// Package openuri allows sandboxed applications to open URIs (e.g. a http: link to the applications homepage) under the control of the user.
// Upstream API documentation can be found at https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.OpenURI.html.
package openuri

import (
	"github.com/godbus/dbus/v5"
	"github.com/rymdport/portal/internal/apis"
	"github.com/rymdport/portal/internal/convert"
)

const (
	openURIBaseName = apis.CallBaseName + ".OpenURI"
	openURICallName = openURIBaseName + ".OpenURI"
)

// OpenURIOptions holds optional settings that can be passed to the OpenURI call.
type OpenURIOptions struct {
	HandleToken string // A string that will be used as the last element of the handle. Must be a valid object path element.
	Writeable   bool   // Whether to allow the chosen application to write to the file. This key only takes effect the uri points to a local file that is exported in the document portal, and the chosen application is sandboxed itself.
	Ask         bool   // Whether to ask the user to choose an app. If this is not passed, or false, the portal may use a default or pick the last choice.
}

// OpenURI opens the given URI in the corresponding application.
func OpenURI(parentWindow, uri string, options *OpenURIOptions) error {
	conn, err := dbus.SessionBus() // Shared connection, don't close.
	if err != nil {
		return err
	}

	data := map[string]dbus.Variant{}

	if options != nil {
		data = map[string]dbus.Variant{
			"writable": convert.FromBool(options.Writeable),
			"ask":      convert.FromBool(options.Ask),
		}

		if options.HandleToken != "" {
			data["handle_token"] = convert.FromString(options.HandleToken)
		}
	}

	obj := conn.Object(apis.ObjectName, apis.ObjectPath)
	call := obj.Call(openURICallName, 0, parentWindow, uri, data)
	return call.Err
}
