// Package uri is meant to be an RFC 3986 compliant URI builder and parser.
//
// This is based on the work from ttacon/uri (credits: Trey Tacon).
//
// This fork concentrates on RFC 3986 strictness for URI parsing and validation.
//
// Reference: https://tools.ietf.org/html/rfc3986
//
// Tests have been augmented with test suites of URI validators in other languages:
// perl, python, scala, .Net.
//
// Extra features like MySQL URIs present in the original repo have been removed.
package uri

import (
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"strings"
	"unicode"
)

// URI represents a general RFC3986 URI.
type URI interface {
	// Scheme the URI conforms to.
	Scheme() string

	// Authority information for the URI, including the "//" prefix.
	Authority() Authority

	// Query returns a map of key/value pairs of all parameters
	// in the query string of the URI.
	Query() url.Values

	// Fragment returns the fragment (component preceded by '#') in the
	// URI if there is one.
	Fragment() string

	// Builder returns a Builder that can be used to modify the URI.
	Builder() Builder

	// String representation of the URI
	String() string

	// Validate the different components of the URI
	Validate() error
}

// Authority information that a URI contains
// as specified by RFC3986.
//
// Username and password are given by UserInfo().
type Authority interface {
	UserInfo() string
	Host() string
	Port() string
	Path() string
	String() string
	Validate(...string) error
}

const (
	// char and string literals.
	colonMark          = ':'
	questionMark       = '?'
	fragmentMark       = '#'
	percentMark        = '%'
	atHost             = '@'
	slashMark          = '/'
	openingBracketMark = '['
	closingBracketMark = ']'
	authorityPrefix    = "//"
)

var (
	// predefined sets of accecpted runes beyond the "unreserved" character set
	pcharExtraRunes           = []rune{':', '@'} // pchar = unreserved | ':' | '@'
	queryOrFragmentExtraRunes = append(pcharExtraRunes, '/', '?')
	userInfoExtraRunes        = append(pcharExtraRunes, ':')
)

// IsURI tells if a URI is valid according to RFC3986/RFC397.
func IsURI(raw string) bool {
	_, err := Parse(raw)
	return err == nil
}

// IsURIReference tells if a URI reference is valid according to RFC3986/RFC397
//
// Reference: https://www.rfc-editor.org/rfc/rfc3986#section-4.1 and
// https://www.rfc-editor.org/rfc/rfc3986#section-4.2
func IsURIReference(raw string) bool {
	_, err := ParseReference(raw)
	return err == nil
}

// Parse attempts to parse a URI.
// It returns an error if the URI is not RFC3986-compliant.
func Parse(raw string) (URI, error) {
	return parse(raw, false)
}

// ParseReference attempts to parse a URI relative reference.
//
// It returns an error if the URI is not RFC3986-compliant.
func ParseReference(raw string) (URI, error) {
	return parse(raw, true)
}

