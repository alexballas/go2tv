package uri

// Error from the github.com/fredbi/uri module.
type Error interface {
	error
}

// Validation errors.
var (
	ErrNoSchemeFound         = Error(newErr("no scheme found in URI"))
	ErrInvalidURI            = Error(newErr("not a valid URI"))
	ErrInvalidCharacter      = Error(newErr("invalid character in URI"))
	ErrInvalidScheme         = Error(newErr("invalid scheme in URI"))
	ErrInvalidQuery          = Error(newErr("invalid query string in URI"))
	ErrInvalidFragment       = Error(newErr("invalid fragment in URI"))
	ErrInvalidPath           = Error(newErr("invalid path in URI"))
	ErrInvalidHost           = Error(newErr("invalid host in URI"))
	ErrInvalidPort           = Error(newErr("invalid port in URI"))
	ErrInvalidUserInfo       = Error(newErr("invalid userinfo in URI"))
	ErrMissingHost           = Error(newErr("missing host in URI"))
	ErrInvalidHostAddress    = Error(newErr("invalid address for host"))
	ErrInvalidRegisteredName = Error(newErr("invalid host (registered name)"))
	ErrInvalidDNSName        = Error(newErr("invalid host (DNS name)"))
)
