package appearance

import (
	"errors"
	"image/color"

	"github.com/rymdport/portal/settings"
)

// ErrNotSet indicates that the value is not set.
var ErrNotSet = errors.New("not set")

// ColorScheme indicates the system’s preferred color scheme.
type ColorScheme uint8

const (
	NoPreference = ColorScheme(iota) // No preference.
	Dark                             // Prefer dark appearance.
	Light                            // Prefer light appearance.
)

// GetColorScheme returns the currently set color scheme.
func GetColorScheme() (ColorScheme, error) {
	value, err := settings.ReadOne(Namespace, "color-scheme")
	if err != nil {
		return NoPreference, err
	}

	result := value.(uint32)
	if result > 2 {
		result = 0 // Unknown values should be treated as 0 (no preference).
	}

	return ColorScheme(result), nil
}

// GetAccentColor returns the currently set accent color.
// If not set, the ErrorNotSet will be returned.
func GetAccentColor() (*color.RGBA, error) {
	value, err := settings.ReadOne(Namespace, "accent-color")
	if err != nil {
		return nil, ErrNotSet
	}

	result := value.([]float64)
	if len(result) != 4 {
		return nil, ErrNotSet
	}

	red := result[0] * 255
	if red < 0 || red > 255 {
		return nil, ErrNotSet
	}

	green := result[1] * 255
	if green < 0 || green > 255 {
		return nil, ErrNotSet
	}

	blue := result[2] * 255
	if blue < 0 || blue > 255 {
		return nil, ErrNotSet
	}

	alpha := result[3] * 255
	if alpha < 0 || alpha > 255 {
		return nil, ErrNotSet
	}

	return &color.RGBA{
		R: uint8(red),
		G: uint8(green),
		B: uint8(blue),
		A: uint8(alpha),
	}, nil
}