func parse(raw string, withURIReference bool) (URI, error) {
	var (
		scheme string
		curr   int
	)

	schemeEnd := strings.IndexByte(raw, colonMark)      // position of a ":"
	hierPartEnd := strings.IndexByte(raw, questionMark) // position of a "?"
	queryEnd := strings.IndexByte(raw, fragmentMark)    // position of a "#"

	// exclude pathological input
	if schemeEnd == 0 || hierPartEnd == 0 || queryEnd == 0 {
		// ":", "?", "#"
		return nil, ErrInvalidURI
	}

	if schemeEnd == 1 {
		return nil, errorsJoin(
			ErrInvalidScheme,
			fmt.Errorf("scheme has a minimum length of 2 characters"),
		)
	}

	if hierPartEnd == 1 || queryEnd == 1 {
		// ".:", ".?", ".#"
		return nil, ErrInvalidURI
	}

	if hierPartEnd > 0 && hierPartEnd < schemeEnd || queryEnd > 0 && queryEnd < schemeEnd {
		// e.g. htt?p: ; h#ttp: ..
		return nil, ErrInvalidURI
	}

	if queryEnd > 0 && queryEnd < hierPartEnd {
		// e.g.  https://abc#a?b
		hierPartEnd = queryEnd
	}

	isRelative := strings.HasPrefix(raw, authorityPrefix)
	switch {
	case schemeEnd > 0 && !isRelative:
		scheme = raw[curr:schemeEnd]
		if schemeEnd+1 == len(raw) {
			// trailing ':' (e.g. http:)
			u := &uri{
				scheme: scheme,
			}

			return u, u.Validate()
		}
	case !withURIReference:
		// scheme is required for URI
		return nil, errorsJoin(
			ErrNoSchemeFound,
			fmt.Errorf("for URI (not URI reference), the scheme is required"),
		)
	case isRelative:
		// scheme is optional for URI references.
		//
		// start with // and a ':' is following... e.g //example.com:8080/path
		schemeEnd = -1
	}

	curr = schemeEnd + 1

	if hierPartEnd == len(raw)-1 || (hierPartEnd < 0 && queryEnd < 0) {
		// trailing ? or (no query & no fragment)
		if hierPartEnd < 0 {
			hierPartEnd = len(raw)
		}

		authorityInfo, err := parseAuthority(raw[curr:hierPartEnd])
		if err != nil {
			return nil, errorsJoin(ErrInvalidURI, err)
		}

		u := &uri{
			scheme:    scheme,
			hierPart:  raw[curr:hierPartEnd],
			authority: authorityInfo,
		}

		return u, u.Validate()
	}

	var (
		hierPart, query, fragment string
		authorityInfo             authorityInfo
		err                       error
	)

	if hierPartEnd > 0 {
		hierPart = raw[curr:hierPartEnd]
		authorityInfo, err = parseAuthority(hierPart)
		if err != nil {
			return nil, errorsJoin(ErrInvalidURI, err)
		}

		if hierPartEnd+1 < len(raw) {
			if queryEnd < 0 {
				// query ?, no fragment
				query = raw[hierPartEnd+1:]
			} else if hierPartEnd < queryEnd-1 {
				// query ?, fragment
				query = raw[hierPartEnd+1 : queryEnd]
			}
		}

		curr = hierPartEnd + 1
	}

	if queryEnd == len(raw)-1 && hierPartEnd < 0 {
		// trailing #,  no query "?"
		hierPart = raw[curr:queryEnd]
		authorityInfo, err = parseAuthority(hierPart)
		if err != nil {
			return nil, errorsJoin(ErrInvalidURI, err)
		}

		u := &uri{
			scheme:    scheme,
			hierPart:  hierPart,
			authority: authorityInfo,
			query:     query,
		}

		return u, u.Validate()
	}

	if queryEnd > 0 {
		// there is a fragment
		if hierPartEnd < 0 {
			// no query
			hierPart = raw[curr:queryEnd]
			authorityInfo, err = parseAuthority(hierPart)
			if err != nil {
				return nil, errorsJoin(ErrInvalidURI, err)
			}
		}

		if queryEnd+1 < len(raw) {
			fragment = raw[queryEnd+1:]
		}
	}

	u := &uri{
		scheme:    scheme,
		hierPart:  hierPart,
		query:     query,
		fragment:  fragment,
		authority: authorityInfo,
	}

	return u, u.Validate()
}

type uri struct {
	// raw components
	scheme   string
	hierPart string
	query    string
	fragment string

	// parsed components
	authority authorityInfo
}

func (u *uri) URI() URI {
	return u
}

func (u *uri) Scheme() string {
	return u.scheme
}

func (u *uri) Authority() Authority {
	u.ensureAuthorityExists()
	return u.authority
}

// Query returns parsed query parameters like standard lib URL.Query().
func (u *uri) Query() url.Values {
	v, _ := url.ParseQuery(u.query)
	return v
}

func (u *uri) Fragment() string {
	return u.fragment
}

func isNumerical(input string) bool {
	return strings.IndexFunc(input,
		func(r rune) bool { return r < '0' || r > '9' },
	) == -1
}

