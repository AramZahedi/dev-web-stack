package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// defaultAllowedHosts are the only hostnames the loopback dashboard answers
// on. Ports are ignored during comparison. Configured hosts are merged with
// these, never substituted for them.
var defaultAllowedHosts = []string{"127.0.0.1", "localhost", "devstack.test"}

// socketAuthKey marks requests that arrived over the Unix socket and were
// authenticated by peer credentials.
type socketAuthKey struct{}

// WithSocketAuth marks every request as authenticated by the local control
// transport. The listener must already have verified the peer UID/SID (see
// internal/ipc.PeerPolicy); this middleware must never wrap a listener that
// accepts unverified local clients. Such requests skip the browser session
// cookie and CSRF check; the host allowlist still applies because it wraps
// this handler.
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
		if isAPIPath(r.URL.Path) && !isLoopbackRequest(r) {
			writeError(w, http.StatusForbidden, codeForbidden, "access denied: control API is loopback only", nil)
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

// normalizeAllowedHosts builds the allowlist. The required loopback hosts are
// always present; configured hosts are additive, never a replacement.
func normalizeAllowedHosts(configured []string) map[string]struct{} {
	allowed := make(map[string]struct{}, len(defaultAllowedHosts)+len(configured))
	for _, raw := range defaultAllowedHosts {
		if host, ok := requestHost(raw); ok {
			allowed[host] = struct{}{}
		}
	}
	for _, raw := range configured {
		if host, ok := requestHost(raw); ok {
			allowed[host] = struct{}{}
		}
	}
	return allowed
}

// authenticate enforces the mutation session and CSRF rules for API routes.
//
// Posture: the control API is loopback-only and every request has already
// passed the Host allowlist. Non-loopback callers are rejected with 403.
// Read-only API requests (including the SSE endpoints) are allowed without a
// session cookie. Mutations require the session cookie and the exact CSRF HMAC
// for that session. Socket-authenticated CLI requests bypass the cookie/CSRF
// checks but still receive the Host guard. Health, session, and UI hosting are
// exempt from the session cookie, but API routes still require loopback access.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAPIPath(r.URL.Path) && !isLoopbackRequest(r) {
			writeError(w, http.StatusForbidden, codeForbidden, "access denied: control API is loopback only", nil)
			return
		}

		if !isAPIPath(r.URL.Path) || socketAuthenticated(r.Context()) || isAuthExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		// The filesystem browser is a privileged read: it discloses directory
		// names outside the project. Require the dashboard session even though
		// other GET endpoints intentionally allow the first /session call.
		if r.URL.Path == "/api/v1/fs" || strings.HasSuffix(r.URL.Path, "/config/content") {
			cookie, err := r.Cookie(sessionCookieName)
			if err != nil || strings.TrimSpace(cookie.Value) == "" {
				writeError(w, http.StatusForbidden, codeForbidden, "missing dashboard session", nil)
				return
			}
		}

		// Read-only API requests and SSE streams intentionally do not need a
		// session. This avoids racing the dashboard's first GET /session call.
		if !isMutation(r.Method) {
			next.ServeHTTP(w, r)
			return
		}

		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || strings.TrimSpace(cookie.Value) == "" {
			// A mutation without a session is a CSRF-style rejection.
			writeError(w, http.StatusForbidden, codeForbidden, "missing dashboard session", nil)
			return
		}
		if !s.validCSRF(cookie.Value, r.Header.Get(csrfHeader)) {
			writeError(w, http.StatusForbidden, codeForbidden, "missing or invalid CSRF token", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bodyLimit caps request bodies on API mutations before handlers decode them.
// It runs after authentication so unauthenticated requests fail as 403 rather
// than 413.
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

// apiMethodErrors converts net/http's plain-text 405 for API method
// mismatches into the stable JSON error envelope. It sits directly around the
// mux so it never interferes with authentication or SSE writes.
func (s *Server) apiMethodErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAPIPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		writer := &methodErrorWriter{ResponseWriter: w}
		next.ServeHTTP(writer, r)
		if writer.intercepted {
			body, err := json.Marshal(apiError{
				Code:    codeMethodNotAllowed,
				Message: "method not allowed",
				Details: nil,
			})
			if err == nil {
				_, _ = w.Write(body)
			}
		}
	})
}

// methodErrorWriter swallows the plain-text body net/http writes for a 405 and
// gives the surrounding middleware a chance to replace it with JSON.
type methodErrorWriter struct {
	http.ResponseWriter
	intercepted bool
	wroteHeader bool
}

func (w *methodErrorWriter) WriteHeader(status int) {
	if status == http.StatusMethodNotAllowed && !w.wroteHeader {
		w.intercepted = true
		w.wroteHeader = true
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *methodErrorWriter) Write(p []byte) (int, error) {
	if w.intercepted {
		return len(p), nil
	}
	w.wroteHeader = true
	return w.ResponseWriter.Write(p)
}

func (w *methodErrorWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *methodErrorWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
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

// isLoopbackRequest reports whether r originated from a local loopback interface
// or was authenticated over the local Unix socket / Windows named pipe.
func isLoopbackRequest(r *http.Request) bool {
	if socketAuthenticated(r.Context()) {
		return true
	}
	if !isLoopbackAddress(r.RemoteAddr) {
		return false
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		for _, part := range strings.Split(xff, ",") {
			part = strings.TrimSpace(part)
			if part != "" && !isLoopbackAddress(part) {
				return false
			}
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		if !isLoopbackAddress(xri) {
			return false
		}
	}
	return true
}

// isLoopbackAddress reports whether addr (host:port or bare IP) resolves to
// IPv4 127.0.0.0/8, IPv6 ::1, or "localhost".
func isLoopbackAddress(addr string) bool {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return false
	}
	host := addr
	if parsedHost, _, err := net.SplitHostPort(addr); err == nil {
		host = parsedHost
	}
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}
