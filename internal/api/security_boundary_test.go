package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityBoundaryRejectsNonLoopbackControlAPI(t *testing.T) {
	_, fx := newTestAPIWithFakes(t)
	// Use fx.server.Handler() directly to test production middleware without testLoopbackHandler adapter.
	handler := fx.server.Handler()

	nonLoopbackAddrs := []string{
		"172.25.48.1:54321",  // WSL NAT host gateway
		"172.25.48.2:54321",  // WSL distro IP
		"192.168.1.50:54321", // LAN client
		"10.0.0.1:54321",     // Private subnet
		"198.51.100.1:1234",  // External IP
	}

	endpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/session"},
		{"GET", "/api/v1/sites"},
		{"GET", "/api/v1/health"},
		{"POST", "/api/v1/sites"},
		{"GET", "/api/v1/services"},
		{"GET", "/api/v1/fs"},
	}

	for _, addr := range nonLoopbackAddrs {
		for _, ep := range endpoints {
			req := httptest.NewRequest(ep.method, "http://127.0.0.1"+ep.path, strings.NewReader(`{}`))
			req.RemoteAddr = addr
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("expected 403 Forbidden for non-loopback %s %s from %s, got %d (body: %s)",
					ep.method, ep.path, addr, rec.Code, rec.Body.String())
			}
		}
	}
}

func TestSecurityBoundaryRejectsSpoofedForwardedFor(t *testing.T) {
	_, fx := newTestAPIWithFakes(t)
	handler := fx.server.Handler()

	// Direct loopback connection, but X-Forwarded-For indicates non-loopback origin
	req := httptest.NewRequest("GET", "http://127.0.0.1/api/v1/session", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "172.25.48.1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for external X-Forwarded-For, got %d", rec.Code)
	}

	// X-Real-IP indicates non-loopback origin
	req = httptest.NewRequest("GET", "http://127.0.0.1/api/v1/session", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Real-IP", "192.168.1.100")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for external X-Real-IP, got %d", rec.Code)
	}
}

func TestSecurityBoundaryAllowsLoopbackAndSocketAuth(t *testing.T) {
	_, fx := newTestAPIWithFakes(t)
	handler := fx.server.Handler()

	// IPv4 loopback
	req := httptest.NewRequest("GET", "http://127.0.0.1/api/v1/session", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("IPv4 loopback expected 200 OK, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	// IPv6 loopback
	req = httptest.NewRequest("GET", "http://127.0.0.1/api/v1/session", nil)
	req.RemoteAddr = "[::1]:12345"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("IPv6 loopback expected 200 OK, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Socket authenticated request bypasses RemoteAddr check
	req = httptest.NewRequest("GET", "http://127.0.0.1/api/v1/session", nil)
	req.RemoteAddr = "172.25.48.1:54321" // external IP
	ctx := context.WithValue(req.Context(), socketAuthKey{}, true)
	req = req.WithContext(ctx)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("socket-authenticated expected 200 OK, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestSecurityBoundarySessionTLSRequirement(t *testing.T) {
	// Configure server with HTTPS port bound
	cfg := Config{
		Listeners: ListenerPorts{
			HTTPPort:  80,
			HTTPSPort: 443,
			UIPort:    7070,
		},
	}
	fxTLS := newFixture(t, cfg)
	handler := fxTLS.server.Handler()

	// Plaintext HTTP to devstack.test must be rejected when HTTPS is expected
	req := httptest.NewRequest("GET", "http://devstack.test/api/v1/session", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for plaintext HTTP devstack.test session, got %d", rec.Code)
	}

	// HTTPS to devstack.test must succeed and issue Secure cookie
	req = httptest.NewRequest("GET", "https://devstack.test/api/v1/session", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for HTTPS devstack.test session, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	cookie := rec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookie {
		if c.Name == sessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("session cookie not found in response")
	}
	if !sessionCookie.Secure {
		t.Fatal("session cookie must have Secure=true over HTTPS")
	}

	// Plaintext HTTP to 127.0.0.1 (local UI port) is allowed
	req = httptest.NewRequest("GET", "http://127.0.0.1/api/v1/session", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for local 127.0.0.1 session, got %d", rec.Code)
	}
}
