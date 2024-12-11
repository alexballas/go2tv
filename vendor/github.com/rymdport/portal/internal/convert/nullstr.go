package convert

import (
	"reflect"

	"github.com/godbus/dbus/v5"
)

var bytesSignature = dbus.SignatureOfType(reflect.TypeOf([]byte{}))

// ToNullTerminatedValue converts a regular string into a null terminated byte string.
func ToNullTerminatedValue(input string) dbus.Variant {
	return dbus.MakeVariantWithSignature(FromStringToNullTerminated(input), bytesSignature)
}

// FromStringToNullTerminated converts a regular string into a null terminated byte string
func FromStringToNullTerminated(input string) []byte {
	terminated := make([]byte, len(input)+1)
	copy(terminated, input)
	return terminated
}
