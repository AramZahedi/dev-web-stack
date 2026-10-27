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
