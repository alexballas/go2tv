package utils

import (
	"testing"
)

func TestRandomString(t *testing.T) {
	_, err := RandomString()
	if err != nil {
		t.Fatalf("RandomString: failed to test due to error %s", err.Error())
	}
}
