package proxy

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRouterHostMatchAndUnknownHost(t *testing.T) {
	r := NewRouter()
	r.Replace(Snapshot{Routes: map[string]Route{"blog.test": {
		SiteID: "1", Host: "blog.test", Kind: KindNode, Upstream: "127.0.0.1:41001",
	}}})
	if _, ok := r.Lookup("BLOG.TEST"); !ok {
		t.Fatal("case-insensitive lookup failed")
	}

	req := httptest.NewRequest("GET", "http://evil.test/", nil)
	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "devstack") {
		t.Fatalf("404 page is not branded: %q", rec.Body.String())
	}
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("HSTS header emitted: %q", got)
	}
}

func TestRouterLookupStripsPortAndNormalizesCase(t *testing.T) {
	r := NewRouter()
	r.Replace(Snapshot{Routes: map[string]Route{
		"blog.test": {SiteID: "1", Host: "blog.test", Kind: KindStatic},
	}})

	for _, host := range []string{"blog.test", "BLOG.TEST", "Blog.Test:8080", "blog.test:443", "blog.test."} {
		route, ok := r.Lookup(host)
		if !ok {
			t.Fatalf("Lookup(%q) failed", host)
		}
		if route.SiteID != "1" {
			t.Fatalf("Lookup(%q) = %+v", host, route)
		}
	}
	if _, ok := r.Lookup("www.blog.test"); ok {
		t.Fatal("unexpected alias match")
	}
}

func TestRouterReplaceSwapsSnapshot(t *testing.T) {
	r := NewRouter()
	r.Replace(Snapshot{Routes: map[string]Route{
		"old.test": {SiteID: "old", Host: "old.test"},
	}})
	r.Replace(Snapshot{Routes: map[string]Route{
		"new.test": {SiteID: "new", Host: "new.test"},
	}})

	if _, ok := r.Lookup("old.test"); ok {
		t.Fatal("old route survived Replace")
	}
	if _, ok := r.Lookup("new.test"); !ok {
		t.Fatal("new route missing after Replace")
	}

	// A zero-value router must behave like an empty snapshot.
	empty := NewRouter()
	if _, ok := empty.Lookup("anything.test"); ok {
		t.Fatal("empty router returned a route")
	}
}

func TestRouterCertificateSelectsBySNI(t *testing.T) {
	blogCert := &tls.Certificate{}
	wwwCert := &tls.Certificate{}
	defaultCert := &tls.Certificate{}
	r := NewRouter()
	r.Replace(Snapshot{
		Certs: map[string]*tls.Certificate{
			"blog.test": blogCert,
			"www.test":  wwwCert,
		},
		DefaultCert: defaultCert,
	})

	cases := []struct {
		sni  string
		want *tls.Certificate
	}{
		{"blog.test", blogCert},
		{"BLOG.TEST", blogCert},
		{"blog.test.", blogCert},
		{"blog.test:443", blogCert},
		{"unknown.test", defaultCert},
		{"", defaultCert},
	}
	for _, tc := range cases {
		got, err := r.Certificate(&tls.ClientHelloInfo{ServerName: tc.sni})
		if err != nil {
			t.Fatalf("Certificate(%q): %v", tc.sni, err)
		}
		if got != tc.want {
			t.Fatalf("Certificate(%q) = %p, want %p", tc.sni, got, tc.want)
		}
	}

	bare := NewRouter()
	if _, err := bare.Certificate(&tls.ClientHelloInfo{ServerName: "blog.test"}); err == nil {
		t.Fatal("expected an error when no certificate is configured")
	}
}

func TestRouterUpstreamTimeouts(t *testing.T) {
	r := NewRouter()
	if got := r.staticTransport.ResponseHeaderTimeout; got != staticResponseHeaderTimeout {
		t.Fatalf("static response header timeout = %v", got)
	}
	if got := r.nodeTransport.ResponseHeaderTimeout; got != nodeResponseHeaderTimeout {
		t.Fatalf("node response header timeout = %v", got)
	}
	if staticResponseHeaderTimeout >= nodeResponseHeaderTimeout {
		t.Fatal("Node upstreams must allow more time than static upstreams")
	}
}
