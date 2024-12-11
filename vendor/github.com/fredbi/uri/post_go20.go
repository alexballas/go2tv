//go:build go1.20

package uri

// TODO: build tag should be go1.20

import "errors"

func newErr(msg string) error {
	return errors.New(msg)
}

// errorsJoin is a temporary indirection to keep support for go1.19
func errorsJoin(errs ...error) error {
	return errors.Join(errs...)
}
