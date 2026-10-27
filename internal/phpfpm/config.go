// Package phpfpm renders and validates the php-fpm configuration used by
// devstack's per-site FPM masters.
//
// Every PHP site gets one master listening on a loopback TCP port in the
// backend range. The rendered file is deliberately small: one global section
// that keeps FPM in the foreground and logs to the site stream, and one pool
// with fixed sizing, the site document root, and the site's environment.
package phpfpm

import (
	"errors"
	"fmt"
	"net"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

// ErrConfigInvalid is the sentinel wrapped by every configuration and
// validation failure in this package.
var ErrConfigInvalid = errors.New("phpfpm: invalid configuration")

// Backend FastCGI port range reserved for PHP sites.
const (
	portMin = 43000
	portMax = 43999
)

var (
	userPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	envPattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Config describes one per-site php-fpm master. Paths must be absolute and
// clean; Env entries become pool environment variables.
type Config struct {
	SiteID       string
	BuildRoot    string
	PHPIni       string
	ListenHost   string
	ListenPort   int
	DocumentRoot string
	ErrorLog     string
	PIDFile      string
	User, Group  string
	Env          map[string]string
	// Pool is the persisted per-site sizing. Nil or zero selects the
	// conservative defaults.
	Pool *model.PHPFPMBudget
}

// ConfigError reports one rejected field or value. It wraps ErrConfigInvalid.
type ConfigError struct {
	Field   string
	Message string
}

// Error implements error.
func (e *ConfigError) Error() string {
	return fmt.Sprintf("phpfpm: %s: %s", e.Field, e.Message)
}

// Unwrap makes every configuration failure match ErrConfigInvalid.
func (e *ConfigError) Unwrap() error { return ErrConfigInvalid }

func invalid(field, format string, args ...any) error {
	return &ConfigError{Field: field, Message: fmt.Sprintf(format, args...)}
}

// validateConfig checks every value before any bytes are rendered. Paths are
// absolute and already clean, listen hosts are literal IP addresses, the port
// is inside the PHP backend range, and env names are shell-safe.
func validateConfig(cfg Config) error {
	if err := validateText("site id", cfg.SiteID); err != nil {
		return err
	}
	for _, path := range []struct {
		field string
		value string
	}{
		{"build root", cfg.BuildRoot},
		{"php.ini", cfg.PHPIni},
		{"document root", cfg.DocumentRoot},
		{"error log", cfg.ErrorLog},
		{"pid file", cfg.PIDFile},
	} {
		if err := validatePath(path.field, path.value); err != nil {
			return err
		}
	}

	if cfg.ListenHost == "" {
		return invalid("listen host", "is required")
	}
	if strings.ContainsAny(cfg.ListenHost, "\x00\r\n") {
		return invalid("listen host", "contains a NUL, carriage return, or newline")
	}
	if ip := net.ParseIP(cfg.ListenHost); ip == nil {
		return invalid("listen host", "%q is not an IP address", cfg.ListenHost)
	}
	if cfg.ListenPort < portMin || cfg.ListenPort > portMax {
		return invalid("listen port", "%d is outside %d-%d", cfg.ListenPort, portMin, portMax)
	}

	if cfg.User != "" {
		if err := validateAccount("user", cfg.User); err != nil {
			return err
		}
	}
	if cfg.Group != "" {
		if err := validateAccount("group", cfg.Group); err != nil {
			return err
		}
	}
	if cfg.Pool != nil {
		if err := cfg.Pool.Validate(); err != nil {
			return invalid("pool", "%v", err)
		}
	}

	keys := make([]string, 0, len(cfg.Env))
	for key := range cfg.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !envPattern.MatchString(key) {
			return invalid("env", "%q is not a valid environment variable name", key)
		}
		if err := validateText(fmt.Sprintf("env[%s]", key), cfg.Env[key]); err != nil {
			return err
		}
	}
	return nil
}

// validateText rejects values that would break a line or inject directives.
// Control characters are not needed in any rendered value.
func validateText(field, value string) error {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return invalid(field, "contains a control character")
		}
	}
	return nil
}

// validatePath requires an absolute, clean path. Rejecting relative and
// unclean paths keeps the generated file independent of the daemon's working
// directory and prevents ambiguous FPM parsing.
func validatePath(field, value string) error {
	if value == "" {
		return invalid(field, "is required")
	}
	if err := validateText(field, value); err != nil {
		return err
	}
	isAbs, isClean := isAbsPOSIXPath(value)
	if !isAbs {
		return invalid(field, "%q is not an absolute path", value)
	}
	if !isClean {
		return invalid(field, "%q is not a clean path", value)
	}
	return nil
}

func isAbsPOSIXPath(p string) (isAbs, isClean bool) {
	if strings.HasPrefix(p, "/") {
		return true, path.Clean(p) == p
	}
	return filepath.IsAbs(p), filepath.Clean(p) == p
}

func validateAccount(field, value string) error {
	if err := validateText(field, value); err != nil {
		return err
	}
	if !userPattern.MatchString(value) {
		return invalid(field, "%q contains invalid characters", value)
	}
	return nil
}