// Validate checks that all parts of a URI abide by allowed characters.
func (u *uri) Validate() error {
	if u.scheme != "" {
		if err := u.validateScheme(u.scheme); err != nil {
			return err
		}
	}

	if u.query != "" {
		if err := u.validateQuery(u.query); err != nil {
			return err
		}
	}

	if u.fragment != "" {
		if err := u.validateFragment(u.fragment); err != nil {
			return err
		}
	}

	if u.hierPart != "" {
		return u.Authority().Validate(u.scheme)
	}

	// empty hierpart case
	return nil
}

// validateScheme verifies the correctness of the scheme part.
//
// Reference: https://www.rfc-editor.org/rfc/rfc3986#section-3.1
//
//	scheme = ALPHA *( ALPHA / DIGIT / "+" / "-" / "." )
//
// NOTE: scheme is not supposed to contain any percent-encoded sequence.
//
// TODO(fredbi): verify the IRI RFC to check if unicode is allowed in scheme.
func (u *uri) validateScheme(scheme string) error {
	if len(scheme) < 2 {
		return ErrInvalidScheme
	}

	for i, r := range scheme {
		if i == 0 {
			if !unicode.IsLetter(r) {
				return ErrInvalidScheme
			}

			continue
		}

		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '+' && r != '-' && r != '.' {
			return ErrInvalidScheme
		}
	}

	return nil
}

// validateQuery validates the query part.
//
// Reference: https://www.rfc-editor.org/rfc/rfc3986#section-3.4
//
//	pchar = unreserved / pct-encoded / sub-delims / ":" / "@"
//	query = *( pchar / "/" / "?" )
func (u *uri) validateQuery(query string) error {
	if err := validateUnreservedWithExtra(query, queryOrFragmentExtraRunes); err != nil {
		return errorsJoin(ErrInvalidQuery, err)
	}

	return nil
}

// validateFragment validatesthe fragment part.
//
// Reference: https://www.rfc-editor.org/rfc/rfc3986#section-3.5
//
//	pchar = unreserved / pct-encoded / sub-delims / ":" / "@"
//
// fragment    = *( pchar / "/" / "?" )
func (u *uri) validateFragment(fragment string) error {
	if err := validateUnreservedWithExtra(fragment, queryOrFragmentExtraRunes); err != nil {
		return errorsJoin(ErrInvalidFragment, err)
	}

	return nil
}

func validateUnreservedWithExtra(s string, acceptedRunes []rune) error {
	skip := 0
	for i, r := range s {
		if skip > 0 {
			skip--
			continue
		}

		// accepts percent-encoded sequences
		if r == '%' {
			if i+2 >= len(s) || !isHex(s[i+1]) || !isHex(s[i+2]) {
				return fmt.Errorf("part %q contains a malformed percent-encoded part near [%s...]", s, s[:i])
			}

			skip = 2

			continue
		}

		// RFC grammar definitions:
		// sub-delims  = "!" / "$" / "&" / "'" / "(" / ")"
		//               / "*" / "+" / "," / ";" / "="
		// gen-delims  = ":" / "/" / "?" / "#" / "[" / "]" / "@"
		// unreserved    = ALPHA / DIGIT / "-" / "." / "_" / "~"
		// pchar         = unreserved / pct-encoded / sub-delims / ":" / "@"
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) &&
			// unreserved
			r != '-' && r != '.' && r != '_' && r != '~' &&
			// sub-delims
			r != '!' && r != '$' && r != '&' && r != '\'' && r != '(' && r != ')' &&
			r != '*' && r != '+' && r != ',' && r != ';' && r != '=' {
			runeFound := false
			for _, acceptedRune := range acceptedRunes {
				if r == acceptedRune {
					runeFound = true
					break
				}
			}
			if !runeFound {
				return fmt.Errorf("%q contains an invalid character: '%U' (%q)", s, r, r)
			}
		}
	}

	return nil
}

