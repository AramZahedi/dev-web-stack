// Package performance computes bounded nginx, Apache, PHP-FPM, and host tuning
// values from measured host resources.
//
// The package is pure: it never writes files, starts containers, applies
// sysctl values, or runs benchmarks. Renderers produce deterministic include
// content, and ApplyPlan/ResetPlan describe the paths and bytes for the
// daemon and helper to apply. This keeps host tuning testable and keeps the
// privileged helper's input limited to content and fixed paths.
package performance

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalid is the sentinel wrapped by every rejected input or profile.
var ErrInvalid = errors.New("performance: invalid input")

// ValidationError reports one rejected field or value. It wraps ErrInvalid.
type ValidationError struct {
	Field   string
	Message string
}

// Error implements error.
func (e *ValidationError) Error() string {
	return fmt.Sprintf("performance: %s: %s", e.Field, e.Message)
}

// Unwrap makes every validation failure match ErrInvalid.
func (e *ValidationError) Unwrap() error { return ErrInvalid }

func invalid(field, format string, args ...any) error {
	return &ValidationError{Field: field, Message: fmt.Sprintf(format, args...)}
}

// Profile selects the tuning posture. Custom values are supplied explicitly
// and are validated against the same safety caps without being recomputed.
type Profile string

const (
	// Balanced is the conservative local-development profile.
	Balanced Profile = "balanced"
	// Throughput maximizes safe resource use for the active web server.
	Throughput Profile = "throughput"
	// Custom is a user-edited profile. Compute does not regenerate it.
	Custom Profile = "custom"
)

// Validate reports whether the profile is supported.
func (p Profile) Validate() error {
	switch p {
	case Balanced, Throughput, Custom:
		return nil
	case "":
		return invalid("profile", "is required")
	default:
		return invalid("profile", "%q is not balanced, throughput, or custom", p)
	}
}

// ParseProfile normalizes a stored profile value.
func ParseProfile(raw string) (Profile, error) {
	p := Profile(strings.ToLower(strings.TrimSpace(raw)))
	if err := p.Validate(); err != nil {
		return "", err
	}
	return p, nil
}

// Safety caps. They bound generated values on very large or very small hosts
// and are also enforced for custom profiles.
const (
	maxNginxWorkers     = 32
	minNginxConnections = 16
	maxNginxConnections = 16384
	minOpenFileCache    = 1000
	maxOpenFileCache    = 20000

	minApacheThreads     = 25
	maxApacheThreads     = 32
	maxAllowedThreads    = 64
	maxApacheServerLimit = 64
	maxApacheWorkers     = 2048

	maxPHPSites       = 256
	maxPHPPerSite     = 256
	maxPHPGlobalChild = 256

	maxBacklog = 65535
	maxFileMax = 1 << 22
	maxNoFile  = 1 << 20

	minFileMax = 64 << 10
)
