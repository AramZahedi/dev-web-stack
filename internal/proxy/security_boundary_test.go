package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProxyRejectsNonLoopbackDashboard(t *testing.T) {
	// Set up mock upstream for dashboard
	upstreamCalled := false
	dashboardUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"upstream":"dashboard"}`))
	}))
	defer dashboardUpstream.Close()

	// Set up mock upstream for normal site
	siteUpstreamCalled := false
	siteUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		siteUpstreamCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"upstream":"site"}`))
	}))
	defer siteUpstream.Close()

	router := NewRouter()
	router.Replace(Snapshot{
		Routes: map[string]Route{
			"devstack.test": {
				SiteID:   "__dashboard__",
				Host:     "devstack.test",
				Kind:     KindStatic,
				Upstream: strings.TrimPrefix(dashboardUpstream.URL, "http://"),
				TLS:      false,
			},
			"blog.test": {
				SiteID:   "site-1",
				Host:     "blog.test",
				Kind:     KindStatic,
				Upstream: strings.TrimPrefix(siteUpstream.URL, "http://"),
				TLS:      false,
			},
		},
	})

	nonLoopbackAddrs := []string{
		"172.25.48.1:8080",   // WSL NAT host gateway
		"172.25.48.2:34567",  // WSL distro IP
		"192.168.1.100:8080", // LAN caller
		"10.0.0.5:12345",     // Private subnet
		"198.51.100.1:1234",  // Documentation / public IP
	}

	endpoints := []string{
		"/",
		"/api/v1/session",
		"/api/v1/sites",
		"/api/v1/services",
	}

	for _, addr := range nonLoopbackAddrs {
		for _, ep := range endpoints {
			upstreamCalled = false
			req := httptest.NewRequest("GET", "http://devstack.test"+ep, nil)
			req.RemoteAddr = addr
			rec := httptest.NewRecorder()
			router.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("expected 403 Forbidden for non-loopback %s from %s, got %d", ep, addr, rec.Code)
			}
			if upstreamCalled {
				t.Fatalf("upstream was called for forbidden non-loopback request from %s", addr)
			}
		}
	}

	// Normal site (blog.test) with non-loopback RemoteAddr must route successfully to upstream
	siteUpstreamCalled = false
	req := httptest.NewRequest("GET", "http://blog.test/", nil)
	req.RemoteAddr = "172.25.48.1:8080"
	rec := httptest.NewRecorder()
	router.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for normal site from WSL remote addr, got %d", rec.Code)
	}
	if !siteUpstreamCalled {
		t.Fatal("normal site upstream was not called")
	}

	// Loopback requests to devstack.test must succeed
	loopbackAddrs := []string{
		"127.0.0.1:54321",
		"[::1]:54321",
	}
	for _, addr := range loopbackAddrs {
		upstreamCalled = false
		req = httptest.NewRequest("GET", "http://devstack.test/api/v1/session", nil)
		req.RemoteAddr = addr
		rec = httptest.NewRecorder()
		router.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for loopback devstack.test from %s, got %d", addr, rec.Code)
		}
		if !upstreamCalled {
			t.Fatalf("upstream was not called for loopback devstack.test from %s", addr)
		}
	}
}

func TestProxyDashboardForceHTTPSAndNonLoopbackPrecedence(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	router := NewRouter()
	router.Replace(Snapshot{
		Routes: map[string]Route{
			"devstack.test": {
				SiteID:     "__dashboard__",
				Host:       "devstack.test",
				Kind:       KindStatic,
				Upstream:   strings.TrimPrefix(upstream.URL, "http://"),
				TLS:        true,
				ForceHTTPS: true,
			},
		},
	})

	// Non-loopback caller on HTTP must receive 403 Forbidden, NOT a redirect
	req := httptest.NewRequest("GET", "http://devstack.test/api/v1/session", nil)
	req.RemoteAddr = "172.25.48.1:8080"
	rec := httptest.NewRecorder()
	router.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for non-loopback HTTP, got %d", rec.Code)
	}

	// Loopback caller on HTTP with ForceHTTPS must receive 308 redirect to HTTPS
	req = httptest.NewRequest("GET", "http://devstack.test/api/v1/session", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rec = httptest.NewRecorder()
	router.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusPermanentRedirect {
		t.Fatalf("expected 308 Permanent Redirect for loopback HTTP with ForceHTTPS, got %d", rec.Code)
	}
	location := rec.Header().Get("Location")
	if !strings.HasPrefix(location, "https://devstack.test") {
		t.Fatalf("unexpected redirect location: %s", location)
	}
}

