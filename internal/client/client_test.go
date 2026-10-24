package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/daemon"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
)

// fakeAPI is a minimal Unix-socket API used by the client tests.
type fakeAPI struct {
	mu     sync.Mutex
	sites  map[string]model.Site
	routes []recordedRequest
	err    error
	delay  time.Duration
}

type recordedRequest struct {
	method string
	path   string
	query  string
	body   string
}

func (f *fakeAPI) record(r *http.Request) {
	data, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.routes = append(f.routes, recordedRequest{
		method: r.Method,
		path:   r.URL.Path,
		query:  r.URL.RawQuery,
		body:   string(data),
	})
	f.mu.Unlock()
}

func (f *fakeAPI) last(t *testing.T, method, path string) recordedRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.routes) - 1; i >= 0; i-- {
		if f.routes[i].method == method && f.routes[i].path == path {
			return f.routes[i]
		}
	}
	t.Fatalf("no %s %s request recorded: %+v", method, path, f.routes)
	return recordedRequest{}
}

func (f *fakeAPI) lastBody() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.routes) == 0 {
		return ""
	}
	return f.routes[len(f.routes)-1].body
}

func (f *fakeAPI) writeErrorOr(w http.ResponseWriter, ok func()) {
	if f.err != nil {
		writeTestJSON(w, http.StatusConflict, map[string]any{
			"code":    "DOMAIN_CONFLICT",
			"message": f.err.Error(),
			"details": map[string]any{"field": "name"},
		})
		return
	}
	ok()
}

func writeTestJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func startFakeAPI(t *testing.T, home string, api *fakeAPI) {
	t.Helper()
	if api.sites == nil {
		api.sites = map[string]model.Site{}
	}
	socket := daemon.SocketPath(home)
	if err := os.MkdirAll(filepath.Dir(socket), 0o755); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: api.mux()}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
}

