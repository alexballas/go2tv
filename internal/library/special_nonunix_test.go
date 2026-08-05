//go:build !unix

package library

import "testing"

func makeFIFO(string) error         { return nil }
func makeSocket(*testing.T, string) {}
