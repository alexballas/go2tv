# uri
![Lint](https://github.com/fredbi/uri/actions/workflows/01-golang-lint.yaml/badge.svg)
![CI](https://github.com/fredbi/uri/actions/workflows/02-test.yaml/badge.svg)
[![Coverage Status](https://coveralls.io/repos/github/fredbi/uri/badge.svg?branch=master)](https://coveralls.io/github/fredbi/uri?branch=master)
![Vulnerability Check](https://github.com/fredbi/uri/actions/workflows/03-govulncheck.yaml/badge.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/fredbi/uri)](https://goreportcard.com/report/github.com/fredbi/uri)

![GitHub tag (latest by date)](https://img.shields.io/github/v/tag/fredbi/uri)
[![Go Reference](https://pkg.go.dev/badge/github.com/fredbi/uri.svg)](https://pkg.go.dev/github.com/fredbi/uri)
[![license](http://img.shields.io/badge/license/License-MIT-yellow.svg)](https://raw.githubusercontent.com/fredbi/uri/master/LICENSE.md)


Package uri is meant to be an RFC 3986 compliant URI builder, parser and validator for `golang`.

It supports strict RFC validation for URI and URI relative references.

This allows for stricter conformance than the `net/url` package in the Go standard libary,
which provides a workable but loose implementation of the RFC for URLs.

## What's new?

### v1.1.0

**Build**

* requires go1.19

**Features**

* Typed errors: parsing and validation now returns errors of type `uri.Error`,
  with a more accurate pinpointing of the error provided by the value.
  Errors support the go1.20 addition to standard errors with `Join()` and `Cause()`.
  For go1.19, backward compatibility is ensured (errors.Join() is emulated).
* DNS schemes can be overridden at the package level

**Performances**

* Significantly improved parsing speed by dropping usage of regular expressions and reducing allocations (~ x20 faster).

**Fixes**

* stricter compliance regarding paths beginning with a double '/'
* stricter compliance regarding the length of DNS names and their segments
* stricter compliance regarding IPv6 addresses with an empty zone
* stricter compliance regarding IPv6 vs IPv4 litterals
* an empty IPv6 litteral `[]` is invalid

**Known open issues**

* IRI validation lacks strictness
* IPv6 validation relies on the standard library and lacks strictness

**Other**

Major refactoring to enhance code readability, esp. for testing code.

* Refactored validations
* Refactored test suite
* Added support for fuzzing, dependabots & codeQL scans

## Usage

### Parsing

```go
	u, err := Parse("https://example.com:8080/path")
	if err != nil {
		fmt.Printf("Invalid URI")
	} else {
		fmt.Printf("%s", u.Scheme())
	}
	// Output: https
```

```go
	u, err := ParseReference("//example.com/path")
	if err != nil {
		fmt.Printf("Invalid URI reference")
	} else {
		fmt.Printf("%s", u.Authority().Path())
	}
	// Output: /path
```

### Validating

```go
    isValid := IsURI("urn://example.com?query=x#fragment/path") // true

    isValid= IsURI("//example.com?query=x#fragment/path") // false

    isValid= IsURIReference("//example.com?query=x#fragment/path") // true
```

#### Caveats

* **Registered name vs DNS name**: RFC3986 defines a super-permissive "registered name" for hosts, for URIs
  not specifically related to an Internet name. Our validation performs a stricter host validation according
  to DNS rules whenever the scheme is a well-known IANA-registered scheme
  (the function `UsesDNSHostValidation(string) bool` is customizable).

> Examples:
> `ftp://host`, `http://host` default to validating a proper DNS hostname.

* **IPv6 validation** relies on IP parsing from the standard library. It is not super strict
  regarding the full-fledged IPv6 specification.

* **URI vs URL**: every URL should be a URI, but the converse does not always hold. This module intends to perform
  stricter validation than the pragmatic standard library `net/url`, which currently remains about 30% faster.

* **URI vs IRI**: at this moment, this module checks for URI, while supporting unicode letters as `ALPHA` tokens.
  This is not strictly compliant with the IRI specification (see known issues).

### Building

The exposed type `URI` can be transformed into a fluent `Builder` to set the parts of an URI.

```go
	aURI, _ := Parse("mailto://user@domain.com")
	newURI := auri.Builder().SetUserInfo(test.name).SetHost("newdomain.com").SetScheme("http").SetPort("443")
```

### Canonicalization

Not supported for now (contemplated as a topic for V2).

For URL normalization, see [PuerkitoBio/purell](https://github.com/PuerkitoBio/purell).

## Reference specifications

The librarian's corner (still WIP).

|Title|Reference|Notes|
|---------------------------------------------|----------------------------------------------------|----------------|
| Uniform Resource Identifier (URI)           | [RFC3986](https://www.rfc-editor.org/rfc/rfc3986)  | Deviations (1) |
| Uniform Resource Locator (URL)              | [RFC1738](https://www.rfc-editor.org/info/rfc1738) | |
| Relative URL                                | [RFC1808](https://www.rfc-editor.org/info/rfc1808) | |
| Internationalized Resource Identifier (IRI) | [RFC3987](https://tools.ietf.org/html/rfc3987)     | (1) |
| IPv6 addressing scheme reference and erratum|                                                    | (2) |
| Representing IPv6 Zone Identifiers| [RFC6874](https://www.rfc-editor.org/rfc/rfc6874.txt) |      | |
| https://tools.ietf.org/html/rfc6874         | ||
| https://www.rfc-editor.org/rfc/rfc3513      | ||

(1) Deviations from the RFC:
* Tokens: ALPHAs are tolerated to be Unicode Letter codepoints, DIGITs are tolerated to be Unicode Digit codepoints.
  Some improvements are needed to abide more strictly to IRIi's provisions for internationalization.

(2) IP addresses:
* Now validation is stricter regarding `[...]` litterals (which _must_ be IPv6) and ddd.ddd.ddd.ddd litterals (which _must_ be IPv4).
* RFC3886 requires the 6 parts of the IPv6 to be present. This module tolerates common syntax, such as `[::]`.
  Notice that `[]` is illegal, although the golang IP parser equates this to `[::]` (zero value IP).
* IPv6 zones are supported, with the '%' escaped as '%25'

## [FAQ](docs/FAQ.md)

## [Benchmarks](docs/BENCHMARKS.md)

## Credits

* Tests have been aggregated from the  test suites of URI validators from other languages:
Perl, Python, Scala, .Net. and the Go url standard library.

* This package was initially based on the work from ttacon/uri (credits: Trey Tacon).
> Extra features like MySQL URIs present in the original repo have been removed.

* A lot of improvements and suggestions have been brought by the incredible guys at [`fyne-io`](https://github.com/fyne-io). Thanks all.

## TODOs

- [] Support IRI `ucschar` as unreserved characters
- [] Support IRI iprivate in query
- [] Prepare v2. See [the proposal](docs/V2.md)
- [] Revisit URI vs IRI support & strictness, possibly with options (V2?)
- [] [Other investigations](./docs/TODO.md)

### Notes
```
ucschar        = %xA0-D7FF / %xF900-FDCF / %xFDF0-FFEF
                  / %x10000-1FFFD / %x20000-2FFFD / %x30000-3FFFD
                  / %x40000-4FFFD / %x50000-5FFFD / %x60000-6FFFD
                  / %x70000-7FFFD / %x80000-8FFFD / %x90000-9FFFD
                  / %xA0000-AFFFD / %xB0000-BFFFD / %xC0000-CFFFD
                  / %xD0000-DFFFD / %xE1000-EFFFD
```

```
iprivate       = %xE000-F8FF / %xF0000-FFFFD / %x100000-10FFFD

		// TODO: RFC6874
		//  A <zone_id> SHOULD contain only ASCII characters classified as
   		// "unreserved" for use in URIs [RFC3986].  This excludes characters
   		// such as "]" or even "%" that would complicate parsing.  However, the
   		// syntax described below does allow such characters to be percent-
   		// encoded, for compatibility with existing devices that use them.
```
