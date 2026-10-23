package vhost

import (
	"bytes"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestRenderApacheStaticMatrix(t *testing.T) {
	base := staticSite()
	tests := []struct {
		name   string
		golden string
		mutate func(*SiteSpec)
	}{
		{
			name:   "force https",
			golden: "apache/static-force-https.conf",
			mutate: func(s *SiteSpec) { s.ForceHTTPS = true },
		},
		{
			name:   "serving",
			golden: "apache/static.conf",
			mutate: func(*SiteSpec) {},
		},
		{
			name:   "spa",
			golden: "apache/static-spa.conf",
			mutate: func(s *SiteSpec) { s.SPAFallback = true },
		},
		{
			name:   "stopped",
			golden: "apache/static-stopped.conf",
			mutate: func(s *SiteSpec) { s.DesiredState = model.DesiredStopped },
		},
		{
			name:   "http only",
			golden: "apache/static-http.conf",
			mutate: func(s *SiteSpec) {
				s.TLS = false
				s.CertPEM = nil
				s.KeyPEM = nil
			},
		},
		{
			name:   "no www",
			golden: "apache/static-no-www.conf",
			mutate: func(s *SiteSpec) { s.WWW = false },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := base
			tc.mutate(&spec)

			got, err := RenderSite(ServerApache, spec)
			if err != nil {
				t.Fatalf("RenderSite: %v", err)
			}
			compareGolden(t, tc.golden, got)
		})
	}
}

func TestRenderApacheNode(t *testing.T) {
	got, err := RenderSite(ServerApache, nodeSite())
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}
	compareGolden(t, "apache/node.conf", got)
}

func TestRenderApacheNodeStopped(t *testing.T) {
	spec := nodeSite()
	spec.DesiredState = model.DesiredStopped

	got, err := RenderSite(ServerApache, spec)
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}
	if bytes.Contains(got, []byte("ProxyPass")) {
		t.Error("stopped node vhost still proxies to the backend")
	}
	if !bytes.Contains(got, []byte("RewriteRule ^ - [R=503,L]")) {
		t.Error("stopped node vhost does not return 503")
	}
	if !bytes.Contains(got, []byte("Site unavailable")) {
		t.Error("stopped node vhost does not contain the branded maintenance page")
	}
}

func TestRenderApacheRedirectsPreservePath(t *testing.T) {
	forced := staticSite()
	forced.ForceHTTPS = true
	got, err := RenderSite(ServerApache, forced)
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}
	if !bytes.Contains(got, []byte("RewriteRule ^ https://%{HTTP_HOST}%{REQUEST_URI} [R=308,L]")) {
		t.Error("force-HTTPS vhost does not preserve host and request path")
	}

	httpOnly := staticSite()
	httpOnly.TLS = false
	httpOnly.CertPEM = nil
	httpOnly.KeyPEM = nil
	got, err = RenderSite(ServerApache, httpOnly)
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}
	if !bytes.Contains(got, []byte("RewriteRule ^ http://%{HTTP_HOST}%{REQUEST_URI} [R=308,L]")) {
		t.Error("TLS-disabled vhost does not redirect HTTPS to HTTP with host and path")
	}
	if bytes.Contains(got, []byte("Redirect permanent /")) {
		t.Error("vhost uses a bare Redirect instead of a path-preserving rewrite")
	}
	if !bytes.Contains(got, []byte("RewriteEngine On")) {
		t.Error("vhost does not enable mod_rewrite")
	}
}

func TestRenderEscapesApachePaths(t *testing.T) {
	tests := []struct {
		name string
		root string
		want string
	}{
		{
			name: "spaces and quotes",
			root: `/home/u/my "site"/public`,
			want: `DocumentRoot "/home/u/my \"site\"/public"`,
		},
		{
			name: "directory section",
			root: `/home/u/my "site"/public`,
			want: `<Directory "/home/u/my \"site\"/public">`,
		},
		{
			name: "backslash",
			root: `/home/u/back\slash`,
			want: `DocumentRoot "/home/u/back\\slash"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := staticSite()
			spec.DocumentRoot = tc.root

			got, err := RenderSite(ServerApache, spec)
			if err != nil {
				t.Fatalf("RenderSite: %v", err)
			}
			if !bytes.Contains(got, []byte(tc.want)) {
				t.Errorf("rendered config does not contain %q:\n%s", tc.want, got)
			}
		})
	}

	for _, root := range []string{"/home/u/blog\npublic", "/home/u/blog\x00public"} {
		spec := staticSite()
		spec.DocumentRoot = root
		if _, err := RenderSite(ServerApache, spec); err == nil {
			t.Fatalf("RenderSite accepted a document root %q", root)
		}
		if err := ValidateSiteSpec(spec); err == nil {
			t.Fatalf("ValidateSiteSpec accepted a document root %q", root)
		}
	}
}

func TestRenderApacheNodeProxySettings(t *testing.T) {
	got, err := RenderSite(ServerApache, nodeSite())
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}
	for _, want := range []string{
		`ProxyPass "/" "http://127.0.0.1:41003/" upgrade=websocket connectiontimeout=5 timeout=120`,
		`ProxyPassReverse "/" "http://127.0.0.1:41003/"`,
		"ProxyPreserveHost On",
		"ProxyAddHeaders Off",
		`RequestHeader set Host "%{DEVSTACK_HOST}e"`,
		`RequestHeader set X-Forwarded-For "%{DEVSTACK_REMOTE}e"`,
		`RequestHeader set X-Forwarded-Proto "http"`,
		`RequestHeader set X-Forwarded-Proto "https"`,
		`RequestHeader set X-Forwarded-Host "%{DEVSTACK_HOST}e"`,
		`ErrorDocument 503 "`,
	} {
		if !bytes.Contains(got, []byte(want)) {
			t.Errorf("node vhost is missing %q", want)
		}
	}
}

func TestRenderApacheStaticHardening(t *testing.T) {
	got, err := RenderSite(ServerApache, staticSite())
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}
	for _, want := range []string{
		`DocumentRoot "/home/u/blog/public"`,
		"Options -Indexes",
		"AllowOverride None",
		"Require all granted",
		`<FilesMatch "^\.">`,
		`<LocationMatch "^/_devstack(/|$)">`,
		`Header always set Cache-Control "no-store"`,
	} {
		if !bytes.Contains(got, []byte(want)) {
			t.Errorf("static vhost is missing %q", want)
		}
	}
	if strings.Contains(string(got), "Options +Indexes") {
		t.Error("static vhost enables directory listing")
	}
}
