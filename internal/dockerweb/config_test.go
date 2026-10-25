package dockerweb

import (
	"strings"
	"testing"
)

func TestBaseConfigsListenOnConfiguredPorts(t *testing.T) {
	nginx, err := RenderBase(ServerNginx, 8080, 8443)
	if err != nil {
		t.Fatalf("RenderBase(nginx): %v", err)
	}
	// nginx listens in the generated server blocks, not in nginx.conf.
	if strings.Contains(string(nginx), "listen 8080") {
		t.Fatal("nginx base config must not contain per-site listen directives")
	}
	if !strings.Contains(string(nginx), "include /etc/nginx/conf.d/generated/*.conf;") {
		t.Fatal("nginx base config missing generated include")
	}

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
	} {
		if !strings.Contains(text, want) {
			t.Errorf("nginx base config missing %q", want)
		}
	}
}