func (f *fakeAPI) mux() *http.ServeMux {
	if f.sites == nil {
		f.sites = map[string]model.Site{}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sites", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		if f.delay > 0 {
			time.Sleep(f.delay)
		}
		f.writeErrorOr(w, func() {
			f.mu.Lock()
			list := make([]model.Site, 0, len(f.sites))
			for _, site := range f.sites {
				list = append(list, site)
			}
			f.mu.Unlock()
			writeTestJSON(w, http.StatusOK, list)
		})
	})
	mux.HandleFunc("POST /api/v1/sites", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() {
			var req struct {
				Name string `json:"name"`
				Path string `json:"path"`
				Kind string `json:"kind"`
			}
			if err := json.Unmarshal([]byte(f.lastBody()), &req); err != nil {
				writeTestJSON(w, http.StatusBadRequest, map[string]any{"code": "INVALID_REQUEST", "message": err.Error(), "details": nil})
				return
			}
			site := model.Site{ID: "site-1", Name: req.Name, Domain: req.Name + ".test", Kind: model.SiteKind(req.Kind), HostPath: req.Path}
			f.mu.Lock()
			f.sites[site.ID] = site
			f.mu.Unlock()
			writeTestJSON(w, http.StatusCreated, site)
		})
	})
	mux.HandleFunc("GET /api/v1/sites/{ref}", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() {
			f.mu.Lock()
			site, ok := f.sites[r.PathValue("ref")]
			f.mu.Unlock()
			if !ok {
				writeTestJSON(w, http.StatusNotFound, map[string]any{"code": "NOT_FOUND", "message": "missing", "details": nil})
				return
			}
			writeTestJSON(w, http.StatusOK, site)
		})
	})
	mux.HandleFunc("PATCH /api/v1/sites/{ref}", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() {
			f.mu.Lock()
			site, ok := f.sites[r.PathValue("ref")]
			f.mu.Unlock()
			if !ok {
				writeTestJSON(w, http.StatusNotFound, map[string]any{"code": "NOT_FOUND", "message": "missing", "details": nil})
				return
			}
			var req struct {
				Domain string `json:"domain"`
			}
			_ = json.Unmarshal([]byte(f.lastBody()), &req)
			if req.Domain != "" {
				site.Domain = req.Domain
			}
			f.mu.Lock()
			f.sites[site.ID] = site
			f.mu.Unlock()
			writeTestJSON(w, http.StatusOK, site)
		})
	})
	mux.HandleFunc("DELETE /api/v1/sites/{ref}", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() {
			f.mu.Lock()
			delete(f.sites, r.PathValue("ref"))
			f.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		})
	})
	mux.HandleFunc("POST /api/v1/sites/{ref}/{action}", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() { w.WriteHeader(http.StatusAccepted) })
	})
	mux.HandleFunc("GET /api/v1/sites/{ref}/php", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() {
			writeTestJSON(w, http.StatusOK, sites.PHPInfo{
				Site:         model.Site{ID: "site-1", Name: "blog", Kind: model.SitePHP, PHPVersion: "8.4", PHPBuild: "php@8.4.4-r1"},
				GeneratedIni: "memory_limit = 128M\n",
				Ini:          map[string]string{"memory_limit": "128M"},
			})
		})
	})
	mux.HandleFunc("PUT /api/v1/sites/{ref}/php", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() {
			writeTestJSON(w, http.StatusOK, model.Site{ID: "site-1", Name: "blog", Kind: model.SitePHP, PHPVersion: "8.5", PHPBuild: "php@8.5.0-r1"})
		})
	})
	mux.HandleFunc("POST /api/v1/sites/{ref}/php/exec", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() {
			writeTestJSON(w, http.StatusOK, map[string]any{
				"exit_code": 2,
				"stdout":    "PHP 8.4.4\n",
				"stderr":    "warning\n",
			})
		})
	})
	mux.HandleFunc("GET /api/v1/php/extensions", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() {
			writeTestJSON(w, http.StatusOK, []catalog.Extension{{
				Name: "curl", Type: catalog.ExtensionShared, SO: "curl.so", Default: true, Versions: []string{"8.4"},
			}})
		})
	})
	mux.HandleFunc("GET /api/v1/php/defaults/{version}", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() {
			writeTestJSON(w, http.StatusOK, model.PHPDefaults{
				Version:    r.PathValue("version"),
				Ini:        map[string]string{"memory_limit": "256M"},
				Extensions: map[string]bool{"xdebug": false},
			})
		})
	})
	mux.HandleFunc("PUT /api/v1/php/defaults/{version}", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() {
			version := r.PathValue("version")
			writeTestJSON(w, http.StatusOK, sites.PHPDefaultsResult{
				Defaults: model.PHPDefaults{Version: version},
				Sites:    []sites.PHPMigrationResult{{Site: "blog", OK: true}},
			})
		})
	})
	mux.HandleFunc("POST /api/v1/php/migrate", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() {
			writeTestJSON(w, http.StatusOK, []sites.PHPMigrationResult{{Site: "blog", OK: true}})
		})
	})
	mux.HandleFunc("GET /api/v1/sites/{ref}/logs/stream", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "data: {\"text\":\"hello\"}\n\n")
		})
	})
	mux.HandleFunc("GET /api/v1/doctor", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() { writeTestJSON(w, http.StatusOK, map[string]any{"checks": []any{}, "ok": true}) })
	})
	mux.HandleFunc("POST /api/v1/doctor/repair", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() { writeTestJSON(w, http.StatusOK, map[string]any{"checks": []any{}, "ok": true}) })
	})
	mux.HandleFunc("GET /api/v1/trust", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() { writeTestJSON(w, http.StatusOK, []any{}) })
	})
	mux.HandleFunc("POST /api/v1/trust/repair", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() { writeTestJSON(w, http.StatusOK, []any{}) })
	})
	mux.HandleFunc("DELETE /api/v1/trust", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() { writeTestJSON(w, http.StatusOK, []any{}) })
	})
	mux.HandleFunc("GET /api/v1/runtimes", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() { writeTestJSON(w, http.StatusOK, []any{}) })
	})
	mux.HandleFunc("GET /api/v1/runtimes/available", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() { writeTestJSON(w, http.StatusOK, []any{}) })
	})
	mux.HandleFunc("POST /api/v1/runtimes/install", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() {
			writeTestJSON(w, http.StatusCreated, model.RuntimeBuild{ID: "node-24", Kind: "node", Version: "24"})
		})
	})
	mux.HandleFunc("DELETE /api/v1/runtimes/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() { w.WriteHeader(http.StatusNoContent) })
	})
	mux.HandleFunc("GET /api/v1/vhosts/status", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() {
			writeTestJSON(w, http.StatusOK, helperproto.VhostStatusResponse{
				Mode: "nginx", Server: "nginx", ServerVersion: "1.26.0", ConfigOK: true,
			})
		})
	})
	mux.HandleFunc("POST /api/v1/vhosts/mode", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() {
			var req struct {
				Mode string `json:"mode"`
			}
			_ = json.Unmarshal([]byte(f.lastBody()), &req)
			writeTestJSON(w, http.StatusOK, helperproto.VhostStatusResponse{Mode: req.Mode, ConfigOK: true})
		})
	})
	mux.HandleFunc("POST /api/v1/vhosts/sync", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() {
			writeTestJSON(w, http.StatusOK, helperproto.VhostStatusResponse{Mode: "nginx", ConfigOK: true})
		})
	})
	mux.HandleFunc("GET /api/v1/vhosts/preview", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeErrorOr(w, func() {
			writeTestJSON(w, http.StatusOK, map[string]string{
				"server":  r.URL.Query().Get("server"),
				"site":    r.URL.Query().Get("site"),
				"content": "# preview\nserver_name " + r.URL.Query().Get("site") + ".test;\n",
			})
		})
	})
	return mux
}

