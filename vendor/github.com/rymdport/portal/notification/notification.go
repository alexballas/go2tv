// Package notification lets sandboxed applications send and withdraw notifications. It is not possible for the application to learn if the notification was actually presented to the user.
// Upstream API documentation can be found at https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.Notification.html.
package notification

import (
	"strconv"

	"github.com/godbus/dbus/v5"
	"github.com/rymdport/portal/internal/apis"
	"github.com/rymdport/portal/internal/convert"
)

const (
	notificationBaseName       = apis.CallBaseName + ".Notification"
	addNotificationCallName    = notificationBaseName + ".AddNotification"
	removeNotificationCallName = notificationBaseName + ".RemoveNotification"
)

// Priority is the priroity of a notification.
type Priority = string

const (
	Low    Priority = "low"
	Normal Priority = "normal"
	High   Priority = "high"
	Urgent Priority = "urgent"
)

// Content holds the content to send with the notification.
type Content struct {
	Title    string   // User-visible string to display as the title.
	Body     string   // User-visible string to display as the body.
	Icon     string   // Serialized icon or name of application icon.
	Priority Priority // The priority for the notification.
}

// Add sends a notification using org.freedesktop.portal.Notification.Add.
func Add(id uint, content Content) error {
	bus, err := dbus.SessionBus() // shared connection, don't close.
	if err != nil {
		return err
	}

	data := map[string]dbus.Variant{
		"title": convert.FromString(content.Title),
		"body":  convert.FromString(content.Body),
	}

	// Only add the icon field when it is set
	if content.Icon != "" {
		data["icon"] = convert.FromString(content.Icon)
	}

	// Only add the priority field when it is set.
	if content.Priority != "" {
		data["priority"] = convert.FromString(content.Priority)
	}

	obj := bus.Object(apis.ObjectName, apis.ObjectPath)
	call := obj.Call(addNotificationCallName, 0, strconv.FormatUint(uint64(id), 10), data)
	return call.Err
}

// Remove removes the notification with the corresponding id.
func Remove(id uint) error {
	bus, err := dbus.SessionBus() // shared connection, don't close.
	if err != nil {
		return err
	}

	obj := bus.Object(apis.ObjectName, apis.ObjectPath)
	call := obj.Call(removeNotificationCallName, 0, strconv.FormatUint(uint64(id), 10))
	return call.Err
}