func TestProxyDashboardRejectsUntrustedForwardingHeaders(t *testing.T) {
	upstreamCalled := false
	dashboardUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"upstream":"dashboard"}`))
	}))
	defer dashboardUpstream.Close()

	router := NewRouter()
	router.Replace(Snapshot{
		Routes: map[string]Route{
			"devstack.test": {
				SiteID:   "__dashboard__",
				Host:     "devstack.test",
				Kind:     KindStatic,
				Upstream: strings.TrimPrefix(dashboardUpstream.URL, "http://"),
				TLS:      false,
			},
		},
	})

	testCases := []struct {
		name       string
		remoteAddr string
		headers    map[string][]string
		wantStatus int
	}{
		{
			name:       "clean loopback",
			remoteAddr: "127.0.0.1:54321",
			wantStatus: http.StatusOK,
		},
		{
			name:       "IPv4-mapped IPv6 loopback",
			remoteAddr: "[::ffff:127.0.0.1]:54321",
			wantStatus: http.StatusOK,
		},
		{
			name:       "scoped IPv6 loopback",
			remoteAddr: "[::1%lo0]:54321",
			wantStatus: http.StatusOK,
		},
		{
			name:       "single XFF with external IP",
			remoteAddr: "127.0.0.1:54321",
			headers:    map[string][]string{"X-Forwarded-For": {"203.0.113.1"}},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "multi-IP XFF with loopback and external IP",
			remoteAddr: "127.0.0.1:54321",
			headers:    map[string][]string{"X-Forwarded-For": {"127.0.0.1, 203.0.113.1"}},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "multi-header XFF bypass attempt",
			remoteAddr: "127.0.0.1:54321",
			headers:    map[string][]string{"X-Forwarded-For": {"127.0.0.1", "203.0.113.1"}},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "single X-Real-IP external",
			remoteAddr: "127.0.0.1:54321",
			headers:    map[string][]string{"X-Real-IP": {"198.51.100.2"}},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "multi-header X-Real-IP bypass attempt",
			remoteAddr: "127.0.0.1:54321",
			headers:    map[string][]string{"X-Real-IP": {"127.0.0.1", "198.51.100.2"}},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "Forwarded RFC 7239 external node",
			remoteAddr: "127.0.0.1:54321",
			headers:    map[string][]string{"Forwarded": {"for=203.0.113.195;proto=http"}},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "Forwarded RFC 7239 loopback node",
			remoteAddr: "127.0.0.1:54321",
			headers:    map[string][]string{"Forwarded": {"for=127.0.0.1;proto=http"}},
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			upstreamCalled = false
			req := httptest.NewRequest("GET", "http://devstack.test/", nil)
			req.RemoteAddr = tc.remoteAddr
			for k, values := range tc.headers {
				for _, v := range values {
					req.Header.Add(k, v)
				}
			}
			rec := httptest.NewRecorder()
			router.Handler().ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d (body: %s)", tc.wantStatus, rec.Code, rec.Body.String())
			}
			if tc.wantStatus == http.StatusForbidden && upstreamCalled {
				t.Fatal("upstream was called for forbidden request")
			}
		})
	}
}

func TestProxyHostHeaderValidationAndSplitting(t *testing.T) {
	router := NewRouter()
	router.Replace(Snapshot{
		Routes: map[string]Route{
			"blog.test": {
				SiteID:     "site-1",
				Host:       "blog.test",
				Kind:       KindStatic,
				Upstream:   "127.0.0.1:59999",
				TLS:        true,
				ForceHTTPS: true,
			},
		},
	})

	maliciousHosts := []string{
		"blog.test\r\nInjected-Header: evil",
		"blog.test\nInjected-Header: evil",
		"blog.test/path",
		"blog.test\\escape",
		"blog.test#fragment",
		"blog.test?query",
		"blog.test@evil.test",
		"blog .test",
		"blog\ttest",
	}

	for _, h := range maliciousHosts {
		t.Run(h, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Host = h
			req.RemoteAddr = "127.0.0.1:12345"
			rec := httptest.NewRecorder()
			router.Handler().ServeHTTP(rec, req)

			// Must not route or redirect with injected headers
			if rec.Code == http.StatusPermanentRedirect {
				loc := rec.Header().Get("Location")
				if strings.Contains(loc, "\r") || strings.Contains(loc, "\n") || strings.Contains(loc, "Injected-Header") {
					t.Fatalf("CRLF injection in Location header: %q", loc)
				}
			}
			if rec.Code != http.StatusNotFound {
				t.Fatalf("expected 404 for invalid host %q, got %d", h, rec.Code)
			}
		})
	}
}
