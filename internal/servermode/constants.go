package servermode

import "time"

const (
	defaultReadHeaderTimeout = 5 * time.Second
	defaultJSONTimeout       = 15 * time.Second
	defaultIdleTimeout       = 60 * time.Second
	defaultMaxHeaderBytes    = 64 << 10
)
