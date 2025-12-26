---
trigger: always_on
---

# AGENTS.md

This document contains guidelines for agentic coding agents working in this Go2TV codebase.

- In all interactions and commit messages, be extremely concise and sacrifice grammar for the sake of concision.

## Build & Development Commands

### Core Commands
- `make build` - Build the main Go2TV binary to build/go2tv
- `make test` - Run all tests with verbose output (`go test -v ./...`)
- `make run` - Build and run the application
- `make clean` - Clean build artifacts
- `go test -v ./path/to/package` - Run tests for a specific package
- `go test -run TestFunctionName -v ./path/to/package` - Run a single test

### Testing
- Tests are located in `*_test.go` files throughout the codebase
- Use table-driven tests with struct-based test cases
- Test files include: httphandlers_test.go, soapbuilders_test.go, dlnatools_test.go, etc.

## Code Style Guidelines

### Imports & Formatting
- Use standard Go formatting (`gofmt`)
- Group imports: stdlib, third-party, internal packages (separated by blank lines)
- Use full import paths (e.g., `github.com/alexballas/go2tv/soapcalls/utils`)

### Naming Conventions
- PascalCase for exported types, functions, constants
- camelCase for unexported items
- Use descriptive names: `TVPayload`, `BuildContentFeatures`, `FyneScreen`
- Error variables: `ErrSomething` (e.g., `ErrInvalidSeekFlag`)

### Types & Functions
- Define structs for XML/SOAP message structures with XML tags
- Use builder pattern for complex constructions (e.g., `setAVTransportSoapBuild`)
- Return errors with context using `fmt.Errorf("function: %w", err)`

### Error Handling
- Always handle returned errors
- Wrap errors with context about where they occurred
- Define package-level error variables for common error cases

### Testing Patterns
- Use `t.Run()` for subtests with descriptive names
- Table-driven tests with `tt := []struct{...}` pattern
- Use `t.Fatalf()` for fatal test failures
- Test both success and error paths

### Platform-specific Code
- Use build tags for platform-specific code (e.g., `//go:build !(android || ios)`)
- Windows-specific files end with `_windows.go`
- Use appropriate build tags for mobile vs desktop implementations

### Constants & Configuration
- Define constants for magic strings and numbers
- Use maps for profile/configuration mappings (e.g., `dlnaprofiles`)
- Package-level constants for DLNA flags and protocols

### XML/SOAP Handling
- Define explicit struct types for all XML/SOAP messages
- Use XML struct tags properly
- Handle XML entity escaping for Samsung TV compatibility
- Use `xml.Marshal` with proper error handling

### Plans
- At the end of each plan, give me a list of unresolved questions to answer, if any. Make the questions extremely concise. Sacrifice grammar for the sake of concision.

