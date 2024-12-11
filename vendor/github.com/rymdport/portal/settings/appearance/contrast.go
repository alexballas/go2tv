package appearance

import "github.com/rymdport/portal/settings"

// Contrast indicates the system’s preferred contrast level.
type Contrast uint8

const (
	NormalContrast = Contrast(iota) // No preference (normal contrast)
	HigherContrast                  // Higher contrast
)

// GetContrast returns the currently set contrast setting.
func GetContrast() (Contrast, error) {
	value, err := settings.ReadOne(Namespace, "color-scheme")
	if err != nil {
		return NormalContrast, err
	}

	result := value.(uint32)
	if result > 1 {
		result = 0 // Unknown values should be treated as 0 (no preference).
	}

	return Contrast(result), nil
}
