package dockerweb

import (
	"strings"
	"testing"
)

func TestApacheBaseConfigListensOnConfiguredPorts(t *testing.T) {
	apache, err := RenderBase(ServerApache, 8080, 8443)
	if err != nil {
		t.Fatalf("RenderBase(apache): %v", err)
	}
	text := string(apache)
	for _, want := range []string{"Listen 8080", "Listen 8443"} {
		if !strings.Contains(text, want) {
			t.Errorf("apache base config missing %q", want)
		}
	}
	if strings.Contains(text, "Listen 80\n") || strings.Contains(text, "Listen 443\n") {
		t.Error("apache base config must not listen on production ports when overridden")
	}

	nginx, err := RenderBase(ServerNginx, 8080, 8443)
	if err != nil {
		t.Fatalf("RenderBase(nginx): %v", err)
	}
	if strings.Contains(string(nginx), "listen ") {
		t.Error("nginx base config must not contain listen directives; generated server blocks own the ports")
	}
}

func TestNginxSiteVhostUsesConfiguredListenPorts(t *testing.T) {
	f := newRenderFixture(t)
	f.writeFile(t, "site/index.html", "ok")
	site := f.site("site", "site", "site.test", "site")
	site.TLS = true
	set := baseSet(f, site)
	set.HTTPListenPort = 8080
	set.HTTPSListenPort = 8443
	out := renderOne(t, ServerNginx, set, "site.conf")
	for _, want := range []string{"listen 8080;", "listen 8443 ssl;"} {
		if !strings.Contains(out, want) {
			t.Errorf("nginx site vhost missing %q", want)
		}
	}
}

func TestRenderBaseRejectsInvalidPorts(t *testing.T) {
	cases := []struct {
		name        string
		http, https int
	}{
		{name: "zero http", http: 0, https: 443},
		{name: "too high", http: 80, https: 70000},
		{name: "equal", http: 8443, https: 8443},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := RenderBase(ServerNginx, tc.http, tc.https); err == nil {
				t.Fatal("expected invalid port error")
			}
			if _, err := RenderBase(ServerApache, tc.http, tc.https); err == nil {
				t.Fatal("expected invalid port error")
			}
		})
	}
}

func TestApacheBaseHasRuntimePathsAndIncludes(t *testing.T) {
	base, err := RenderBase(ServerApache, 80, 443)
	if err != nil {
		t.Fatal(err)
	}
	text := string(base)
	for _, want := range []string{
		`ServerRoot "/usr/local/apache2"`,
		`ErrorLog "/usr/local/apache2/logs/error_log"`,
		`CustomLog "/usr/local/apache2/logs/access_log" common`,
		"DocumentRoot \"/usr/local/apache2/htdocs\"",
		"SSLSessionCache",
		"IncludeOptional conf/conf.d/generated/*.conf",
		"IncludeOptional conf/conf.d/custom/*.conf",
		"LoadModule mpm_event_module modules/mod_mpm_event.so",
		"LoadModule proxy_fcgi_module modules/mod_proxy_fcgi.so",
		"LoadModule proxy_wstunnel_module modules/mod_proxy_wstunnel.so",
		"LoadModule ssl_module modules/mod_ssl.so",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("apache base config missing %q", want)
		}
	}
}

func TestNginxBaseLeavesManagedDirectivesToPerformanceSnippets(t *testing.T) {
	base, err := RenderBase(ServerNginx, 80, 443)
	if err != nil {
		t.Fatal(err)
	}
	for _, managed := range []string{
		"worker_processes",
		"worker_connections",
		"sendfile ",
		"tcp_nopush",
		"keepalive_timeout",
		"gzip ",
		"gzip_types",
	} {
		if strings.Contains(string(base), managed) {
			t.Errorf("nginx base config must not duplicate performance directive %q", managed)
		}
	}
}

func TestNginxBaseHasRuntimePaths(t *testing.T) {
	base, err := RenderBase(ServerNginx, 80, 443)
	if err != nil {
		t.Fatal(err)
	}
	text := string(base)
	for _, want := range []string{
		"user  nginx;",
		"error_log  /var/log/nginx/error.log warn;",
		"pid        /var/run/nginx.pid;",
		"include       /etc/nginx/mime.types;",
		"access_log  /var/log/nginx/access.log  main;",
		"include /etc/nginx/conf.d/performance/main.conf;",
		"include /etc/nginx/conf.d/performance/events.conf;",
		"include /etc/nginx/conf.d/performance/http.conf;",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("nginx base config missing %q", want)
		}
	}
}
