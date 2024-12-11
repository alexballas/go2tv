//go:build !go1.20

package uri

import (
	"errors"
	"fmt"
)

// TODO: build tag should be go1.20

var _ Error = &uriErr{}

type uriErr struct {
	err     error
	wrapped error
}

func (e uriErr) Unwrap() error {
	return e.wrapped
}

func (e *uriErr) Is(target error) bool {
	return e == target || errors.Is(e.err, target) || errors.Is(e.wrapped, target)
}

func (e uriErr) Error() string {
	if e.wrapped != nil {
		return fmt.Sprintf("%v: %v", e.err, e.wrapped)
	}

	if e.err != nil {
		return e.err.Error()
	}

	return ""
}

// newErr emulates a wrappable error available with go1.20 standard library.
func newErr(msg string) error {
	return &uriErr{
		err: fmt.Errorf(msg),
	}
}

// errorsJoin emulates the error wrapping carried out with go1.20 errors standard library.
func errorsJoin(errs ...error) error {
	if len(errs) == 0 {
		return nil
	}

	wrappedErr := &uriErr{err: errs[0]}
	current := wrappedErr

	for _, err := range errs[1:] {
		if err == nil {
			continue
		}

		w := &uriErr{err: err}
		current.wrapped = w
		current = w
	}

	return wrappedErr
}
