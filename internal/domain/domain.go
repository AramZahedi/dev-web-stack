// Package domain validates project names and local domains and allocates the
// loopback backend ports used by site upstreams and FastCGI pools.
package domain

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

const (
	// domainSuffix is the reserved local TLD for managed sites.
	domainSuffix = ".test"
	// reservedDomain is served by the dashboard and must never be assigned to a
	// site.
	reservedDomain = "devstack.test"

	maxLabelLen  = 63
	maxDomainLen = 253
)

// NormalizeName lowercases a project name and validates it as a single DNS
// label: ASCII letters, digits, and hyphens, with no leading or trailing
// hyphen. Whitespace and path separators are rejected rather than trimmed so a
// name can never escape the sites directory.
func NormalizeName(s string) (string, error) {
	name := strings.ToLower(s)
	if strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("project name %q must not contain path separators", s)
	}
	for _, r := range name {
		if unicode.IsSpace(r) {
			return "", fmt.Errorf("project name %q must not contain whitespace", s)
		}
	}
	if err := validateLabel(name); err != nil {
		return "", fmt.Errorf("invalid project name %q: %w", s, err)
	}
	return name, nil
}

// DomainFor returns the default domain for a project name.
func DomainFor(name string) string {
	return name + domainSuffix
}

// ValidateDomain checks that d is a valid lowercase domain ending in .test.
// Every label must be 1-63 ASCII alphanumeric or hyphen characters without a
// leading or trailing hyphen, and the whole domain must not exceed 253
// characters. devstack.test is accepted here because the dashboard uses it;
// use ValidateSiteDomain when assigning a domain to a site.
func ValidateDomain(d string) error {
	if d == "" {
		return errors.New("domain is empty")
	}
	if len(d) > maxDomainLen {
		return fmt.Errorf("domain %q is longer than %d characters", d, maxDomainLen)
	}
	if !strings.HasSuffix(d, domainSuffix) {
		return fmt.Errorf("domain %q must end in %s", d, domainSuffix)
	}
	if d == domainSuffix {
		return fmt.Errorf("domain %q must have a label before %s", d, domainSuffix)
	}
	for _, label := range strings.Split(d, ".") {
		if err := validateLabel(label); err != nil {
			return fmt.Errorf("domain %q: %w", d, err)
		}
	}
	return nil
}

// ValidateSiteDomain validates a site domain and rejects the dashboard's
// reserved domain.
func ValidateSiteDomain(d string) error {
	if err := ValidateDomain(d); err != nil {
		return err
	}
	if d == reservedDomain {
		return fmt.Errorf("domain %q is reserved for the dashboard", d)
	}
	return nil
}

// ValidateProjectPath checks that p is an absolute, existing, readable
// directory and returns its symlink-resolved path. The resolved path is what
// callers should persist so later operations act on the real directory.
func ValidateProjectPath(p string) (string, error) {
	if p == "" {
		return "", errors.New("project path is empty")
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("project path %q must be absolute", p)
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("resolve project path %q: %w", p, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat project path %q: %w", p, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project path %q is not a directory", p)
	}
	if err := checkReadableDir(resolved); err != nil {
		return "", fmt.Errorf("project path %q %w", p, err)
	}
	return resolved, nil
}

// validateLabel checks a single DNS label: ASCII alphanumerics and hyphens,
// 1-63 characters, and no leading or trailing hyphen.
func validateLabel(label string) error {
	if label == "" {
		return errors.New("label is empty")
	}
	if len(label) > maxLabelLen {
		return fmt.Errorf("label %q is longer than %d characters", label, maxLabelLen)
	}
	for i := 0; i < len(label); i++ {
		c := label[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
		default:
			return fmt.Errorf("label %q contains invalid character %q", label, string(c))
		}
	}
	if label[0] == '-' || label[len(label)-1] == '-' {
		return fmt.Errorf("label %q must not start or end with a hyphen", label)
	}
	return nil
}

// checkReadableDir verifies that the daemon can list the directory. Opening
// alone is not enough on platforms where read permission is checked on the
// first directory read.
func checkReadableDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("is not readable: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("is not readable: %w", err)
	}
	return nil
}
