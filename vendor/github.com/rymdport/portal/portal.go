package portal

import "errors"

// ErrunexpectedResonse is returned when the received dbus data was in an unexpected format.
var ErrUnexpectedResponse = errors.New("unexpected response from dbus")
