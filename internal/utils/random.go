package utils

import (
	"crypto/rand"
	"fmt"
)

func RandomString() (string, error) {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return "", fmt.Errorf("can't generate a random number: %w", err)
	}
	return fmt.Sprintf("%X", b), nil
}
