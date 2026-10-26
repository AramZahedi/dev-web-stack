//go:build ignore

// render_fixture renders the Docker web-server config tree used by
// test/docker/wave1-web.sh. It is a standalone helper and is not part of the
// devstack binary or the unit test suite.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AramZahedi/dev-web-stack/internal/dockerweb"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func main() {
	server := flag.String("server", "nginx", "nginx or apache")
	root := flag.String("root", "", "fixture root")
	httpPort := flag.Int("http", 18080, "HTTP listen port")
	httpsPort := flag.Int("https", 18443, "HTTPS listen port")
	phpPort := flag.Int("php", 19000, "PHP-FPM port")
	nodePort := flag.Int("node", 17073, "Node upstream port")
	dashboardPort := flag.Int("dashboard", 17070, "dashboard upstream port")
	mode := flag.String("mode", "htaccess", "htaccess or generated Apache front-controller mode")
	out := flag.String("out", "", "output directory")
	flag.Parse()
	if *root == "" || *out == "" {
		fatalf("-root and -out are required")
	}
	srv := dockerweb.Server(*server)
	if err := srv.Validate(); err != nil {
		fatalf("%v", err)
	}

	projects := filepath.Join(*root, "projects")
	sites := []dockerweb.SiteSpec{
		{
			ID:                  "laravel",
			Name:                "laravel",
			Domain:              "laravel.test",
			WWW:                 true,
			Kind:                model.SitePHP,
			HostDocumentRoot:    filepath.Join(projects, "laravel-min", "public"),
			BackendPort:         *phpPort,
			TLS:                 true,
			ForceHTTPS:          true,
			FrontController:     true,
			FrontControllerFile: "index.php",
			DesiredState:        model.DesiredRunning,
		},
		{
			ID:           "node",
			Name:         "node",
			Domain:       "node.test",
			Kind:         model.SiteNode,
			BackendPort:  *nodePort,
			TLS:          true,
			DesiredState: model.DesiredRunning,
		},
		{
			ID:               "nuxt",
			Name:             "nuxt",
			Domain:           "nuxt.test",
			Kind:             model.SiteStatic,
			HostDocumentRoot: filepath.Join(projects, "nuxt-static"),
			TLS:              true,
			SPAFallback:      true,
			SPAFallbackFile:  "index.html",
			DesiredState:     model.DesiredRunning,
		},
		{
			ID:               "stopped",
			Name:             "stopped",
			Domain:           "stopped.test",
			Kind:             model.SiteStatic,
			HostDocumentRoot: filepath.Join(projects, "stopped"),
			TLS:              true,
			DesiredState:     model.DesiredStopped,
		},
	}
	if srv == dockerweb.ServerNginx {
		sites[0].FrontControllerMode = "auto"
	} else if *mode == "generated" {
		sites[0].FrontControllerMode = "generated"
		sites[0].ApacheAllowOverride = "none"
	} else {
		sites[0].FrontControllerMode = "htaccess"
		sites[0].ApacheAllowOverride = "all"
	}

	set := dockerweb.Set{
		Projects:        projects,
		CertsDir:        "/certs",
		Sites:           sites,
		Dashboard:       dockerweb.DashboardSpec{Domain: "devstack.test", UIPort: *dashboardPort},
		MaxBodyBytes:    64 << 20,
		HTTPListenPort:  *httpPort,
		HTTPSListenPort: *httpsPort,
	}
	base, err := dockerweb.RenderBase(srv, *httpPort, *httpsPort)
	if err != nil {
		fatalf("render base: %v", err)
	}
	files, err := dockerweb.Render(srv, set)
	if err != nil {
		fatalf("render vhosts: %v", err)
	}

	generated := filepath.Join(*out, "conf.d", "generated")
	if err := os.MkdirAll(generated, 0o755); err != nil {
		fatalf("mkdir generated: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(*out, "logs"), 0o755); err != nil {
		fatalf("mkdir logs: %v", err)
	}
	if srv == dockerweb.ServerNginx {
		// EnsureBase seeds these safe snippets in production because the base
		// nginx config includes them unconditionally. Mirror that here so the
		// live fixture parses before the performance flow runs.
		perfDir := filepath.Join(*out, "conf.d", "performance")
		if err := os.MkdirAll(perfDir, 0o755); err != nil {
			fatalf("mkdir performance includes: %v", err)
		}
		performanceFiles := map[string]string{
			"main.conf":   "worker_processes 2;\nworker_rlimit_nofile 4096;\n",
			"events.conf": "worker_connections 2048;\n",
			"http.conf":   "sendfile on;\ntcp_nopush on;\nkeepalive_timeout 30s;\nkeepalive_requests 1000;\ngzip on;\ngzip_comp_level 2;\ngzip_min_length 1024;\ngzip_types text/plain text/css application/json application/javascript application/xml image/svg+xml;\n",
		}
		for name, content := range performanceFiles {
			if err := os.WriteFile(filepath.Join(perfDir, name), []byte(content), 0o644); err != nil {
				fatalf("write performance %s: %v", name, err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(*out, "base.conf"), base, 0o644); err != nil {
		fatalf("write base: %v", err)
	}
	if err := os.WriteFile(filepath.Join(*out, "conf.d", "00-devstack-default.conf"), files["_default.conf"], 0o644); err != nil {
		fatalf("write default vhost: %v", err)
	}
	for name, data := range files {
		if name == "_default.conf" {
			continue
		}
		if err := os.WriteFile(filepath.Join(generated, name), data, 0o644); err != nil {
			fatalf("write %s: %v", name, err)
		}
	}
	fmt.Printf("rendered %s config into %s\n", srv, *out)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
