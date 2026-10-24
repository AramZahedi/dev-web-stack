package vhost

import (
	"bytes"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestRenderNginxStaticMatrix(t *testing.T) {
	base := staticSite()
	tests := []struct {
		name   string
		golden string
		mutate func(*SiteSpec)
	}{
		{
			name:   "force https",
			golden: "nginx/static-force-https.conf",
			mutate: func(s *SiteSpec) { s.ForceHTTPS = true },
		},
		{
			name:   "serving",
			golden: "nginx/static.conf",
			mutate: func(*SiteSpec) {},
		},
		{
			name:   "spa",
			golden: "nginx/static-spa.conf",
			mutate: func(s *SiteSpec) { s.SPAFallback = true },
		},
		{
			name:   "stopped",
			golden: "nginx/static-stopped.conf",
			mutate: func(s *SiteSpec) { s.DesiredState = model.DesiredStopped },
		},
		{
			name:   "http only",
			golden: "nginx/static-http.conf",
			mutate: func(s *SiteSpec) {
				s.TLS = false
				s.CertPEM = nil
				s.KeyPEM = nil
			},
		},
		{
			name:   "no www",
			golden: "nginx/static-no-www.conf",
			mutate: func(s *SiteSpec) { s.WWW = false },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := base
			tc.mutate(&spec)

			got, err := RenderSite(ServerNginx, spec)
			if err != nil {
				t.Fatalf("RenderSite: %v", err)
			}
			compareGolden(t, tc.golden, got)
		})
	}
}

func TestRenderNginxNode(t *testing.T) {
	got, err := RenderSite(ServerNginx, nodeSite())
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}
	compareGolden(t, "nginx/node.conf", got)
}

func TestRenderNginxNodeStopped(t *testing.T) {
	spec := nodeSite()
	spec.DesiredState = model.DesiredStopped

	got, err := RenderSite(ServerNginx, spec)
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}
	if bytes.Contains(got, []byte("proxy_pass")) {
		t.Error("stopped node vhost still proxies to the backend")
	}
	if !bytes.Contains(got, []byte("return 503 ")) {
		t.Error("stopped node vhost does not return 503")
	}
	if !bytes.Contains(got, []byte("Site unavailable")) {
		t.Error("stopped node vhost does not contain the branded maintenance page")
	}
}

func TestRenderNginxTLSRedirects(t *testing.T) {
	httpOnly := staticSite()
	httpOnly.TLS = false
	httpOnly.CertPEM = nil
	httpOnly.KeyPEM = nil

	got, err := RenderSite(ServerNginx, httpOnly)
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}
	if !bytes.Contains(got, []byte("return 308 http://$host$request_uri;")) {
		t.Error("TLS-disabled site does not redirect HTTPS to HTTP")
	}
	for _, want := range []string{
		"ssl_certificate     /etc/devstack/tls/__dashboard__.crt;",
		"ssl_certificate_key /etc/devstack/tls/__dashboard__.key;",
	} {
		if !bytes.Contains(got, []byte(want)) {
			t.Errorf("TLS-disabled site is missing dashboard certificate line %q", want)
		}
	}

	forced := staticSite()
	forced.ForceHTTPS = true
	got, err = RenderSite(ServerNginx, forced)
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}
	if !bytes.Contains(got, []byte("return 308 https://$host$request_uri;")) {
		t.Error("force-HTTPS site does not redirect HTTP to HTTPS")
	}
	if !bytes.Contains(got, []byte("ssl_certificate     /etc/devstack/tls/s1.crt;")) {
		t.Error("force-HTTPS site does not use the site certificate")
	}
}

func TestRenderNginxHTTP2Compatibility(t *testing.T) {
	got, err := RenderSite(ServerNginx, staticSite())
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}
	if !bytes.Contains(got, []byte("listen 443 ssl http2;")) {
		t.Error("missing combined listen/ssl/http2 directive")
	}
	if bytes.Contains(got, []byte("http2 on;")) {
		t.Error("emits the nginx 1.25-only http2 directive")
	}
}

func TestRenderNginxQuotesInterpolatedPaths(t *testing.T) {
	tests := []struct {
		name string
		root string
		want string
	}{
		{
			name: "spaces",
			root: "/home/u/my site/public",
			want: `root "/home/u/my site/public";`,
		},
		{
			name: "quote",
			root: `/home/u/we"ird`,
			want: `root "/home/u/we\"ird";`,
		},
		{
			name: "brace",
			root: "/home/u/brace}",
			want: `root "/home/u/brace}";`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := staticSite()
			spec.DocumentRoot = tc.root

			got, err := RenderSite(ServerNginx, spec)
			if err != nil {
				t.Fatalf("RenderSite: %v", err)
			}
			if !bytes.Contains(got, []byte(tc.want)) {
				t.Errorf("rendered config does not contain %q:\n%s", tc.want, got)
			}
		})
	}

	spec := staticSite()
	spec.DocumentRoot = "/home/u/blog\npublic"
	if _, err := RenderSite(ServerNginx, spec); err == nil {
		t.Fatal("RenderSite accepted a document root containing a newline")
	}

	spec = staticSite()
	spec.DocumentRoot = "/home/u/$cache"
	if _, err := RenderSite(ServerNginx, spec); err == nil {
		t.Fatal("RenderSite accepted a document root containing a dollar sign")
	}
	if err := ValidateSiteSpec(spec); err == nil {
		t.Fatal("ValidateSiteSpec accepted a document root containing a dollar sign")
	}
}

