package api

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// defaultAllowedHosts are the only hostnames the loopback dashboard answers
// on. Ports are ignored during comparison.
var defaultAllowedHosts = []string{"127.0.0.1", "localhost", "devstack.test"}

// socketAuthKey marks requests that arrived over the Unix socket and were
// authenticated by peer credentials.
type socketAuthKey struct{}

// WithSocketAuth marks every request as authenticated by the Unix-socket
// transport. Such requests skip the browser session cookie and CSRF check;
// the host allowlist still applies because it wraps this handler.
func WithSocketAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), socketAuthKey{}, true)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// socketAuthenticated reports whether WithSocketAuth marked the request.
func socketAuthenticated(ctx context.Context) bool {
	value, _ := ctx.Value(socketAuthKey{}).(bool)
	return value
}

// securityHeaders sets conservative defaults on every response.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// hostGuard rejects requests whose Host is not in the allowlist. It runs
// before authentication, routing, and static hosting.
func (s *Server) hostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, ok := requestHost(r.Host)
		if !ok {
			writeError(w, http.StatusForbidden, codeForbidden, "request host is not allowed", nil)
			return
		}
		if _, ok := s.allowedHosts[host]; !ok {
			writeError(w, http.StatusForbidden, codeForbidden, "request host is not allowed", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requestHost extracts and validates the hostname from a Host header. Any
// numeric port is accepted; an invalid port, empty host, or bare hostile value
// is rejected.
func requestHost(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}

	host := raw
	if parsedHost, port, err := net.SplitHostPort(raw); err == nil {
		host = parsedHost
		if port != "" {
			number, convErr := strconv.Atoi(port)
			if convErr != nil || number < 0 || number > 65535 {
				return "", false
			}
		}
	} else if strings.HasPrefix(raw, "[") {
		// A bracketed IPv6 literal without a port.
		if !strings.HasSuffix(raw, "]") {
			return "", false
		}
		host = raw[1 : len(raw)-1]
	} else if strings.Count(raw, ":") == 1 {
		// Something that looks like host:port but does not parse (for example
		// "localhost:bad"). Reject it rather than accepting the host part.
		return "", false
	}

	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return "", false
	}
	return host, true
}

// normalizeAllowedHosts builds the allowlist. An empty configuration uses the
// loopback defaults.
func normalizeAllowedHosts(configured []string) map[string]struct{} {
	values := configured
	if len(values) == 0 {
		values = defaultAllowedHosts
	}
	allowed := make(map[string]struct{}, len(values))
	for _, raw := range values {
		if host, ok := requestHost(raw); ok {
			allowed[host] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		for _, raw := range defaultAllowedHosts {
			allowed[raw] = struct{}{}
		}
	}
	return allowed
}

// authenticate enforces the browser session and CSRF rules for API routes.
// Health, session, and UI hosting are exempt; socket-authenticated requests
// bypass the cookie/CSRF checks entirely.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAPIPath(r.URL.Path) || socketAuthenticated(r.Context()) || isAuthExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || strings.TrimSpace(cookie.Value) == "" {
			if isMutation(r.Method) {
				// The brief requires a mutation without a session to be a 403,
				// matching the CSRF rejection path.
				writeError(w, http.StatusForbidden, codeForbidden, "missing dashboard session", nil)
			} else {
				writeError(w, http.StatusUnauthorized, codeUnauthorized, "a dashboard session is required", nil)
			}
			return
		}

		if isMutation(r.Method) && !s.validCSRF(cookie.Value, r.Header.Get(csrfHeader)) {
			writeError(w, http.StatusForbidden, codeForbidden, "missing or invalid CSRF token", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bodyLimit caps request bodies on API mutations before handlers decode them.
// It runs after authentication so unauthenticated requests fail as 401/403
// rather than 413.
func (s *Server) bodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAPIPath(r.URL.Path) || !isMutation(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		if s.cfg.MaxBodyBytes > 0 && r.ContentLength > s.cfg.MaxBodyBytes {
			writeError(w, http.StatusRequestEntityTooLarge, codePayloadTooLarge, "request body is too large", nil)
			return
		}
		if s.cfg.MaxBodyBytes > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// isAPIPath reports whether path belongs to the /api/v1 subtree.
func isAPIPath(value string) bool {
	return value == "/api/v1" || strings.HasPrefix(value, "/api/v1/")
}

// isAuthExempt reports the two API routes that establish health and sessions.
func isAuthExempt(value string) bool {
	return value == "/api/v1/health" || value == "/api/v1/session"
}

// isMutation reports whether a method requires CSRF protection.
func isMutation(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	default:
		return true
	}
}