func isHex[T byte | rune](c T) bool {
	switch {
	case '0' <= c && c <= '9':
		return true
	case 'a' <= c && c <= 'f':
		return true
	case 'A' <= c && c <= 'F':
		return true
	}
	return false
}

// validateIPvFuture covers the special provision in the RFC for future IP scheme.
// The passed argument removes the heading "v" character.
//
// Example: http://[v6.fe80::a_en1]
//
//	Reference: https://www.rfc-editor.org/rfc/rfc3986#section-3.2.2
//
//	IPvFuture     = "v" 1*HEXDIG "." 1*( unreserved / sub-delims / ":" )
func validateIPvFuture(address string) error {
	rr := strings.NewReader(address)
	var (
		foundHexDigits, foundDot bool
	)

	for {
		r, _, err := rr.ReadRune()
		if err == io.EOF {
			break
		}

		if r == '.' {
			foundDot = true

			break
		}

		if !isHex(r) {
			return errors.New(
				"invalid IP vFuture format: expect an hexadecimal version tag",
			)
		}

		foundHexDigits = true
	}

	if !foundHexDigits || !foundDot {
		return errors.New(
			"invalid IP vFuture format: expect a '.' after an hexadecimal version tag",
		)
	}

	if rr.Len() == 0 {
		return errors.New("invalid IP vFuture format: expect a non-empty address after the version tag")
	}

	offset, _ := rr.Seek(0, io.SeekCurrent)

	return validateUnreservedWithExtra(address[offset:], userInfoExtraRunes)
}

type authorityInfo struct {
	prefix   string
	userinfo string
	host     string
	port     string
	path     string
	isIPv6   bool
}

func (a authorityInfo) UserInfo() string { return a.userinfo }
func (a authorityInfo) Host() string     { return a.host }
func (a authorityInfo) Port() string     { return a.port }
func (a authorityInfo) Path() string     { return a.path }
func (a authorityInfo) String() string {
	buf := strings.Builder{}
	buf.WriteString(a.prefix)
	buf.WriteString(a.userinfo)

	if len(a.userinfo) > 0 {
		buf.WriteByte(atHost)
	}

	if a.isIPv6 {
		buf.WriteString("[" + a.host + "]")
	} else {
		buf.WriteString(a.host)
	}

	if len(a.port) > 0 {
		buf.WriteByte(colonMark)
	}

	buf.WriteString(a.port)
	buf.WriteString(a.path)

	return buf.String()
}

// Validate the Authority part.
//
// Reference: https://www.rfc-editor.org/rfc/rfc3986#section-3.2
func (a authorityInfo) Validate(schemes ...string) error {
	if a.path != "" {
		if err := a.validatePath(a.path); err != nil {
			return err
		}
	}

	if a.host != "" {
		if err := a.validateHost(a.host, a.isIPv6, schemes...); err != nil {
			return err
		}
	}

	if a.port != "" {
		if err := a.validatePort(a.port, a.host); err != nil {
			return err
		}
	}

	if a.userinfo != "" {
		if err := a.validateUserInfo(a.userinfo); err != nil {
			return err
		}
	}

	return nil
}

// validatePath validates the path part.
//
// Reference: https://www.rfc-editor.org/rfc/rfc3986#section-3.3
func (a authorityInfo) validatePath(path string) error {
	if a.host == "" && a.port == "" && len(path) >= 2 && path[0] == '/' && path[1] == '/' {
		return errorsJoin(
			ErrInvalidPath,
			fmt.Errorf(
				`if a URI does not contain an authority component, then the path cannot begin with two slash characters ("//"): %q`,
				a.path,
			))
	}

	// NOTE: this loop used to be neatly written with strings.Split().
	// However, analysis showed that constantly allocating the returned slice
	// was a significant burden on the gc (at least when compared to the workload
	// the rest of this module generates).
	var previousPos int
	for pos, char := range path {
		if char != '/' {
			continue
		}

		if pos > previousPos {
			if err := validateUnreservedWithExtra(path[previousPos:pos], pcharExtraRunes); err != nil {
				return errorsJoin(
					ErrInvalidPath,
					err,
				)
			}
		}

		previousPos = pos + 1
	}

	if previousPos < len(path) { // don't care if the last char was a separator
		if err := validateUnreservedWithExtra(path[previousPos:], pcharExtraRunes); err != nil {
			return errorsJoin(
				ErrInvalidPath,
				err,
			)
		}
	}

	return nil
}

