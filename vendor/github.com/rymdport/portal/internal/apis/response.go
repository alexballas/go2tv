package apis

import (
	"github.com/godbus/dbus/v5"
	"github.com/rymdport/portal"
)

// ReadResponse takes the given dbus connection and tries to read the responce object.
// This only works for dbus clals that have an associated response.
func ReadResponse(conn *dbus.Conn, call *dbus.Call) (map[string]dbus.Variant, error) {
	var responsePath dbus.ObjectPath
	err := call.Store(&responsePath)
	if err != nil {
		return nil, err
	}

	err = conn.AddMatchSignal(
		dbus.WithMatchObjectPath(responsePath),
		dbus.WithMatchInterface(RequestInterface),
		dbus.WithMatchMember(ResponseMember),
	)
	if err != nil {
		return nil, err
	}

	dbusChan := make(chan *dbus.Signal)
	conn.Signal(dbusChan)

	response := <-dbusChan
	if len(response.Body) != 2 {
		return nil, portal.ErrUnexpectedResponse
	}

	if responseKey, ok := response.Body[0].(uint32); !ok {
		return nil, portal.ErrUnexpectedResponse
	} else if responseKey == 1 || responseKey == 2 {
		return nil, nil
	}

	result, ok := response.Body[1].(map[string]dbus.Variant)
	if !ok {
		return nil, portal.ErrUnexpectedResponse
	}

	return result, nil
}
