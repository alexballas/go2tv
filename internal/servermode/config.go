package servermode

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go2tv.app/go2tv/v2/internal/pathutil"
)

const DefaultListen = "127.0.0.1:9666"

var (
	ErrAllowedOriginRequired = errors.New("non-loopback listen requires -allowed-origin")
	ErrInvalidListen         = errors.New("invalid listen address")
	ErrInvalidOrigin         = errors.New("invalid allowed origin")
	ErrInvalidMediaRoot      = errors.New("invalid media root")
	ErrServerFlagConflict    = errors.New("-server conflicts with CLI flags")
	ErrServerFlagWithoutMode = errors.New("server-only flag requires -server")
	ErrPositionalArguments   = errors.New("positional arguments not allowed")
)

type Strings []string

func (s *Strings) String() string { return strings.Join(*s, ",") }

func (s *Strings) Set(value string) error {
	*s = append(*s, value)
	return nil
}

type Config struct {
	Listen         string
	MediaRoots     []string
	AllowedOrigins []string
	Version        string
	Debug          bool
	// ManagedChild marks a GUI-managed child run: discovery arrives over the
	// parent stdin pipe and managed event frames are emitted on stdout.
	ManagedChild bool
}

type CLIOptions struct {
	Server         bool
	Listen         string
	Debug          bool
	MediaRoots     Strings
	AllowedOrigins Strings
	// ManagedChild is bound to the hidden -managed-child flag, which only the
	// desktop go2tv binary registers.
	ManagedChild bool
}

func RegisterCLIFlags(flags *flag.FlagSet) *CLIOptions {
	options := &CLIOptions{}
	flags.BoolVar(&options.Server, "server", false, "Run Web server mode.")
	flags.StringVar(&options.Listen, "listen", DefaultListen, "Web server listen address.")
	flags.BoolVar(&options.Debug, "debug", false, "Enable Web server protocol debug logs.")
	flags.Var(&options.MediaRoots, "media-root", "Allowed media directory (repeatable; required with -server).")
	flags.Var(&options.AllowedOrigins, "allowed-origin", "Allowed Web origin, including scheme/host/port (repeatable).")
	return options
}

func (o *CLIOptions) Validate(flags *flag.FlagSet) error {
	var legacy []string
	serverOptionSet := false
	flags.Visit(func(visited *flag.Flag) {
		switch visited.Name {
		case "server":
		case "listen", "debug", "media-root", "allowed-origin", "managed-child":
			serverOptionSet = true
		default:
			legacy = append(legacy, "-"+visited.Name)
		}
	})
	return ValidateCLI(o.Server, serverOptionSet, o.MediaRoots, o.AllowedOrigins, legacy, flags.Args())
}

func (o *CLIOptions) Config(version string) Config {
	return Config{
		Listen:         o.Listen,
		MediaRoots:     o.MediaRoots,
		AllowedOrigins: o.AllowedOrigins,
		Version:        version,
		Debug:          o.Debug,
		ManagedChild:   o.ManagedChild,
	}
}

// ValidateCLI enforces mode separation before legacy flag processing.
func ValidateCLI(server bool, serverOptionSet bool, roots, origins []string, legacySet []string, args []string) error {
	if len(args) != 0 {
		return ErrPositionalArguments
	}
	if !server {
		if serverOptionSet || len(roots) != 0 || len(origins) != 0 {
			return ErrServerFlagWithoutMode
		}
		return nil
	}
	if len(legacySet) != 0 {
		slices.Sort(legacySet)
		return fmt.Errorf("%w: %s", ErrServerFlagConflict, strings.Join(legacySet, ", "))
	}
	if len(roots) == 0 {
		return fmt.Errorf("%w: at least one -media-root required", ErrInvalidMediaRoot)
	}
	return nil
}

func Validate(cfg Config) (Config, error) {
	if len(cfg.MediaRoots) == 0 {
		return Config{}, fmt.Errorf("%w: at least one -media-root required", ErrInvalidMediaRoot)
	}
	if cfg.Listen == "" {
		cfg.Listen = DefaultListen
	}
	host, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil || host == "" || port == "" {
		return Config{}, fmt.Errorf("%w: %q", ErrInvalidListen, cfg.Listen)
	}
	ip := net.ParseIP(host)
	if ip == nil && !strings.EqualFold(host, "localhost") {
		return Config{}, fmt.Errorf("%w: host must be IP or localhost", ErrInvalidListen)
	}
	if _, err := net.LookupPort("tcp", port); err != nil {
		return Config{}, fmt.Errorf("%w: %q", ErrInvalidListen, cfg.Listen)
	}
	cfg.Listen = net.JoinHostPort(canonicalHost(host), port)

	canonicalOrigins := make([]string, 0, len(cfg.AllowedOrigins))
	seenOrigins := make(map[string]struct{}, len(cfg.AllowedOrigins))
	for _, raw := range cfg.AllowedOrigins {
		origin, err := canonicalOrigin(raw)
		if err != nil {
			return Config{}, err
		}
		if _, exists := seenOrigins[origin]; exists {
			return Config{}, fmt.Errorf("%w: duplicate %q", ErrInvalidOrigin, origin)
		}
		seenOrigins[origin] = struct{}{}
		canonicalOrigins = append(canonicalOrigins, origin)
	}
	if !isLoopbackHost(host) && len(canonicalOrigins) == 0 {
		return Config{}, ErrAllowedOriginRequired
	}
	cfg.AllowedOrigins = canonicalOrigins

	roots := make([]string, 0, len(cfg.MediaRoots))
	for _, root := range cfg.MediaRoots {
		abs, err := filepath.Abs(root)
		if err != nil {
			return Config{}, fmt.Errorf("%w: %v", ErrInvalidMediaRoot, err)
		}
		canonical, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return Config{}, fmt.Errorf("%w: %v", ErrInvalidMediaRoot, err)
		}
		info, err := os.Stat(canonical)
		if err != nil || !info.IsDir() {
			return Config{}, fmt.Errorf("%w: %q is not a directory", ErrInvalidMediaRoot, root)
		}
		for _, existing := range roots {
			if pathutil.Overlap(existing, canonical) {
				return Config{}, fmt.Errorf("%w: duplicate/overlapping roots %q and %q", ErrInvalidMediaRoot, existing, canonical)
			}
		}
		roots = append(roots, canonical)
	}
	cfg.MediaRoots = roots
	return cfg, nil
}

func canonicalOrigin(raw string) (string, error) {
	if raw == "null" || strings.TrimSpace(raw) != raw {
		return "", fmt.Errorf("%w: %q", ErrInvalidOrigin, raw)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%w: %q", ErrInvalidOrigin, raw)
	}
	host := u.Hostname()
	port := u.Port()
	if host == "" || port == "" {
		return "", fmt.Errorf("%w: scheme, host, port required", ErrInvalidOrigin)
	}
	return strings.ToLower(u.Scheme) + "://" + net.JoinHostPort(canonicalHost(host), port), nil
}

func canonicalHost(host string) string {
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
