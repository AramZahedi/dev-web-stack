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
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".test") || lower == "test" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
