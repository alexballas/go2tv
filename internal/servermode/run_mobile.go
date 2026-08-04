//go:build android || ios

package servermode

import (
	"context"
	"errors"
	"io"
)

var ErrUnsupported = errors.New("server mode unsupported on mobile")

func Run(context.Context, Config, io.Writer) error { return ErrUnsupported }
