package performance

import (
	"net"
	"strings"
)

// PublicRouteURL formats a site domain into an absolute https://<domain>/ public route URL.
func PublicRouteURL(domain string) string {
	domain = strings.TrimSpace(domain)
	if !strings.HasPrefix(domain, "http://") && !strings.HasPrefix(domain, "https://") {
		domain = "https://" + domain
	}
	if !strings.HasSuffix(domain, "/") {
		domain += "/"
	}
	return domain
}

// IsLocalOrTestHost reports whether a host is loopback or a local .test domain.
func IsLocalOrTestHost(host string) bool {
	host = strings.TrimSpace(host)
	if strings.Contains(host, ":") {
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
	}
	if strings.EqualFold(host, "localhost") || isTestHost(host) {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isTestHost reports whether host is the reserved .test namespace used by
// devstack sites. The trailing dot form is accepted.
func isTestHost(host string) bool {
	lower := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	return lower == "test" || strings.HasSuffix(lower, ".test")
}
