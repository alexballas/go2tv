package settings

import (
	"github.com/godbus/dbus/v5"
	"github.com/rymdport/portal/internal/apis"
)

// Changed is the result given when a setting changes its value.
type Changed struct {
	Namespace string // Namespace of changed setting.
	Key       string // The key of changed setting.
	Value     any    // The new value.
}

// OnSignalSettingChanged listens for the SettingChanged signal.
// This signal is emitted when a setting changes.
func OnSignalSettingChanged(callback func(changed Changed)) error {
	conn, err := dbus.SessionBus()
	if err != nil {
		return err
	}

	if err := conn.AddMatchSignal(
		dbus.WithMatchObjectPath(apis.ObjectPath),
		dbus.WithMatchInterface(settingsCallPath),
		dbus.WithMatchMember("SettingChanged"),
	); err != nil {
		return err
	}

	dbusChan := make(chan *dbus.Signal)
	conn.Signal(dbusChan)

	for sig := range dbusChan {
		if len(sig.Body) == 0 {
			continue
		}

		namespace, ok := sig.Body[0].(string)
		if !ok {
			continue // We sometimes get responses from other portals.
		}

		changed := Changed{Namespace: namespace}

		if len(sig.Body) > 1 {
			key, ok := sig.Body[1].(string)
			if !ok {
				continue // Avoid crashing if the response is unexpected.
			}

			changed.Key = key
		}

		if len(sig.Body) > 2 {
			changed.Value = sig.Body[2]
			variant, ok := changed.Value.(dbus.Variant)
			if ok {
				changed.Value = variant.Value()
			}
		}

		callback(changed)
	}

	return nil
}