// validateHost validates the host part.
//
// Reference: https://www.rfc-editor.org/rfc/rfc3986#section-3.2.2
func (a authorityInfo) validateHost(host string, isIPv6 bool, schemes ...string) error {
	var unescapedHost string

	if strings.ContainsRune(host, '%') {
		// only proceed with PathUnescape if we need to (saves an alloc otherwise)
		var err error
		unescapedHost, err = url.PathUnescape(host)
		if err != nil {
			return errorsJoin(
				ErrInvalidHost,
				fmt.Errorf("invalid percent-encoding in the host part"),
			)
		}
	} else {
		unescapedHost = host
	}

	if isIPv6 {
		return validateIPv6(unescapedHost)
	}

	// check for IPv4 address
	//
	// The host SHOULD check
	// the string syntactically for a dotted-decimal number before
	// looking it up in the Domain Name System.

	// IPv4 may contain percent-encoded escaped characters, e.g. 192.168.0.%31 is valid.
	// Reference: https://www.rfc-editor.org/rfc/rfc3986#appendix-A
	//
	//  IPv4address   = dec-octet "." dec-octet "." dec-octet "." dec-octet
	//  dec-octet     = DIGIT                 ; 0-9
	//    / %x31-39 DIGIT         ; 10-99
	//    / "1" 2DIGIT            ; 100-199
	//    / "2" %x30-34 DIGIT     ; 200-249
	//    / "25" %x30-35          ; 250-255
	if addr, err := netip.ParseAddr(unescapedHost); err == nil {
		if !addr.Is4() {
			return errorsJoin(
				ErrInvalidHostAddress,
				fmt.Errorf("a host as an address, without square brackets, should refer to an IPv4 address: %q", host),
			)
		}

		return nil
	}

	// this is not an IP, check for host DNS or registered name
	if err := validateHostForScheme(host, unescapedHost, schemes...); err != nil {
		return errorsJoin(
			ErrInvalidHost,
			err,
		)
	}

	return nil
}

func validateIPv6(unescapedHost string) error {
	// address the provision made in the RFC for a "IPvFuture"
	if unescapedHost[0] == 'v' || unescapedHost[0] == 'V' {
		if err := validateIPvFuture(unescapedHost[1:]); err != nil {
			return errorsJoin(
				ErrInvalidHostAddress,
				err,
			)
		}

		return nil
	}

	// check for IPv6 address
	// IPv6 may contain percent-encoded escaped characters
	addr, err := netip.ParseAddr(unescapedHost)
	if err != nil {
		// RFC3986 stipulates that only IPv6 addresses are within square brackets
		return errorsJoin(
			ErrInvalidHostAddress,
			fmt.Errorf("a square-bracketed host part should be a valid IPv6 address: %q", unescapedHost),
		)
	}

	if !addr.Is6() {
		return errorsJoin(
			ErrInvalidHostAddress,
			fmt.Errorf("a square-bracketed host part should not contain an IPv4 address: %q", unescapedHost),
		)
	}

	return nil
}

// validateHostForScheme validates the host according to 2 different sets of rules:
//   - if the scheme is a scheme well-known for using DNS host names, the DNS host validation applies (RFC)
//     (applies to schemes at: https://www.iana.org/assignments/uri-schemes/uri-schemes.xhtml)
//   - otherwise, applies the "registered-name" validation stated by RFC 3986:
//
// dns-name see: https://www.rfc-editor.org/rfc/rfc1034, https://www.rfc-editor.org/info/rfc5890
// reg-name    = *( unreserved / pct-encoded / sub-delims )
func validateHostForScheme(host, unescapedHost string, schemes ...string) error {
	for _, scheme := range schemes {
		if UsesDNSHostValidation(scheme) {
			if err := validateDNSHostForScheme(unescapedHost); err != nil {
				return err
			}
		}

		if err := validateRegisteredHostForScheme(host); err != nil {
			return err
		}
	}

	return nil
}