func TestClientSitesCRUD(t *testing.T) {
	home := t.TempDir()
	startFakeAPI(t, home, &fakeAPI{sites: map[string]model.Site{}})
	c, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	site, err := c.AddSite(context.Background(), sites.CreateRequest{Name: "blog", Path: t.TempDir(), Kind: "static"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := c.ListSites(context.Background())
	if err != nil || len(list) != 1 || list[0].Domain != "blog.test" {
		t.Fatalf("%+v %v", list, err)
	}
	if err := c.DeleteSite(context.Background(), site.ID); err != nil {
		t.Fatal(err)
	}
	if list, err = c.ListSites(context.Background()); err != nil || len(list) != 0 {
		t.Fatalf("site not deleted: %+v %v", list, err)
	}
}

func TestClientRoutesAndPayloads(t *testing.T) {
	home := t.TempDir()
	api := &fakeAPI{sites: map[string]model.Site{}}
	startFakeAPI(t, home, api)
	c, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	site, err := c.AddSite(ctx, sites.CreateRequest{Name: "blog", Path: "/tmp/blog", Kind: "static", SPAFallback: true})
	if err != nil {
		t.Fatal(err)
	}
	create := api.last(t, http.MethodPost, "/api/v1/sites")
	if !strings.Contains(create.body, `"path":"/tmp/blog"`) || !strings.Contains(create.body, `"spa_fallback":true`) {
		t.Fatalf("create payload = %s", create.body)
	}

	if _, err := c.GetSite(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	api.last(t, http.MethodGet, "/api/v1/sites/"+site.ID)

	www := false
	if _, err := c.UpdateSite(ctx, site.ID, sites.UpdateRequest{Domain: "hello.test", WWW: &www}); err != nil {
		t.Fatal(err)
	}
	update := api.last(t, http.MethodPatch, "/api/v1/sites/"+site.ID)
	if !strings.Contains(update.body, `"domain":"hello.test"`) || !strings.Contains(update.body, `"www":false`) {
		t.Fatalf("update payload = %s", update.body)
	}

	if err := c.SiteAction(ctx, site.ID, "restart"); err != nil {
		t.Fatal(err)
	}
	api.last(t, http.MethodPost, "/api/v1/sites/"+site.ID+"/restart")

	logs, err := c.Logs(ctx, site.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := io.ReadAll(logs)
	_ = logs.Close()
	if err != nil || !strings.Contains(string(stream), "hello") {
		t.Fatalf("logs = %q %v", stream, err)
	}
	logReq := api.last(t, http.MethodGet, "/api/v1/sites/"+site.ID+"/logs/stream")
	if !strings.Contains(logReq.query, "follow=false") {
		t.Fatalf("logs query = %q", logReq.query)
	}

	live, err := c.Logs(ctx, site.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	stream, err = io.ReadAll(live)
	_ = live.Close()
	if err != nil || !strings.Contains(string(stream), "hello") {
		t.Fatalf("live logs = %q %v", stream, err)
	}
	liveReq := api.last(t, http.MethodGet, "/api/v1/sites/"+site.ID+"/logs/stream")
	if !strings.Contains(liveReq.query, "follow=true") {
		t.Fatalf("live logs query = %q", liveReq.query)
	}

	if report, err := c.Doctor(ctx); err != nil || !report.OK {
		t.Fatalf("doctor = %+v %v", report, err)
	}
	if report, err := c.Repair(ctx); err != nil || !report.OK {
		t.Fatalf("repair = %+v %v", report, err)
	}
	if targets, err := c.TrustStatus(ctx); err != nil || len(targets) != 0 {
		t.Fatalf("trust status = %+v %v", targets, err)
	}
	if targets, err := c.TrustRepair(ctx); err != nil || len(targets) != 0 {
		t.Fatalf("trust repair = %+v %v", targets, err)
	}
	if targets, err := c.TrustRemove(ctx); err != nil || len(targets) != 0 {
		t.Fatalf("trust remove = %+v %v", targets, err)
	}
	if builds, err := c.Runtimes(ctx); err != nil || len(builds) != 0 {
		t.Fatalf("runtimes = %+v %v", builds, err)
	}
	if entries, err := c.AvailableRuntimes(ctx); err != nil || len(entries) != 0 {
		t.Fatalf("available = %+v %v", entries, err)
	}
	build, err := c.InstallRuntime(ctx, "node-24")
	if err != nil || build.ID != "node-24" {
		t.Fatalf("install = %+v %v", build, err)
	}
	install := api.last(t, http.MethodPost, "/api/v1/runtimes/install")
	if !strings.Contains(install.body, `"id":"node-24"`) {
		t.Fatalf("install payload = %s", install.body)
	}
	if err := c.RemoveRuntime(ctx, "node-24"); err != nil {
		t.Fatal(err)
	}
	api.last(t, http.MethodDelete, "/api/v1/runtimes/node-24")

	if err := c.DeleteSite(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	api.last(t, http.MethodDelete, "/api/v1/sites/"+site.ID)
}

func TestClientPHPApplyAndExec(t *testing.T) {
	home := t.TempDir()
	api := &fakeAPI{sites: map[string]model.Site{}}
	startFakeAPI(t, home, api)
	c, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	info, err := c.PHPInfo(ctx, "blog")
	if err != nil || info.Site.Name != "blog" || !strings.Contains(info.GeneratedIni, "memory_limit") {
		t.Fatalf("info = %+v %v", info, err)
	}
	api.last(t, http.MethodGet, "/api/v1/sites/blog/php")

	raw := "display_errors = On\n"
	site, err := c.ApplyPHP(ctx, "blog", sites.PHPChange{
		Version:    "8.5",
		Build:      "php@8.5.0-r1",
		Extensions: map[string]bool{"xdebug": false},
		Ini:        map[string]string{"memory_limit": "512M"},
		IniRaw:     &raw,
	})
	if err != nil || site.PHPVersion != "8.5" {
		t.Fatalf("apply = %+v %v", site, err)
	}
	apply := api.last(t, http.MethodPut, "/api/v1/sites/blog/php")
	for _, want := range []string{`"version":"8.5"`, `"build":"php@8.5.0-r1"`, `"xdebug":false`, `"memory_limit":"512M"`, `"ini_raw":"display_errors = On\n"`} {
		if !strings.Contains(apply.body, want) {
			t.Fatalf("apply payload missing %s: %s", want, apply.body)
		}
	}

	if _, err := c.ApplyPHP(ctx, "blog", sites.PHPChange{Extensions: map[string]bool{}, Ini: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	clear := api.last(t, http.MethodPut, "/api/v1/sites/blog/php")
	if !strings.Contains(clear.body, `"extensions":{}`) || !strings.Contains(clear.body, `"ini":{}`) {
		t.Fatalf("empty maps must clear overrides: %s", clear.body)
	}
	if strings.Contains(clear.body, "ini_raw") {
		t.Fatalf("nil ini_raw must be omitted: %s", clear.body)
	}

	result, err := c.PHPExec(ctx, "blog", []string{"-v"})
	if err != nil || result.ExitCode != 2 || result.Stdout != "PHP 8.4.4\n" || result.Stderr != "warning\n" {
		t.Fatalf("exec = %+v %v", result, err)
	}
	exec := api.last(t, http.MethodPost, "/api/v1/sites/blog/php/exec")
	if !strings.Contains(exec.body, `"argv":["-v"]`) {
		t.Fatalf("exec payload = %s", exec.body)
	}
}

func TestClientPHPRoutingAndCreatePayload(t *testing.T) {
	home := t.TempDir()
	api := &fakeAPI{sites: map[string]model.Site{}}
	startFakeAPI(t, home, api)
	c, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	extensions, err := c.PHPExtensions(ctx, "8.4")
	if err != nil || len(extensions) != 1 || extensions[0].Name != "curl" {
		t.Fatalf("extensions = %+v %v", extensions, err)
	}
	extReq := api.last(t, http.MethodGet, "/api/v1/php/extensions")
	if extReq.query != "version=8.4" {
		t.Fatalf("extensions query = %q", extReq.query)
	}

	defaults, err := c.PHPDefaults(ctx, "8.4")
	if err != nil || defaults.Version != "8.4" || defaults.Ini["memory_limit"] != "256M" {
		t.Fatalf("defaults = %+v %v", defaults, err)
	}
	api.last(t, http.MethodGet, "/api/v1/php/defaults/8.4")

	result, err := c.SavePHPDefaults(ctx, model.PHPDefaults{
		Version:    "8.4",
		Ini:        map[string]string{"memory_limit": "1G"},
		Extensions: map[string]bool{"xdebug": true},
	})
	if err != nil || result.Defaults.Version != "8.4" || len(result.Sites) != 1 {
		t.Fatalf("save defaults = %+v %v", result, err)
	}
	save := api.last(t, http.MethodPut, "/api/v1/php/defaults/8.4")
	if !strings.Contains(save.body, `"memory_limit":"1G"`) || !strings.Contains(save.body, `"xdebug":true`) {
		t.Fatalf("save defaults payload = %s", save.body)
	}

	migration, err := c.MigratePHP(ctx, sites.PHPMigrationRequest{FromVersion: "8.4", ToVersion: "8.5", ToBuild: "php@8.5.0-r1"})
	if err != nil || len(migration) != 1 || !migration[0].OK {
		t.Fatalf("migration = %+v %v", migration, err)
	}
	migrate := api.last(t, http.MethodPost, "/api/v1/php/migrate")
	if !strings.Contains(migrate.body, `"from_version":"8.4"`) || !strings.Contains(migrate.body, `"to_build":"php@8.5.0-r1"`) {
		t.Fatalf("migrate payload = %s", migrate.body)
	}

	if _, err := c.AddSite(ctx, sites.CreateRequest{
		Name: "blog", Path: "/tmp/blog", Kind: "php",
		PHPVersion: "8.4", PHPBuild: "php@8.4.4-r1",
		PHPExtensions:   map[string]bool{"xdebug": true},
		PHPIni:          map[string]string{"memory_limit": "512M"},
		PHPIniRaw:       "display_errors = On\n",
		FrontController: true,
	}); err != nil {
		t.Fatal(err)
	}
	create := api.last(t, http.MethodPost, "/api/v1/sites")
	for _, want := range []string{`"php_version":"8.4"`, `"php_build":"php@8.4.4-r1"`, `"php_extensions":{"xdebug":true}`, `"php_ini_raw":"display_errors = On\n"`, `"front_controller":true`} {
		if !strings.Contains(create.body, want) {
			t.Fatalf("create payload missing %s: %s", want, create.body)
		}
	}

	front := false
	if _, err := c.UpdateSite(ctx, "site-1", sites.UpdateRequest{FrontController: &front}); err != nil {
		t.Fatal(err)
	}
	update := api.last(t, http.MethodPatch, "/api/v1/sites/site-1")
	if !strings.Contains(update.body, `"front_controller":false`) {
		t.Fatalf("update payload = %s", update.body)
	}
}

func TestClientVhostMethods(t *testing.T) {
	home := t.TempDir()
	api := &fakeAPI{sites: map[string]model.Site{}}
	startFakeAPI(t, home, api)
	c, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	status, err := c.VhostStatus(ctx)
	if err != nil || status.Mode != "nginx" || status.ServerVersion != "1.26.0" {
		t.Fatalf("status = %+v %v", status, err)
	}
	api.last(t, http.MethodGet, "/api/v1/vhosts/status")

	status, err = c.SetServingMode(ctx, "apache")
	if err != nil || status.Mode != "apache" {
		t.Fatalf("mode = %+v %v", status, err)
	}
	mode := api.last(t, http.MethodPost, "/api/v1/vhosts/mode")
	if !strings.Contains(mode.body, `"mode":"apache"`) {
		t.Fatalf("mode payload = %s", mode.body)
	}

	status, err = c.SyncVhosts(ctx)
	if err != nil || status.Mode != "nginx" {
		t.Fatalf("sync = %+v %v", status, err)
	}
	api.last(t, http.MethodPost, "/api/v1/vhosts/sync")

	content, err := c.PreviewVhost(ctx, "blog", "apache")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "server_name blog.test;") {
		t.Fatalf("preview content = %q", content)
	}
	preview := api.last(t, http.MethodGet, "/api/v1/vhosts/preview")
	if !strings.Contains(preview.query, "site=blog") || !strings.Contains(preview.query, "server=apache") {
		t.Fatalf("preview query = %q", preview.query)
	}
}

func TestClientErrorMapping(t *testing.T) {
	home := t.TempDir()
	api := &fakeAPI{sites: map[string]model.Site{}, err: errors.New("blog.test is already used")}
	startFakeAPI(t, home, api)
	c, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	_, err = c.AddSite(context.Background(), sites.CreateRequest{Name: "blog", Path: "/tmp/blog", Kind: "static"})
	if err == nil {
		t.Fatal("AddSite succeeded against an error response")
	}
	var clientErr *Error
	if !errors.As(err, &clientErr) {
		t.Fatalf("error type = %T, want *client.Error", err)
	}
	if clientErr.Code != "DOMAIN_CONFLICT" || !strings.Contains(clientErr.Message, "already used") {
		t.Fatalf("error = %+v", clientErr)
	}
	details, ok := clientErr.Details.(map[string]any)
	if !ok || details["field"] != "name" {
		t.Fatalf("details = %#v", clientErr.Details)
	}
}

func TestNewRequiresSocket(t *testing.T) {
	home := t.TempDir()
	if _, err := New(home); err == nil {
		t.Fatal("New accepted a missing socket")
	}
	if err := os.MkdirAll(filepath.Dir(daemon.SocketPath(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemon.SocketPath(home), []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(home); err == nil {
		t.Fatal("New accepted a regular file as a socket")
	}
}

func TestClientActionValidation(t *testing.T) {
	home := t.TempDir()
	startFakeAPI(t, home, &fakeAPI{sites: map[string]model.Site{}})
	c, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err := c.SiteAction(context.Background(), "site-1", "explode"); err == nil {
		t.Fatal("SiteAction accepted an unknown action")
	}
}

func TestClientNewResolvesEnvironmentHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEVSTACK_HOME", home)
	startFakeAPI(t, home, &fakeAPI{sites: map[string]model.Site{}})

	c, err := New("")
	if err != nil {
		t.Fatalf("New with DEVSTACK_HOME: %v", err)
	}
	defer func() { _ = c.Close() }()
	if got, want := c.SocketPath(), daemon.SocketPath(home); got != want {
		t.Fatalf("socket = %q, want %q", got, want)
	}
}

func TestClientNewResolvesUserHomeDefault(t *testing.T) {
	userHome := t.TempDir()
	t.Setenv("DEVSTACK_HOME", "")
	t.Setenv("HOME", userHome)
	home := filepath.Join(userHome, ".devstack")
	startFakeAPI(t, home, &fakeAPI{sites: map[string]model.Site{}})

	c, err := New("")
	if err != nil {
		t.Fatalf("New with HOME: %v", err)
	}
	defer func() { _ = c.Close() }()
	if got, want := c.SocketPath(), daemon.SocketPath(home); got != want {
		t.Fatalf("socket = %q, want %q", got, want)
	}
}

func TestClientNonStreamingRequestTimeout(t *testing.T) {
	home := t.TempDir()
	startFakeAPI(t, home, &fakeAPI{sites: map[string]model.Site{}, delay: 300 * time.Millisecond})
	c, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	c.timeout = 50 * time.Millisecond

	if _, err := c.ListSites(context.Background()); err == nil {
		t.Fatal("ListSites did not time out")
	} else if !strings.Contains(strings.ToLower(err.Error()), "deadline") {
		t.Fatalf("timeout error = %v", err)
	}
}
