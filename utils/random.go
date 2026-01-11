package utils

import (
	"crypto/rand"
	"fmt"
)

// RandomString generates a random string which we
// then use as callback path in our webserver.
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
