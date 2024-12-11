package settings

import (
	"github.com/godbus/dbus/v5"
	"github.com/rymdport/portal/internal/apis"
)

const (
	readCallPath    = settingsCallPath + ".Read"
	readOneCallPath = settingsCallPath + ".ReadOne"
	readAllCallPath = settingsCallPath + ".ReadAll"
)

// ReadAll returns all values for the corresponding namespaces passed.
// If namespaces is an empty array or contains an empty string it matches all.
// Globbing is supported but only for trailing sections, e.g. “org.example.*”.
func ReadAll(namespaces []string) (map[string](map[string]dbus.Variant), error) {
	conn, err := dbus.SessionBus() // Shared connection, don't close.
	if err != nil {
		return nil, err
	}

	obj := conn.Object(apis.ObjectName, apis.ObjectPath)
	call := obj.Call(readAllCallPath, 0, namespaces)
	if call.Err != nil {
		return nil, call.Err
	}

	var value map[string](map[string]dbus.Variant)
	err = call.Store(&value)
	return value, err
}

// ReadOne reads a single value which may be any valid DBus type. Returns an error on any unknown namespace or key.
func ReadOne(namespace, key string) (any, error) {
	value, err := read(readOneCallPath, namespace, key)
	if err != nil {
		return read(readCallPath, namespace, key) // Use deprecated fallback if new interface does not exist.
	}

	return value, err
}

func read(callPath, namespace, key string) (any, error) {
	conn, err := dbus.SessionBus() // Shared connection, don't close.
	if err != nil {
		return nil, err
	}

	obj := conn.Object(apis.ObjectName, apis.ObjectPath)
	call := obj.Call(callPath, 0, namespace, key)
	if call.Err != nil {
		return nil, call.Err
	}

	var value any
	err = call.Store(&value)
	return value, err
}
