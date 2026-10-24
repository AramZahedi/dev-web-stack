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

func TestRenderApacheMixedPortRedirects(t *testing.T) {
	tests := []struct {
		name       string
		httpPort   int
		httpsPort  int
		forceHTTPS bool
		tls        bool
		wantRule   string
	}{
		{
			name:       "force https source high target default",
			httpPort:   18080,
			httpsPort:  443,
			forceHTTPS: true,
			tls:        true,
			wantRule:   "RewriteRule ^ https://%1%{REQUEST_URI} [R=308,L]",
		},
		{
			name:      "tls off source default target high",
			httpPort:  80,
			httpsPort: 18443,
			tls:       false,
			wantRule:  "RewriteRule ^ http://%1%{REQUEST_URI} [R=308,L]",
		},
		{
			name:       "force https source default target high",
			httpPort:   80,
			httpsPort:  18443,
			forceHTTPS: true,
			tls:        true,
			wantRule:   "RewriteRule ^ https://%1:18443%{REQUEST_URI} [R=308,L]",
		},
		{
			name:      "tls off source high target default",
			httpPort:  18080,
			httpsPort: 443,
			tls:       false,
			wantRule:  "RewriteRule ^ http://%1:18080%{REQUEST_URI} [R=308,L]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			site := staticSite()
			site.TLS = tc.tls
			if !tc.tls {
				site.CertPEM = nil
				site.KeyPEM = nil
			}
			site.ForceHTTPS = tc.forceHTTPS

			set := testSet(ServerApache, 0)
			set.Sites = []SiteSpec{site}
			set.HTTPListenPort = tc.httpPort
			set.HTTPSListenPort = tc.httpsPort

			files, err := Render(set)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			data := files["blog-s1.conf"]
			if len(data) == 0 {
				t.Fatal("site config was not rendered")
			}
			if !bytes.Contains(data, []byte("RewriteCond %{HTTP_HOST} ^([^:]+)\n")) {
				t.Errorf("site does not capture the host without the source port:\n%s", data)
			}
			if !bytes.Contains(data, []byte(tc.wantRule)) {
				t.Errorf("site is missing %q:\n%s", tc.wantRule, data)
			}
			if bytes.Contains(data, []byte("RewriteRule ^ http://%{HTTP_HOST}%{REQUEST_URI}")) ||
				bytes.Contains(data, []byte("RewriteRule ^ https://%{HTTP_HOST}%{REQUEST_URI}")) {
				t.Errorf("site still uses the source HTTP_HOST authority:\n%s", data)
			}
		})
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

func TestRenderApachePHP(t *testing.T) {
	s := phpSite()
	s.FrontController = true

	got, err := RenderSite(ServerApache, s)
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}
	for _, want := range []string{
		`DocumentRoot "/home/u/blog/public"`,
		`<Directory "/home/u/blog/public">`,
		"Options -Indexes",
		"AllowOverride None",
		"Require all granted",
		"DirectoryIndex index.php index.html",
		`<FilesMatch "(?i)\.php$">`,
		`SetHandler "proxy:fcgi://127.0.0.1:43001"`,
		`<FilesMatch "(?i)\.php(~|\.(?:bak|orig|save|old|swp|swo|tmp))$">`,
		"RewriteEngine On",
		"RewriteCond %{REQUEST_FILENAME} !-f",
		"RewriteCond %{REQUEST_FILENAME} !-d",
		"RewriteRule ^(.*)$ /index.php/$1 [L,QSA]",
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	compareGolden(t, "apache/php.conf", got)
}

func TestRenderApachePHPNonFrontController(t *testing.T) {
	got, err := RenderSite(ServerApache, phpSite())
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}
	text := string(got)
	for _, want := range []string{
		"DirectoryIndex index.php index.html",
		`SetHandler "proxy:fcgi://127.0.0.1:43001"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "RewriteRule ^(.*)$ /index.php/$1") {
		t.Errorf("non-front-controller PHP vhost rewrites to the index:\n%s", text)
	}
}

func TestRenderApachePHPDenials(t *testing.T) {
	got, err := RenderSite(ServerApache, phpSite())
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}
	text := string(got)
	for _, want := range []string{
		`<FilesMatch "^\.">`,
		`<FilesMatch "(?i)\.php(~|\.(?:bak|orig|save|old|swp|swo|tmp))$">`,
		`<LocationMatch "(^|/)\.">`,
		`<LocationMatch "^/_devstack(/|$)">`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	backup := strings.Index(text, `FilesMatch "(?i)\.php(~|\.(?:bak|orig|save|old|swp|swo|tmp))$"`)
	php := strings.Index(text, `FilesMatch "(?i)\.php$"`)
	if backup < 0 || php < 0 {
		t.Fatalf("missing a PHP FilesMatch section:\n%s", text)
	}
	if backup > php {
		t.Errorf("the source-backup denial is rendered after the PHP handler:\n%s", text)
	}
}

func TestRenderApachePHPFrontControllerRewriteIsPerDirectory(t *testing.T) {
	s := phpSite()
	s.FrontController = true

	got, err := RenderSite(ServerApache, s)
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}
	text := string(got)
	directory := strings.Index(text, `<Directory "/home/u/blog/public">`)
	directoryEnd := strings.Index(text, "</Directory>")
	rule := strings.Index(text, "RewriteRule ^(.*)$ /index.php/$1 [L,QSA]")
	if directory < 0 || directoryEnd < 0 || rule < 0 {
		t.Fatalf("missing the Directory section or front-controller rewrite:\n%s", text)
	}
	if !(directory < rule && rule < directoryEnd) {
		t.Fatalf("front-controller rewrite is outside the Directory section, where REQUEST_FILENAME is the URL path:\n%s", text)
	}
}

func TestRenderApachePHPStopped(t *testing.T) {
	s := phpSite()
	s.DesiredState = model.DesiredStopped

	got, err := RenderSite(ServerApache, s)
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}
	text := string(got)
	if strings.Contains(text, "SetHandler") || strings.Contains(text, "proxy:fcgi") {
		t.Errorf("stopped PHP vhost renders FastCGI:\n%s", text)
	}
	if !strings.Contains(text, "RewriteRule ^ - [R=503,L]") {
		t.Errorf("stopped PHP vhost does not return 503:\n%s", text)
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
		`<LocationMatch "(^|/)\.">`,
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
