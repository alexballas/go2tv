package utils

import (
	"crypto/rand"
	"fmt"
)

// RandomString generates a random string which we
// the callback paths for our webservers.
func RandomString() (string, error) {
	b := make([]byte, 16)
	n, err := rand.Read(b)
	if err != nil {
		if n > 0 {
			return fmt.Sprintf("%X", b), nil
		}
		return "", fmt.Errorf("can't generate a random number: %w", err)
	}
	return fmt.Sprintf("%X", b), nil
}
