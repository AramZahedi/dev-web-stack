package dockerweb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

type renderFixture struct {
	root     string
	projects string
	certs    string
}

func newRenderFixture(t *testing.T) renderFixture {
	t.Helper()
	root := t.TempDir()
	projects := filepath.Join(root, "projects")
	certs := filepath.Join(root, "certs")
	for _, dir := range []string{projects, certs} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	return renderFixture{root: root, projects: projects, certs: certs}
}

func (f renderFixture) writeFile(t *testing.T, rel, content string) string {
	t.Helper()
	path := filepath.Join(f.projects, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func (f renderFixture) site(id, name, domain, rel string) SiteSpec {
	return SiteSpec{
		ID:               id,
		Name:             name,
		Domain:           domain,
		Kind:             model.SiteStatic,
		HostDocumentRoot: filepath.Join(f.projects, filepath.FromSlash(rel)),
		DesiredState:     model.DesiredRunning,
	}
}

func baseSet(f renderFixture, sites ...SiteSpec) Set {
	return Set{
		Projects:        f.projects,
		CertsDir:        "/certs",
		Sites:           sites,
		Dashboard:       DashboardSpec{Domain: "devstack.test", UIPort: 7070},
		MaxBodyBytes:    64 << 20,
		HTTPListenPort:  80,
		HTTPSListenPort: 443,
	}
}

func renderOne(t *testing.T, server Server, set Set, filename string) string {
	t.Helper()
	files, err := Render(server, set)
	if err != nil {
		t.Fatalf("Render(%s): %v", server, err)
	}
	data, ok := files[filename]
	if !ok {
		t.Fatalf("Render(%s) did not produce %q; files: %v", server, filename, keys(files))
	}
	return string(data)
}

func keys(files map[string][]byte) []string {
	out := make([]string, 0, len(files))
	for name := range files {
		out = append(out, name)
	}
	return out
}

func TestRenderNginxStaticTLSForceHTTPS(t *testing.T) {
	f := newRenderFixture(t)
	f.writeFile(t, "site/index.html", "<html>site</html>")
	site := f.site("site", "site", "site.test", "site")
	site.WWW = true
	site.TLS = true
	site.ForceHTTPS = true
	site.SPAFallback = true
	site.SPAFallbackFile = "index.html"

	set := baseSet(f, site)
	out := renderOne(t, ServerNginx, set, "site.conf")
	for _, want := range []string{
		"server_name site.test www.site.test;",
		"root /sites/site;",
		"listen 80;",
		"listen 443 ssl;",
		"http2 on;",
		"return 308 https://$host$request_uri;",
		"ssl_certificate     /certs/site.crt;",
		"ssl_certificate_key /certs/site.key;",
		`try_files $uri $uri/ /index.html;`,
		`location ~ /\. {`,
		`location ~* "\.[A-Za-z0-9]{1,10}$" {`,
		`location ^~ /_devstack {`,
		`deny all;`,
		`try_files $uri =404;`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("nginx static vhost missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "Strict-Transport-Security") {
		t.Error("nginx static vhost must not emit HSTS")
	}
	if strings.Contains(out, `Strict-Transport-Security" always`) {
		t.Error("nginx static vhost must not set HSTS")
	}

	again := renderOne(t, ServerNginx, set, "site.conf")
	if again != out {
		t.Fatal("nginx rendering is not deterministic")
	}
	firstLine := strings.SplitN(out, "\n", 2)[0]
	for _, want := range []string{"nginx", "site", "sha256="} {
		if !strings.Contains(firstLine, want) {
			t.Errorf("generated header %q missing %q", firstLine, want)
		}
	}
}

func TestRenderNginxPHPFrontControllerUsesHostPaths(t *testing.T) {
	f := newRenderFixture(t)
	index := f.writeFile(t, "app/public/index.php", "<?php echo 'ok';")
	site := f.site("app", "app", "app.test", "app/public")
	site.Kind = model.SitePHP
	site.BackendPort = 43000
	site.FrontController = true
	site.FrontControllerFile = "index.php"
	site.FrontControllerMode = "generated"
	site.TLS = true

	out := renderOne(t, ServerNginx, baseSet(f, site), "app.conf")
	for _, want := range []string{
		"root /sites/app/public;",
		"fastcgi_param SCRIPT_FILENAME " + nginxQuotedValue(filepath.Dir(index)+"$fastcgi_script_name") + ";",
		"fastcgi_param DOCUMENT_ROOT " + nginxQuotedValue(filepath.Dir(index)) + ";",
		`fastcgi_param PATH_INFO $devstack_path_info;`,
		`fastcgi_param REQUEST_URI $request_uri;`,
		"fastcgi_param SCRIPT_FILENAME " + nginxQuotedValue(index) + ";",
		`fastcgi_param SCRIPT_NAME "/index.php";`,
		`fastcgi_param PATH_INFO $uri;`,
		"fastcgi_pass 127.0.0.1:43000;",
		"fastcgi_pass_request_headers on;",
		"fastcgi_param HTTP_COOKIE $http_cookie;",
		"fastcgi_param HTTP_AUTHORIZATION $http_authorization;",
		"fastcgi_param HTTP_HOST $http_host;",
		"fastcgi_param HTTP_USER_AGENT $http_user_agent;",
		"fastcgi_param HTTP_ACCEPT $http_accept;",
		"try_files $uri $uri/ @devstack_front_controller;",
		`location ~* \.php(?:~|\.(?:bak|orig|save|old|swp|swo|tmp))$ {`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("nginx PHP vhost missing %q\n%s", want, out)
		}
	}
}

func TestRenderNginxNodeProxy(t *testing.T) {
	f := newRenderFixture(t)
	site := SiteSpec{
		ID:           "node",
		Name:         "node",
		Domain:       "node.test",
		Kind:         model.SiteNode,
		BackendPort:  42000,
		DesiredState: model.DesiredRunning,
		TLS:          true,
	}
	out := renderOne(t, ServerNginx, baseSet(f, site), "node.conf")
	for _, want := range []string{
		"proxy_pass http://127.0.0.1:42000;",
		"proxy_http_version 1.1;",
		"proxy_set_header Upgrade $http_upgrade;",
		"proxy_set_header Connection \"upgrade\";",
		"proxy_set_header X-Forwarded-Proto $scheme;",
		"proxy_set_header X-Forwarded-Host $host;",
		"proxy_hide_header Strict-Transport-Security;",
		"error_page 502 503 504 =503 /_devstack/503.html;",
		`location ~* \.php(?:~|\.(?:bak|orig|save|old|swp|swo|tmp))$ {`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("nginx node vhost missing %q\n%s", want, out)
		}
	}
}

func TestRenderNginxStoppedSiteReturnsBranded503(t *testing.T) {
	f := newRenderFixture(t)
	site := f.site("stopped", "stopped", "stopped.test", "stopped")
	site.DesiredState = model.DesiredStopped
	site.TLS = true
	out := renderOne(t, ServerNginx, baseSet(f, site), "stopped.conf")
	if !strings.Contains(out, "return 503") {
		t.Errorf("stopped nginx vhost missing 503\n%s", out)
	}
	if !strings.Contains(out, "Site unavailable") {
		t.Errorf("stopped nginx vhost missing branded maintenance body\n%s", out)
	}
}

func TestRenderRejectsSiteOutsideProjects(t *testing.T) {
	f := newRenderFixture(t)
	outside := filepath.Join(f.root, "elsewhere", "app")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	site := SiteSpec{
		ID:               "outside",
		Name:             "outside",
		Domain:           "outside.test",
		Kind:             model.SiteStatic,
		HostDocumentRoot: outside,
		DesiredState:     model.DesiredRunning,
	}
	_, err := Render(ServerNginx, baseSet(f, site))
	if err == nil {
		t.Fatal("expected an error for a site outside the projects root")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "outside") {
		t.Fatalf("error = %v, want an explanation that the site is outside projects", err)
	}
}

func TestRenderRejectsMissingFrontControllerAndSymlinkEscape(t *testing.T) {
	f := newRenderFixture(t)
	f.writeFile(t, "app/public/index.php", "<?php")
	site := f.site("app", "app", "app.test", "app/public")
	site.Kind = model.SitePHP
	site.BackendPort = 43001
	site.FrontController = true
	site.FrontControllerFile = "missing.php"
	if _, err := Render(ServerNginx, baseSet(f, site)); err == nil {
		t.Fatal("expected missing front controller file error")
	}

	outside := filepath.Join(f.root, "outside.php")
	if err := os.WriteFile(outside, []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(f.projects, "app/public/escape.php")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	site.FrontControllerFile = "escape.php"
	if _, err := Render(ServerNginx, baseSet(f, site)); err == nil {
		t.Fatal("expected symlink escape error")
	}
}

func TestRenderRejectsMissingSPAFallback(t *testing.T) {
	f := newRenderFixture(t)
	f.writeFile(t, "spa/index.html", "ok")
	site := f.site("spa", "spa", "spa.test", "spa")
	site.SPAFallback = true
	site.SPAFallbackFile = "missing.html"
	if _, err := Render(ServerNginx, baseSet(f, site)); err == nil {
		t.Fatal("expected missing SPA fallback error")
	}
}

func TestRenderApachePHPHtaccessAuto(t *testing.T) {
	f := newRenderFixture(t)
	f.writeFile(t, "app/public/index.php", "<?php")
	f.writeFile(t, "app/public/.htaccess", "RewriteEngine On")
	site := f.site("app", "app", "app.test", "app/public")
	site.Kind = model.SitePHP
	site.BackendPort = 43000
	site.FrontController = true
	site.FrontControllerFile = "index.php"
	site.FrontControllerMode = "auto"
	site.ApacheAllowOverride = "all"
	site.TLS = true

	out := renderOne(t, ServerApache, baseSet(f, site), "app.conf")
	for _, want := range []string{
		`DocumentRoot "/sites/app/public"`,
		`<Directory "/sites/app/public">`,
		"AllowOverride All",
		"Options -Indexes",
		"AcceptPathInfo On",
		"DirectoryIndex index.php index.html",
		`ProxyFCGISetEnvIf "reqenv('SCRIPT_FILENAME') =~ m#^proxy:fcgi://[^/]+/sites/app/public(/.*?\.php)(/.*)$#" PATH_INFO "$2"`,
		`ProxyFCGISetEnvIf "reqenv('SCRIPT_FILENAME') =~ m#^proxy:fcgi://[^/]+/sites/app/public(/.*?\.php)(?:/.*)?$#" SCRIPT_FILENAME ` + apacheQuote(filepath.Join(f.projects, "app/public")+"$1"),
		`ProxyFCGISetEnvIf "reqenv('SCRIPT_FILENAME') =~ m#^/sites/app/public(/.*?\.php)(/.*)$#" PATH_INFO "$2"`,
		`ProxyFCGISetEnvIf "reqenv('SCRIPT_FILENAME') =~ m#^/sites/app/public(/.*?\.php)(?:/.*)?$#" SCRIPT_FILENAME ` + apacheQuote(filepath.Join(f.projects, "app/public")+"$1"),
		`SetHandler "proxy:fcgi://127.0.0.1:43000"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("apache htaccess vhost missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "RewriteRule ^ /index.php/$1") {
		t.Errorf("apache auto+htaccess must not generate a duplicate front-controller rewrite\n%s", out)
	}
	if strings.Contains(out, "Strict-Transport-Security") && !strings.Contains(out, "unset Strict-Transport-Security") {
		t.Error("apache vhost must not emit HSTS")
	}
}

func TestRenderApacheGeneratedFallbackWhenNoHtaccess(t *testing.T) {
	f := newRenderFixture(t)
	f.writeFile(t, "app/public/index.php", "<?php")
	site := f.site("app", "app", "app.test", "app/public")
	site.Kind = model.SitePHP
	site.BackendPort = 43000
	site.FrontController = true
	site.FrontControllerFile = "app.php"
	site.FrontControllerMode = "auto"
	site.ApacheAllowOverride = "none"
	site.DesiredState = model.DesiredRunning
	os.WriteFile(filepath.Join(f.projects, "app/public/app.php"), []byte("<?php"), 0o644)

	out := renderOne(t, ServerApache, baseSet(f, site), "app.conf")
	if !strings.Contains(out, "AllowOverride None") {
		t.Errorf("apache override none missing\n%s", out)
	}
	if !strings.Contains(out, "FallbackResource /app.php") {
		t.Errorf("apache auto+none should generate a front-controller fallback preserving the original URI\n%s", out)
	}
}

func TestRenderApacheHtaccessModeRequiresFileAndOverride(t *testing.T) {
	f := newRenderFixture(t)
	f.writeFile(t, "app/public/index.php", "<?php")
	site := f.site("app", "app", "app.test", "app/public")
	site.Kind = model.SitePHP
	site.BackendPort = 43000
	site.FrontController = true
	site.FrontControllerFile = "index.php"
	site.FrontControllerMode = "htaccess"
	site.ApacheAllowOverride = "all"

	if _, err := Render(ServerApache, baseSet(f, site)); err == nil {
		t.Fatal("expected htaccess mode without .htaccess to fail")
	}

	f.writeFile(t, "app/public/.htaccess", "RewriteEngine On")
	if _, err := Render(ServerApache, baseSet(f, site)); err != nil {
		t.Fatalf("htaccess mode with .htaccess and override all: %v", err)
	}

	site.ApacheAllowOverride = "none"
	if _, err := Render(ServerApache, baseSet(f, site)); err == nil {
		t.Fatal("expected htaccess mode with AllowOverride None to fail")
	}
}

func TestRenderHtaccessModeRejectedByNginx(t *testing.T) {
	f := newRenderFixture(t)
	f.writeFile(t, "app/public/index.php", "<?php")
	f.writeFile(t, "app/public/.htaccess", "RewriteEngine On")
	site := f.site("app", "app", "app.test", "app/public")
	site.Kind = model.SitePHP
	site.BackendPort = 43000
	site.FrontController = true
	site.FrontControllerFile = "index.php"
	site.FrontControllerMode = "htaccess"
	if _, err := Render(ServerNginx, baseSet(f, site)); err == nil {
		t.Fatal("expected nginx + htaccess mode to fail")
	}
}

func TestRenderApacheStaticSPAFallbackKeepsAssets404(t *testing.T) {
	f := newRenderFixture(t)
	f.writeFile(t, "spa/index.html", "ok")
	site := f.site("spa", "spa", "spa.test", "spa")
	site.SPAFallback = true
	site.SPAFallbackFile = "index.html"
	out := renderOne(t, ServerApache, baseSet(f, site), "spa.conf")
	for _, want := range []string{
		`DocumentRoot "/sites/spa"`,
		`RewriteCond %{REQUEST_FILENAME} !-f`,
		`RewriteCond %{REQUEST_FILENAME} !-d`,
		`RewriteCond %{REQUEST_URI} !\.[A-Za-z0-9]{1,10}$`,
		`RewriteRule ^ /index.html [L]`,
		"AllowOverride All",
		`<FilesMatch "(?i)\.php(?:~|\.(?:bak|orig|save|old|swp|swo|tmp))$">`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("apache SPA vhost missing %q\n%s", want, out)
		}
	}
}

func TestRenderRejectsSymlinkedDocumentRootOutsideProjects(t *testing.T) {
	f := newRenderFixture(t)
	outside := filepath.Join(f.root, "outside-site")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "index.html"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(f.projects, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	f.writeFile(t, "placeholder/index.html", "ok")
	site := SiteSpec{
		ID:               "linked",
		Name:             "linked",
		Domain:           "linked.test",
		Kind:             model.SiteStatic,
		HostDocumentRoot: link,
		DesiredState:     model.DesiredRunning,
	}
	if _, err := Render(ServerNginx, baseSet(f, site)); err == nil {
		t.Fatal("expected a symlinked document root outside projects to be rejected")
	} else if !strings.Contains(strings.ToLower(err.Error()), "outside") {
		t.Fatalf("error = %v, want an outside-projects explanation", err)
	}
}

func TestRenderResolvesSymlinkedDocumentRootInsideProjects(t *testing.T) {
	f := newRenderFixture(t)
	f.writeFile(t, "real-site/index.html", "real")
	link := filepath.Join(f.projects, "alias")
	if err := os.Symlink(filepath.Join(f.projects, "real-site"), link); err != nil {
		t.Fatal(err)
	}
	site := SiteSpec{
		ID:               "alias",
		Name:             "alias",
		Domain:           "alias.test",
		Kind:             model.SiteStatic,
		HostDocumentRoot: link,
		DesiredState:     model.DesiredRunning,
	}
	out := renderOne(t, ServerNginx, baseSet(f, site), "alias.conf")
	if !strings.Contains(out, "root /sites/real-site;") {
		t.Fatalf("symlink inside projects should resolve to /sites/real-site\n%s", out)
	}
}

func TestRenderApacheStaticHonorsAllowOverride(t *testing.T) {
	f := newRenderFixture(t)
	f.writeFile(t, "static/index.html", "ok")
	site := f.site("static", "static", "static.test", "static")
	site.ApacheAllowOverride = "all"
	out := renderOne(t, ServerApache, baseSet(f, site), "static.conf")
	if !strings.Contains(out, "AllowOverride All") {
		t.Fatalf("static Apache vhost did not honor apache_allow_override=all\n%s", out)
	}
}

func TestRenderDashboardAndDefault404(t *testing.T) {
	f := newRenderFixture(t)
	for _, server := range []Server{ServerNginx, ServerApache} {
		set := baseSet(f)
		files, err := Render(server, set)
		if err != nil {
			t.Fatalf("Render(%s): %v", server, err)
		}
		dashboard := string(files["_devstack.test.conf"])
		if !strings.Contains(dashboard, "devstack.test") || !strings.Contains(dashboard, "127.0.0.1:7070") {
			t.Errorf("%s dashboard vhost is incomplete\n%s", server, dashboard)
		}
		if !strings.Contains(dashboard, "/certs/__dashboard__.crt") {
			t.Errorf("%s dashboard vhost missing dashboard certificate path\n%s", server, dashboard)
		}
		def := string(files["_default.conf"])
		if !strings.Contains(def, "Site not found") {
			t.Errorf("%s default vhost missing branded 404\n%s", server, def)
		}
		if strings.Contains(def, "Strict-Transport-Security") {
			t.Errorf("%s default vhost must not emit HSTS", server)
		}
	}
}

func TestRenderApacheConfigLoadsRequiredModules(t *testing.T) {
	base, err := RenderBase(ServerApache, 80, 443)
	if err != nil {
		t.Fatalf("RenderBase(apache): %v", err)
	}
	text := string(base)
	for _, module := range []string{
		"mpm_event_module",
		"proxy_module",
		"proxy_http_module",
		"proxy_fcgi_module",
		"proxy_wstunnel_module",
		"rewrite_module",
		"ssl_module",
		"headers_module",
		"setenvif_module",
		"authz_core_module",
		"dir_module",
		"mime_module",
	} {
		if !strings.Contains(text, module) {
			t.Errorf("apache base config missing %s", module)
		}
	}
	for _, want := range []string{"Listen 80", "Listen 443", "IncludeOptional conf/conf.d/generated/*.conf", "IncludeOptional conf/conf.d/custom/*.conf", "ServerRoot \"/usr/local/apache2\""} {
		if !strings.Contains(text, want) {
			t.Errorf("apache base config missing %q", want)
		}
	}
}

func TestRenderNginxBaseIncludesGeneratedAndCustom(t *testing.T) {
	base, err := RenderBase(ServerNginx, 8080, 8443)
	if err != nil {
		t.Fatalf("RenderBase(nginx): %v", err)
	}
	text := string(base)
	for _, want := range []string{
		"include /etc/nginx/conf.d/00-devstack-default.conf;",
		"include /etc/nginx/conf.d/generated/*.conf;",
		"include /etc/nginx/conf.d/custom/*.conf;",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("nginx base config missing %q\n%s", want, text)
		}
	}
}

func TestRenderRejectsUnsupportedServer(t *testing.T) {
	f := newRenderFixture(t)
	if _, err := Render(Server("caddy"), baseSet(f)); err == nil {
		t.Fatal("expected unsupported server error")
	}
}