func validateDNSHostForScheme(unescapedHost string) error {
	// DNS name
	if len(unescapedHost) > 255 {
		// warning: size in bytes, not in runes (existing bug, or is it really?) -- TODO(fredbi)
		return errorsJoin(
			ErrInvalidDNSName,
			fmt.Errorf("hostname is longer than the allowed 255 characters"),
		)
	}

	/*
	   <domain> ::= <subdomain> | " "
	   <subdomain> ::= <label> | <subdomain> "." <label>
	   <label> ::= <letter> [ [ <ldh-str> ] <let-dig> ]
	   <ldh-str> ::= <let-dig-hyp> | <let-dig-hyp> <ldh-str>
	   <let-dig-hyp> ::= <let-dig> | "-"
	   <let-dig> ::= <letter> | <digit>
	   <letter> ::= any one of the 52 alphabetic characters A through Z in
	   upper case and a through z in lower case
	   <digit> ::= any one of the ten digits 0 through 9
	*/

	var previousPos int
	for pos, char := range unescapedHost {
		if char != '.' {
			continue
		}

		if err := validateHostSegment(unescapedHost[previousPos:pos]); err != nil {
			return err
		}

		previousPos = pos + 1
	}

	if previousPos <= len(unescapedHost) { // trailing separator: empty last segment. It matters here
		return validateHostSegment(unescapedHost[previousPos:])
	}

	return nil
}

func validateRegisteredHostForScheme(host string) error {
	// RFC 3986 registered name
	if err := validateUnreservedWithExtra(host, nil); err != nil {
		return errorsJoin(
			ErrInvalidRegisteredName,
			err,
		)
	}

	return nil
}

func validateHostSegment(segment string) error {
	if len(segment) == 0 {
		return errorsJoin(
			ErrInvalidDNSName,
			fmt.Errorf("a DNS name should not contain an empty segment"),
		)
	}

	if len(segment) > 63 {
		return errorsJoin(
			ErrInvalidDNSName,
			fmt.Errorf("a segment in a DNS name should not be longer than 63 characters: %q", segment[:63]),
		)
	}

	rr := strings.NewReader(segment)
	r, _, err := rr.ReadRune()
	if err != nil {
		// strings.RuneReader doesn't actually return any other error than io.EOF,
		// which is not supposed to happen given the above check on length.
		return errorsJoin(
			ErrInvalidDNSName,
			fmt.Errorf("a segment in a DNS name contains an invalid rune: %q contains %q", segment, r),
		)
	}

	if !unicode.IsLetter(r) {
		return errorsJoin(
			ErrInvalidDNSName,
			fmt.Errorf("a segment in a DNS name must begin with a letter: %q starts with %q", segment, r),
		)
	}

	var (
		last rune
		once bool
	)

	for {
		r, _, err = rr.ReadRune()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			// strings.RuneReader doesn't actually return any other error than io.EOF
			return errorsJoin(
				ErrInvalidDNSName,
				fmt.Errorf("a segment in a DNS name contains an invalid rune: %q: with %U (%q)", segment, r, r),
			)
		}
		once = true

		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' {
			return errorsJoin(
				ErrInvalidDNSName,
				fmt.Errorf("a segment in a DNS name must contain only letters, digits or '-': %q contains %q", segment, r),
			)
		}

		last = r
	}

	// last rune in segment
	if once && !unicode.IsLetter(last) && !unicode.IsDigit(last) {
		return errorsJoin(
			ErrInvalidDNSName,
			fmt.Errorf("a segment in a DNS name must end with a letter or a digit: %q ends with %q", segment, last),
		)
	}

	return nil
}

