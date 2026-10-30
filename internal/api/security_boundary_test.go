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
		{"POST", "/api/v1/auth"},
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

func TestAuthTokenGateProtectsDashboardAPI(t *testing.T) {
	const token = "install-token-0123456789abcdef0123456789abcdef"
	fx := newFixture(t, Config{UserHome: t.TempDir(), AuthToken: token})
	handler := fx.server.Handler()

	do := func(method, path string, headers map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://127.0.0.1"+path, nil)
		req.RemoteAddr = "127.0.0.1:54321"
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// Health stays reachable for local diagnostics.
	if rec := do(http.MethodGet, "/api/v1/health", nil); rec.Code != http.StatusOK {
		t.Fatalf("health without token = %d body=%s", rec.Code, rec.Body.String())
	}

	// Every other API route requires the install token.
	for _, path := range []string{"/api/v1/session", "/api/v1/sites", "/api/v1/services"} {
		if rec := do(http.MethodGet, path, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("%s without token = %d body=%s", path, rec.Code, rec.Body.String())
		}
	}
	if rec := do(http.MethodGet, "/api/v1/session", map[string]string{"X-Devstack-Token": "wrong"}); rec.Code != http.StatusForbidden {
		t.Fatalf("session with wrong token = %d body=%s", rec.Code, rec.Body.String())
	}

	// The token header bootstraps an HttpOnly auth cookie.
	rec := do(http.MethodPost, "/api/v1/auth", map[string]string{"X-Devstack-Token": token})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("auth bootstrap = %d body=%s", rec.Code, rec.Body.String())
	}
	var authCookie *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == authCookieName {
			authCookie = cookie
			break
		}
	}
	if authCookie == nil || !authCookie.HttpOnly || authCookie.Value != token {
		t.Fatalf("auth cookie = %+v", authCookie)
	}

	if rec := do(http.MethodGet, "/api/v1/session", nil, authCookie); rec.Code != http.StatusOK {
		t.Fatalf("session with auth cookie = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodGet, "/api/v1/sites", nil, authCookie); rec.Code != http.StatusOK {
		t.Fatalf("sites with auth cookie = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodGet, "/api/v1/sites", map[string]string{"X-Devstack-Token": token}); rec.Code != http.StatusOK {
		t.Fatalf("sites with token header = %d body=%s", rec.Code, rec.Body.String())
	}

	// The SID-authenticated control pipe does not need the browser token.
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/v1/sites", nil)
	req.RemoteAddr = "172.25.48.1:54321"
	req = req.WithContext(context.WithValue(req.Context(), socketAuthKey{}, true))
	socketRec := httptest.NewRecorder()
	handler.ServeHTTP(socketRec, req)
	if socketRec.Code != http.StatusOK {
		t.Fatalf("socket-authenticated sites = %d body=%s", socketRec.Code, socketRec.Body.String())
	}
}

func TestEveryAPIRouteRequiresTheInstallToken(t *testing.T) {
	const token = "install-token-route-coverage-0123456789abcdef"
	fx := newFixture(t, Config{UserHome: t.TempDir(), AuthToken: token})
	handler := fx.server.Handler()
	replace := strings.NewReplacer(
		"{ref}", "probe", "{id}", "probe", "{version}", "8.4",
		"{name}", "probe", "{userID}", "probe", "{backupID}", "probe",
		"{jobID}", "probe", "{linkID}", "probe",
	)

	for _, route := range fx.server.apiRoutes() {
		path := replace.Replace(route.pattern)
		req := httptest.NewRequest(route.method, "http://127.0.0.1"+path, nil)
		req.RemoteAddr = "127.0.0.1:54321"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if route.pattern == "/api/v1/health" {
			if rec.Code != http.StatusOK {
				t.Errorf("%s %s = %d, want health to stay reachable", route.method, path, rec.Code)
			}
			continue
		}
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s without token = %d, want 403", route.method, path, rec.Code)
		}
	}

	// The token is only accepted as the header or the HttpOnly cookie, never a
	// query parameter that would leak into logs and Referer headers.
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/v1/sites?token="+token, nil)
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("query-string token = %d, want 403", rec.Code)
	}

	// The WebSocket route is a GET, but it is still behind the install-token
	// gate; a ticket alone is not enough to reach the upgrade handler.
	req = httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/v1/sites/probe/terminal?ticket=one", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("Origin", "http://127.0.0.1:7070")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("terminal WebSocket without token = %d, want 403", rec.Code)
	}
	req.Header.Set(authTokenHeader, token)
	req.URL.RawQuery = ""
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("terminal WebSocket with token and no ticket = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestAuthBootstrapRejectsWrongTokenAndSetsSecureCookie(t *testing.T) {
	const token = "secure-install-token-0123456789abcdef"
	fx := newFixture(t, Config{UserHome: t.TempDir(), AuthToken: token})
	handler := fx.server.Handler()

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/v1/auth", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Devstack-Token", "wrong")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong bootstrap token = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatalf("wrong bootstrap set cookies: %+v", rec.Result().Cookies())
	}

	req = httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/v1/auth", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Devstack-Token", token)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("bootstrap = %d body=%s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != authCookieName || !cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatalf("bootstrap cookie = %+v", cookies)
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

func TestAPILoopbackBypassViaForwardingHeaders(t *testing.T) {
	_, fx := newTestAPIWithFakes(t)
	handler := fx.server.Handler()

	bypassAttempts := []struct {
		name       string
		remoteAddr string
		headers    map[string][]string
	}{
		{
			name:       "multi-value X-Forwarded-For header",
			remoteAddr: "127.0.0.1:12345",
			headers:    map[string][]string{"X-Forwarded-For": {"127.0.0.1, 198.51.100.1"}},
		},
		{
			name:       "multiple X-Forwarded-For headers",
			remoteAddr: "127.0.0.1:12345",
			headers:    map[string][]string{"X-Forwarded-For": {"127.0.0.1", "198.51.100.1"}},
		},
		{
			name:       "single X-Real-IP external",
			remoteAddr: "127.0.0.1:12345",
			headers:    map[string][]string{"X-Real-IP": {"203.0.113.1"}},
		},
		{
			name:       "multiple X-Real-IP headers",
			remoteAddr: "127.0.0.1:12345",
			headers:    map[string][]string{"X-Real-IP": {"127.0.0.1", "203.0.113.1"}},
		},
		{
			name:       "Forwarded RFC 7239 external node",
			remoteAddr: "127.0.0.1:12345",
			headers:    map[string][]string{"Forwarded": {"for=203.0.113.195;proto=http"}},
		},
		{
			name:       "Forwarded RFC 7239 loopback and external",
			remoteAddr: "127.0.0.1:12345",
			headers:    map[string][]string{"Forwarded": {"for=127.0.0.1", "for=203.0.113.1"}},
		},
	}

	for _, tc := range bypassAttempts {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://127.0.0.1/api/v1/session", nil)
			req.RemoteAddr = tc.remoteAddr
			for k, values := range tc.headers {
				for _, v := range values {
					req.Header.Add(k, v)
				}
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("expected 403 Forbidden for %s, got %d (body: %s)", tc.name, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAPISessionFixationProtection(t *testing.T) {
	_, fx := newTestAPIWithFakes(t)
	handler := fx.server.Handler()

	invalidSessions := []string{
		"short",
		"attacker_chosen_session_id",
		"0123456789abcdef", // too short (16 hex chars)
		"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",  // 64 chars, but non-hex 'z'
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef ", // trailing space
		"../escaped/session",
	}

	for _, invalid := range invalidSessions {
		t.Run("session_endpoint_rejects_and_reissues_"+invalid, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://127.0.0.1/api/v1/session", nil)
			req.RemoteAddr = "127.0.0.1:12345"
			req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: invalid})
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("session code = %d, want 200", rec.Code)
			}
			// When client provides an invalid session, handleSession must issue a brand-new valid cookie
			cookies := rec.Result().Cookies()
			var issued *http.Cookie
			for _, c := range cookies {
				if c.Name == sessionCookieName {
					issued = c
					break
				}
			}
			if issued == nil {
				t.Fatal("expected handleSession to re-issue a clean cookie for invalid session input")
			}
			if issued.Value == invalid {
				t.Fatalf("session fixation: server retained attacker-provided invalid session %q", invalid)
			}
			if !isValidSessionValue(issued.Value) {
				t.Fatalf("re-issued session %q is not valid", issued.Value)
			}
		})

		t.Run("authenticate_rejects_invalid_session_"+invalid, func(t *testing.T) {
			req := httptest.NewRequest("POST", "http://127.0.0.1/api/v1/services/mysql/activate", nil)
			req.RemoteAddr = "127.0.0.1:12345"
			req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: invalid})
			req.Header.Set(csrfHeader, fx.server.csrfFor(invalid))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("expected 403 Forbidden for invalid session %q, got %d", invalid, rec.Code)
			}
		})
	}
}

func TestAPIHostHeaderInjectionRejection(t *testing.T) {
	_, fx := newTestAPIWithFakes(t)
	handler := fx.server.Handler()

	maliciousHosts := []string{
		"127.0.0.1\r\nInjected: evil",
		"localhost\nInjected: evil",
		"127.0.0.1/api",
		"localhost\\path",
		"127.0.0.1#frag",
		"localhost?query",
	}

	for _, h := range maliciousHosts {
		t.Run(h, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://127.0.0.1/api/v1/health", nil)
			req.Host = h
			req.RemoteAddr = "127.0.0.1:12345"
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("expected 403 Forbidden for malicious host %q, got %d", h, rec.Code)
			}
		})
	}
}