func TestRenderNginxNodeProxySettings(t *testing.T) {
	got, err := RenderSite(ServerNginx, nodeSite())
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}
	for _, want := range []string{
		"proxy_pass http://127.0.0.1:41003;",
		"proxy_http_version 1.1;",
		"proxy_set_header Upgrade $http_upgrade;",
		`proxy_set_header Connection "upgrade";`,
		"proxy_set_header Host $host;",
		"proxy_set_header X-Forwarded-For $remote_addr;",
		"proxy_set_header X-Forwarded-Proto $scheme;",
		"proxy_set_header X-Forwarded-Host $host;",
		"proxy_read_timeout 120s;",
		"proxy_intercept_errors on;",
		"error_page 502 503 504 =503 /_devstack/503.html;",
	} {
		if !bytes.Contains(got, []byte(want)) {
			t.Errorf("node vhost is missing %q", want)
		}
	}
}

func TestRenderNginxPHPFrontController(t *testing.T) {
	s := phpSite()
	s.FrontController = true

	got, err := RenderSite(ServerNginx, s)
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}
	for _, want := range []string{
		"root /home/u/blog/public;",
		"index index.php index.html;",
		"autoindex off;",
		"location ~* ^(.+\\.php)(/.*)?$",
		"fastcgi_split_path_info ^(.+\\.php)(/.*)$;",
		"try_files $fastcgi_script_name =404;",
		"fastcgi_pass 127.0.0.1:43001;",
		"fastcgi_index index.php;",
		"fastcgi_read_timeout 120s;",
		"fastcgi_buffering off;",
		"fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;",
		"fastcgi_param SCRIPT_NAME $fastcgi_script_name;",
		"set $devstack_path_info $fastcgi_path_info;",
		"fastcgi_param PATH_INFO $devstack_path_info;",
		"fastcgi_param DOCUMENT_ROOT $document_root;",
		"fastcgi_param QUERY_STRING $query_string;",
		"fastcgi_param REQUEST_METHOD $request_method;",
		"fastcgi_param REQUEST_URI $request_uri;",
		"fastcgi_param SERVER_PROTOCOL $server_protocol;",
		"fastcgi_param GATEWAY_INTERFACE CGI/1.1;",
		"fastcgi_param HTTPS $https if_not_empty;",
		"fastcgi_param SERVER_NAME $server_name;",
		"fastcgi_param SERVER_ADDR $server_addr;",
		"fastcgi_param SERVER_PORT $server_port;",
		"fastcgi_param REMOTE_ADDR $remote_addr;",
		"fastcgi_param REMOTE_PORT $remote_port;",
		"fastcgi_param CONTENT_TYPE $content_type;",
		"fastcgi_param CONTENT_LENGTH $content_length;",
		"location @devstack_front_controller",
		"fastcgi_param SCRIPT_FILENAME \"/home/u/blog/public/index.php\";",
		"fastcgi_param SCRIPT_NAME /index.php;",
		"fastcgi_param PATH_INFO $uri;",
		"try_files $uri $uri/ @devstack_front_controller;",
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	compareGolden(t, "nginx/php-front.conf", got)
}

func TestRenderNginxPHPNonFrontController(t *testing.T) {
	got, err := RenderSite(ServerNginx, phpSite())
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}
	text := string(got)
	for _, want := range []string{
		"index index.php index.html;",
		"location ~* ^(.+\\.php)(/.*)?$",
		"fastcgi_pass 127.0.0.1:43001;",
		"try_files $uri $uri/ =404;",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "@devstack_front_controller") {
		t.Errorf("non-front-controller PHP vhost has a front controller location:\n%s", text)
	}
}

func TestRenderNginxPHPDenyOrdering(t *testing.T) {
	s := phpSite()
	s.FrontController = true

	got, err := RenderSite(ServerNginx, s)
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}
	text := string(got)
	dotfile := strings.Index(text, `location ~ /\. {`)
	backup := strings.Index(text, `location ~* \.php(?:~|\.(?:bak|orig|save|old|swp|swo|tmp))$ {`)
	php := strings.Index(text, `location ~* ^(.+\.php)(/.*)?$ {`)
	devstack := strings.Index(text, `location ^~ /_devstack {`)
	if dotfile < 0 || backup < 0 || php < 0 || devstack < 0 {
		t.Fatalf("missing a denial or PHP location:\n%s", text)
	}
	// nginx evaluates regex locations in order and uses the first match, so a
	// dotfile .php must reach the dotfile denial, not the FastCGI location.
	if !(dotfile < backup && backup < php) {
		t.Fatalf("deny locations are not before the PHP location:\n%s", text)
	}
	if devstack > php {
		t.Errorf("the /_devstack denial is rendered after the PHP location:\n%s", text)
	}
}

func TestRenderNginxPHPStopped(t *testing.T) {
	s := phpSite()
	s.DesiredState = model.DesiredStopped

	got, err := RenderSite(ServerNginx, s)
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}
	text := string(got)
	if strings.Contains(text, "fastcgi_pass") {
		t.Errorf("stopped PHP vhost renders FastCGI:\n%s", text)
	}
	if strings.Contains(text, "location ~*") {
		t.Errorf("stopped PHP vhost renders a PHP location:\n%s", text)
	}
	if !strings.Contains(text, "return 503 ") {
		t.Errorf("stopped PHP vhost does not return 503:\n%s", text)
	}
}

func TestRenderNginxStaticHardening(t *testing.T) {
	got, err := RenderSite(ServerNginx, staticSite())
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}
	for _, want := range []string{
		"root /home/u/blog/public;",
		"autoindex off;",
		`add_header Cache-Control "no-store" always;`,
		"location ~ /\\. {",
		"location ^~ /_devstack {",
		"try_files $uri $uri/ =404;",
	} {
		if !bytes.Contains(got, []byte(want)) {
			t.Errorf("static vhost is missing %q", want)
		}
	}
	if strings.Contains(string(got), "autoindex on") {
		t.Error("static vhost enables directory listing")
	}
}