// validatePort validates the port part.
//
// Reference: https://www.rfc-editor.org/rfc/rfc3986#section-3.2.3
//
// port = *DIGIT
func (a authorityInfo) validatePort(port, host string) error {
	if !isNumerical(port) {
		return ErrInvalidPort
	}

	if host == "" {
		return errorsJoin(
			ErrMissingHost,
			fmt.Errorf("whenever a port is specified, a host part must be present"),
		)
	}

	return nil
}

// validateUserInfo validates the userinfo part.
//
// Reference: https://www.rfc-editor.org/rfc/rfc3986#section-3.2.1
//
// userinfo    = *( unreserved / pct-encoded / sub-delims / ":" )
func (a authorityInfo) validateUserInfo(userinfo string) error {
	if err := validateUnreservedWithExtra(userinfo, userInfoExtraRunes); err != nil {
		return errorsJoin(
			ErrInvalidUserInfo,
			err,
		)
	}

	return nil
}

func parseAuthority(hier string) (authorityInfo, error) {
	// as per RFC 3986 Section 3.6
	var (
		prefix, userinfo, host, port, path string
		isIPv6                             bool
	)

	// authority sections MUST begin with a '//'
	if strings.HasPrefix(hier, authorityPrefix) {
		prefix = authorityPrefix
		hier = strings.TrimPrefix(hier, authorityPrefix)
	}

	if prefix == "" {
		path = hier
	} else {
		// authority   = [ userinfo "@" ] host [ ":" port ]
		slashEnd := strings.IndexByte(hier, slashMark)
		if slashEnd > -1 {
			if slashEnd < len(hier) {
				path = hier[slashEnd:]
			}
			hier = hier[:slashEnd]
		}

		host = hier
		if at := strings.IndexByte(host, atHost); at > 0 {
			userinfo = host[:at]
			if at+1 < len(host) {
				host = host[at+1:]
			}
		}

		if bracket := strings.IndexByte(host, openingBracketMark); bracket >= 0 {
			// ipv6 addresses: "["xx:yy:zz"]":port
			rawHost := host
			closingbracket := strings.IndexByte(host, closingBracketMark)
			switch {
			case closingbracket > bracket+1:
				host = host[bracket+1 : closingbracket]
				rawHost = rawHost[closingbracket+1:]
				isIPv6 = true
			case closingbracket > bracket:
				return authorityInfo{}, errorsJoin(
					ErrInvalidHostAddress,
					fmt.Errorf("empty IPv6 address"),
				)
			default:
				return authorityInfo{}, errorsJoin(
					ErrInvalidHostAddress,
					fmt.Errorf("mismatched square brackets"),
				)
			}

			if colon := strings.IndexByte(rawHost, colonMark); colon >= 0 {
				if colon+1 < len(rawHost) {
					port = rawHost[colon+1:]
				}
			}
		} else {
			if colon := strings.IndexByte(host, colonMark); colon >= 0 {
				if colon+1 < len(host) {
					port = host[colon+1:]
				}
				host = host[:colon]
			}
		}
	}

	return authorityInfo{
		prefix:   prefix,
		userinfo: userinfo,
		host:     host,
		isIPv6:   isIPv6,
		port:     port,
		path:     path,
	}, nil
}

func (u *uri) ensureAuthorityExists() {
	if u.authority.userinfo != "" ||
		u.authority.host != "" ||
		u.authority.port != "" {
		u.authority.prefix = "//"
	}
}

// String representation of an URI.
//
// * https://www.rfc-editor.org/rfc/rfc3986#section-6.2.2.1 and later
func (u *uri) String() string {
	buf := strings.Builder{}
	if len(u.scheme) > 0 {
		buf.WriteString(u.scheme)
		buf.WriteByte(colonMark)
	}

	buf.WriteString(u.authority.String())

	if len(u.query) > 0 {
		buf.WriteByte(questionMark)
		buf.WriteString(u.query)
	}

	if len(u.fragment) > 0 {
		buf.WriteByte(fragmentMark)
		buf.WriteString(u.fragment)
	}

	return buf.String()
}
