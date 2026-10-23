package sites

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/backend"
	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/phpini"
	"github.com/AramZahedi/dev-web-stack/internal/proxy"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

func TestCreatePHPInvalidManifestIsAnActionableError(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	svc.deps.PHPManifest = &catalog.PHPManifest{}

	_, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "PHP") && !strings.Contains(err.Error(), "php") {
		t.Fatalf("error is not actionable: %v", err)
	}
	if sites, _ := svc.List(ctx); len(sites) != 0 {
		t.Fatal("site persisted with an invalid manifest")
	}
}

func TestCreatePHPImplicitBuildIgnoresNonPHPRuntimeRows(t *testing.T) {
	svc, fakes := newTestService(t)
	if err := fakes.store.UpsertRuntime(ctx, model.RuntimeBuild{
		ID:      "node@8.4.4",
		Kind:    "node",
		Version: "8.4.4",
		Backend: model.BackendLocal,
		Path:    fakes.root,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"}); !errors.Is(err, ErrRuntimeMissing) {
		t.Fatalf("err = %v", err)
	}
}

func TestCreatePHPSiteStartsFPMAndPinsBuild(t *testing.T) {
	svc, fakes := newTestService(t)
	build := fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php",
		PHPVersion: "8.4", PHPBuild: build.ID, DocumentRoot: "."})
	if err != nil {
		t.Fatal(err)
	}
	if site.PHPBuild != build.ID || site.PHPConfigHash == "" {
		t.Fatalf("%+v", site)
	}
	if site.FastCGIPort < 43000 || site.FastCGIPort > 43999 {
		t.Fatalf("port = %d", site.FastCGIPort)
	}
	if site.HTTPPort != 0 {
		t.Fatalf("PHP site allocated an HTTP port: %d", site.HTTPPort)
	}
	spec, ok := fakes.backend.lastPrepared()
	if !ok || spec.PHPBuild != build.ID || spec.PHPIniPath == "" || spec.FPMConfigPath == "" {
		t.Fatalf("%+v", spec)
	}
	if spec.PHPBuildPath != build.Path || spec.PHPHealthScript == "" {
		t.Fatalf("spec = %+v", spec)
	}
	if spec.PHPVersion != "8.4" || spec.FastCGIPort != site.FastCGIPort || spec.DocumentRoot != "." {
		t.Fatalf("spec = %+v", spec)
	}
	if fakes.php.lintCalls == 0 || fakes.php.fpmCalls == 0 {
		t.Fatal("validation was not run")
	}
	if !fakes.backend.started[site.ID] {
		t.Fatal("FPM master was not started")
	}
	stored, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.PHPConfigHash != site.PHPConfigHash || stored.PHPBuild != build.ID || stored.FastCGIPort != site.FastCGIPort {
		t.Fatalf("stored site = %+v", stored)
	}
	route, ok := fakes.router.Lookup("blog.test")
	if !ok {
		t.Fatal("route missing")
	}
	if route.Kind != proxy.KindPHP {
		t.Fatalf("route kind = %q", route.Kind)
	}
	if want := "127.0.0.1:" + strconv.Itoa(site.FastCGIPort); route.Upstream != want {
		t.Fatalf("route upstream = %q, want %q", route.Upstream, want)
	}
	if route.DocumentRoot != filepath.Clean(fakes.root) {
		t.Fatalf("route document root = %q", route.DocumentRoot)
	}
}

func TestCreatePHPRequiresInstalledBuild(t *testing.T) {
	t.Run("no build installed", func(t *testing.T) {
		svc, fakes := newTestService(t)
		_, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
		if !errors.Is(err, ErrRuntimeMissing) {
			t.Fatalf("err = %v", err)
		}
		if sites, _ := svc.List(ctx); len(sites) != 0 {
			t.Fatal("site persisted")
		}
		if lint, fpm := fakes.php.counts(); lint != 0 || fpm != 0 {
			t.Fatalf("validators ran without a build: %d/%d", lint, fpm)
		}
	})

	t.Run("explicit build is not installed", func(t *testing.T) {
		svc, fakes := newTestService(t)
		fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
		_, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php",
			PHPVersion: "8.4", PHPBuild: "php@8.4.9-r1"})
		if !errors.Is(err, ErrRuntimeMissing) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("explicit build has a different line", func(t *testing.T) {
		svc, fakes := newTestService(t)
		fakes.installPHPBuild("php@8.3.9-r1", "8.3.9")
		_, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php",
			PHPVersion: "8.4", PHPBuild: "php@8.3.9-r1"})
		if !errors.Is(err, ErrRuntimeMissing) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("implicit build ignores other lines", func(t *testing.T) {
		svc, fakes := newTestService(t)
		fakes.installPHPBuild("php@8.3.9-r1", "8.3.9")
		_, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
		if !errors.Is(err, ErrRuntimeMissing) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestCreatePHPSelectsNewestInstalledBuild(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	newest := fakes.installPHPBuild("php@8.4.5-r2", "8.4.5")
	fakes.installPHPBuild("php@8.4.5-r1", "8.4.5")
	fakes.installPHPBuild("php@8.5.0-r1", "8.5.0")

	site, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	if err != nil {
		t.Fatal(err)
	}
	if site.PHPBuild != newest.ID {
		t.Fatalf("selected build = %q, want %q", site.PHPBuild, newest.ID)
	}
	spec, _ := fakes.backend.lastPrepared()
	if spec.PHPBuildPath != newest.Path {
		t.Fatalf("build path = %q, want %q", spec.PHPBuildPath, newest.Path)
	}

	older, err := svc.Create(ctx, CreateRequest{Name: "shop", Path: fakes.root, Kind: "php",
		PHPVersion: "8.4", PHPBuild: "php@8.4.4-r1"})
	if err != nil {
		t.Fatal(err)
	}
	if older.PHPBuild != "php@8.4.4-r1" {
		t.Fatalf("explicit build = %q", older.PHPBuild)
	}
}

func TestCreatePHPRejectsPHPOnlyFieldsOnOtherKinds(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  CreateRequest
	}{
		{name: "version", req: CreateRequest{Name: "blog", Kind: "static", PHPVersion: "8.4"}},
		{name: "build", req: CreateRequest{Name: "blog", Kind: "static", PHPBuild: "php@8.4.4-r1"}},
		{name: "extensions", req: CreateRequest{Name: "blog", Kind: "static", PHPExtensions: map[string]bool{"xdebug": true}}},
		{name: "ini", req: CreateRequest{Name: "blog", Kind: "static", PHPIni: map[string]string{"memory_limit": "512M"}}},
		{name: "raw ini", req: CreateRequest{Name: "blog", Kind: "static", PHPIniRaw: "memory_limit = 512M\n"}},
		{name: "front controller on static", req: CreateRequest{Name: "blog", Kind: "static", FrontController: true}},
		{name: "front controller on node", req: CreateRequest{Name: "blog", Kind: "node", NodeVersion: "24", Command: "npm run dev", FrontController: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, fakes := newTestService(t)
			tc.req.Path = fakes.root
			if _, err := svc.Create(ctx, tc.req); err == nil {
				t.Fatal("want error")
			} else if !strings.Contains(err.Error(), "PHP") && !strings.Contains(err.Error(), "php") {
				t.Fatalf("error = %v", err)
			}
			if sites, _ := svc.List(ctx); len(sites) != 0 {
				t.Fatalf("site persisted: %+v", sites)
			}
		})
	}
}

func TestCreatePHPValidatesVersionDocumentRootAndConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  CreateRequest
		want string
	}{
		{name: "missing version", req: CreateRequest{Name: "blog", Kind: "php"}, want: "requires a version"},
		{name: "unsupported version", req: CreateRequest{Name: "blog", Kind: "php", PHPVersion: "8.1"}, want: "unsupported PHP version"},
		{name: "escaping document root", req: CreateRequest{Name: "blog", Kind: "php", PHPVersion: "8.4", DocumentRoot: "../escape"}, want: "escapes"},
		{name: "missing document root", req: CreateRequest{Name: "blog", Kind: "php", PHPVersion: "8.4", DocumentRoot: "public"}, want: "document root"},
		{name: "unknown extension", req: CreateRequest{Name: "blog", Kind: "php", PHPVersion: "8.4", PHPExtensions: map[string]bool{"notreal": true}}, want: "unknown php extension"},
		{name: "builtin extension", req: CreateRequest{Name: "blog", Kind: "php", PHPVersion: "8.4", PHPExtensions: map[string]bool{"json": true}}, want: "built-in"},
		{name: "managed ini directive", req: CreateRequest{Name: "blog", Kind: "php", PHPVersion: "8.4", PHPIni: map[string]string{"extension": "evil.so"}}, want: "managed"},
		{name: "ini injection", req: CreateRequest{Name: "blog", Kind: "php", PHPVersion: "8.4", PHPIni: map[string]string{"memory_limit": "512M\nadmin_value=1"}}, want: "newline"},
		{name: "raw managed marker", req: CreateRequest{Name: "blog", Kind: "php", PHPVersion: "8.4", PHPIniRaw: phpini.BeginExtensionsMarker + "\n"}, want: "reserved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, fakes := newTestService(t)
			fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
			tc.req.Path = fakes.root
			if _, err := svc.Create(ctx, tc.req); err == nil {
				t.Fatal("want error")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err, tc.want)
			}
			if sites, _ := svc.List(ctx); len(sites) != 0 {
				t.Fatal("invalid request persisted a site")
			}
			if lint, fpm := fakes.php.counts(); lint != 0 || fpm != 0 {
				t.Fatalf("validators ran for an invalid request: %d/%d", lint, fpm)
			}
		})
	}
}

func TestCreatePHPComposesIniFPMAndStoresSnapshot(t *testing.T) {
	svc, fakes := newTestService(t)
	build := fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site, err := svc.Create(ctx, CreateRequest{
		Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4", PHPBuild: build.ID,
		DocumentRoot:  ".",
		PHPExtensions: map[string]bool{"xdebug": true},
		PHPIni:        map[string]string{"memory_limit": "512M"},
		PHPIniRaw:     "display_errors = On\n",
		Env:           map[string]string{"APP_ENV": "local"},
	})
	if err != nil {
		t.Fatal(err)
	}

	genDir := phpGenerationDir(t, fakes, site)
	ini := string(readFile(t, filepath.Join(genDir, "php.ini")))
	if !strings.Contains(ini, "memory_limit = 512M") {
		t.Fatalf("site override missing from php.ini:\n%s", ini)
	}
	if !strings.Contains(ini, "display_errors = On") {
		t.Fatalf("raw section missing from php.ini:\n%s", ini)
	}
	if !strings.Contains(ini, "zend_extension=xdebug.so") {
		t.Fatalf("extension missing from php.ini:\n%s", ini)
	}
	if !strings.Contains(ini, "; base build configuration") {
		t.Fatalf("build base missing from php.ini:\n%s", ini)
	}
	caFile := filepath.Join(fakes.config.Home, "pki", "ca-bundle.crt")
	if !strings.Contains(ini, `curl.cainfo = "`+caFile+`"`) {
		t.Fatalf("CA bundle missing from php.ini:\n%s", ini)
	}

	fpm := string(readFile(t, filepath.Join(genDir, "php-fpm.conf")))
	if !strings.Contains(fpm, "listen = 127.0.0.1:"+strconv.Itoa(site.FastCGIPort)) {
		t.Fatalf("FastCGI listener missing from php-fpm.conf:\n%s", fpm)
	}
	if !strings.Contains(fpm, `chdir = "`+filepath.Clean(fakes.root)+`"`) {
		t.Fatalf("document root missing from php-fpm.conf:\n%s", fpm)
	}
	if !strings.Contains(fpm, `error_log = "/proc/self/fd/2"`) {
		t.Fatalf("php-fpm stderr is not the site log stream:\n%s", fpm)
	}
	if strings.Contains(fpm, "php-fpm.log") {
		t.Fatalf("php-fpm.conf bypasses the site log stream with a log file:\n%s", fpm)
	}
	if !strings.Contains(fpm, `pid = "`+filepath.Join(genDir, "php-fpm.pid")+`"`) {
		t.Fatalf("generation-local PID file missing from php-fpm.conf:\n%s", fpm)
	}
	if !strings.Contains(fpm, `env[APP_ENV] = "local"`) {
		t.Fatalf("environment missing from php-fpm.conf:\n%s", fpm)
	}

	for _, name := range []string{"php.ini", "php-fpm.conf", "health.php"} {
		if _, err := os.Stat(filepath.Join(fakes.config.Home, "sites", site.ID, name)); err != nil {
			t.Fatalf("active %s missing: %v", name, err)
		}
	}
	if got := readFile(t, filepath.Join(fakes.config.Home, "sites", site.ID, "health.php")); !strings.Contains(string(got), `echo "ok"`) {
		t.Fatalf("health script = %q", got)
	}

	info, err := os.Stat(genDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("generation dir mode = %o, want 700", got)
	}
	for name, want := range map[string]os.FileMode{"php.ini": 0o600, "php-fpm.conf": 0o600, "health.php": 0o644} {
		info, err := os.Stat(filepath.Join(genDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Fatalf("%s mode = %o, want %o", name, got, want)
		}
	}

	snapshot, err := fakes.store.GetSitePHPConfig(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.Ini, map[string]string{"memory_limit": "512M"}) {
		t.Fatalf("snapshot ini = %v", snapshot.Ini)
	}
	if !reflect.DeepEqual(snapshot.Extensions, map[string]bool{"xdebug": true}) {
		t.Fatalf("snapshot extensions = %v", snapshot.Extensions)
	}
	if snapshot.ConfigHash != site.PHPConfigHash {
		t.Fatalf("snapshot hash = %q, want %q", snapshot.ConfigHash, site.PHPConfigHash)
	}
	if snapshot.AppliedAt.IsZero() {
		t.Fatal("snapshot applied_at is zero")
	}

	defaults, err := fakes.store.GetPHPDefaults(ctx, "8.4")
	if err != nil {
		t.Fatalf("version defaults were not seeded: %v", err)
	}
	if len(defaults.Ini) == 0 || len(defaults.Extensions) == 0 {
		t.Fatalf("seeded defaults = %+v", defaults)
	}
}

func TestCreatePHPUsesEditedVersionDefaultsAndDependencyOrder(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	first := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})

	if err := fakes.store.SavePHPDefaults(ctx, model.PHPDefaults{
		Version:    "8.4",
		Ini:        map[string]string{"memory_limit": "640M"},
		Extensions: map[string]bool{"xdebug": true},
	}); err != nil {
		t.Fatal(err)
	}

	second, err := svc.Create(ctx, CreateRequest{Name: "shop", Path: fakes.root, Kind: "php",
		PHPVersion: "8.4", PHPExtensions: map[string]bool{"redis": true}})
	if err != nil {
		t.Fatal(err)
	}
	ini := string(readFile(t, filepath.Join(phpGenerationDir(t, fakes, second), "php.ini")))
	if !strings.Contains(ini, "memory_limit = 640M") {
		t.Fatalf("edited version default missing from php.ini:\n%s", ini)
	}
	if !strings.Contains(ini, "zend_extension=xdebug.so") {
		t.Fatalf("edited extension default missing from php.ini:\n%s", ini)
	}
	igbinary := strings.Index(ini, "extension=igbinary.so")
	redis := strings.Index(ini, "extension=redis.so")
	if igbinary < 0 || redis < 0 || igbinary > redis {
		t.Fatalf("extension dependency order wrong (igbinary=%d, redis=%d):\n%s", igbinary, redis, ini)
	}

	// Creating another site must not overwrite the edited defaults with the
	// embedded baseline.
	defaults, err := fakes.store.GetPHPDefaults(ctx, "8.4")
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Ini["memory_limit"] != "640M" {
		t.Fatalf("edited defaults overwritten: %+v", defaults)
	}
	firstIni := string(readFile(t, filepath.Join(phpGenerationDir(t, fakes, first), "php.ini")))
	if strings.Contains(firstIni, "memory_limit = 640M") {
		t.Fatal("existing generation was rewritten")
	}
}

func TestCreatePHPRejectsAnEmptyExtensionDirectory(t *testing.T) {
	svc, fakes := newTestService(t)
	build := fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	extRoot := filepath.Join(build.Path, "lib", "php", "extensions")
	if err := os.RemoveAll(extRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(extRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php",
		PHPVersion: "8.4", PHPBuild: build.ID}); err == nil {
		t.Fatal("want error")
	} else if !strings.Contains(err.Error(), "extension") {
		t.Fatalf("error = %v", err)
	}
	if sites, _ := svc.List(ctx); len(sites) != 0 {
		t.Fatal("site persisted")
	}
}

func TestCreatePHPDirectSharedObjectExtensionDirectory(t *testing.T) {
	svc, fakes := newTestService(t)
	build := fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	extRoot := filepath.Join(build.Path, "lib", "php", "extensions")
	if err := os.RemoveAll(extRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(extRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extRoot, "opcache.so"), []byte("so"), 0o644); err != nil {
		t.Fatal(err)
	}
	site, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php",
		PHPVersion: "8.4", PHPBuild: build.ID})
	if err != nil {
		t.Fatal(err)
	}
	ini := string(readFile(t, filepath.Join(phpGenerationDir(t, fakes, site), "php.ini")))
	if !strings.Contains(ini, `extension_dir = "`+extRoot+`"`) {
		t.Fatalf("extension_dir = direct .so directory missing:\n%s", ini)
	}
}

func TestCreatePHPRollsBackWhenValidationFails(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	fakes.php.lintErr = &phpini.ValidationError{Line: 8, Message: "syntax error"}
	_, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4", PHPBuild: "php@8.4.4-r1"})
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "syntax error") {
		t.Fatalf("error = %v", err)
	}
	if sites, _ := svc.List(ctx); len(sites) != 0 {
		t.Fatal("site persisted")
	}
	if fakes.backend.started["site"] {
		t.Fatal("FPM started")
	}
	assertNoPHPConfigDir(t, fakes, "site")
	assertNoReservedFastCGIPorts(t, fakes)
}

func TestCreatePHPRollsBackOnFPMValidationFailure(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	fakes.php.fpmErr = errors.New("fpm rejected the pool")
	_, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	if err == nil || !strings.Contains(err.Error(), "fpm rejected") {
		t.Fatalf("err = %v", err)
	}
	if _, ok := fakes.backend.lastPrepared(); ok {
		t.Fatal("FPM was prepared despite validation failure")
	}
	if len(fakes.certs.ensuredFor("site")) != 0 {
		t.Fatal("certificate issued despite validation failure")
	}
	if fakes.hosts.has("blog.test") {
		t.Fatalf("hosts changed despite validation failure: %v", fakes.hosts.last())
	}
	assertNoPHPConfigDir(t, fakes, "site")
	assertNoReservedFastCGIPorts(t, fakes)
}

func TestCreatePHPRollsBackOnStartFailure(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	fakes.backend.startErr = errors.New("master crashed")
	_, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	if err == nil || !strings.Contains(err.Error(), "master crashed") {
		t.Fatalf("err = %v", err)
	}
	if sites, _ := svc.List(ctx); len(sites) != 0 {
		t.Fatalf("site persisted: %+v", sites)
	}
	if fakes.backend.started["site"] {
		t.Fatal("master reported started")
	}
	if !fakes.backend.stopped["site"] {
		t.Fatal("rollback did not stop the staged master")
	}
	if fakes.hosts.has("blog.test") {
		t.Fatalf("hosts not restored: %v", fakes.hosts.last())
	}
	if _, err := fakes.store.GetCertBySite(ctx, "site"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("certificate row remains: %v", err)
	}
	assertNoPHPConfigDir(t, fakes, "site")
	assertNoReservedFastCGIPorts(t, fakes)
}

func TestCreatePHPRollsBackWhenStorePersistFails(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	db, err := sql.Open("sqlite", "file:"+fakes.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_php_site BEFORE INSERT ON sites BEGIN SELECT RAISE(ABORT, 'injected store failure'); END`); err != nil {
		t.Fatal(err)
	}

	_, err = svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	if err == nil || !strings.Contains(err.Error(), "persist") {
		t.Fatalf("err = %v", err)
	}
	if sites, _ := svc.List(ctx); len(sites) != 0 {
		t.Fatalf("site persisted: %+v", sites)
	}
	if fakes.backend.started["site"] {
		t.Fatal("master started despite a persistence failure")
	}
	assertNoPHPConfigDir(t, fakes, "site")
	assertNoReservedFastCGIPorts(t, fakes)
	if fakes.hosts.has("blog.test") {
		t.Fatalf("hosts not restored: %v", fakes.hosts.last())
	}
}

func TestCreatePHPRollsBackWhenConfigCannotBeWritten(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc.deps.Config.Home = file

	_, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	if err == nil {
		t.Fatal("want error")
	}
	if !fakes.hosts.has("devstack.test") {
		t.Fatalf("hosts were changed before config write: %v", fakes.hosts.last())
	}
	assertNoReservedFastCGIPorts(t, fakes)
}

func TestCreatePHPRollsBackWhenTheCertificateFails(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	fakes.certs.err = errors.New("signer down")
	_, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	if err == nil || !strings.Contains(err.Error(), "signer down") {
		t.Fatalf("err = %v", err)
	}
	if sites, _ := svc.List(ctx); len(sites) != 0 {
		t.Fatal("site persisted")
	}
	assertNoPHPConfigDir(t, fakes, "site")
	assertNoReservedFastCGIPorts(t, fakes)
	if fakes.hosts.has("blog.test") {
		t.Fatalf("hosts not restored: %v", fakes.hosts.last())
	}
}

func TestCreatePHPRollsBackWhenRoutePublicationFails(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	fakes.certs.loadErr = errors.New("certificate load failed")
	_, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	if err == nil || !strings.Contains(err.Error(), "certificate load failed") {
		t.Fatalf("err = %v", err)
	}
	if sites, _ := svc.List(ctx); len(sites) != 0 {
		t.Fatalf("site persisted: %+v", sites)
	}
	if fakes.backend.startedGens["site#default"] {
		t.Fatal("master left running after a failed route publication")
	}
	assertNoPHPConfigDir(t, fakes, "site")
	assertNoReservedFastCGIPorts(t, fakes)
}

func TestCreatePHPRollsBackWhenVhostPublicationFails(t *testing.T) {
	svc, fakes := newTestService(t)
	useServingMode(svc, fakes, "nginx")
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	fakes.vhosts.err = errors.New("vhost helper is down")
	_, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	if err == nil || !strings.Contains(err.Error(), "vhost helper is down") {
		t.Fatalf("err = %v", err)
	}
	if sites, _ := svc.List(ctx); len(sites) != 0 {
		t.Fatalf("site persisted: %+v", sites)
	}
	if fakes.backend.startedGens["site#default"] {
		t.Fatal("master left running after a failed vhost publication")
	}
	assertNoPHPConfigDir(t, fakes, "site")
	assertNoReservedFastCGIPorts(t, fakes)
	if fakes.hosts.has("blog.test") {
		t.Fatalf("hosts not restored: %v", fakes.hosts.last())
	}
	if _, err := fakes.store.GetCertBySite(ctx, "site"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("certificate row remains: %v", err)
	}
}

func TestDeletePHPRemovesConfigDirAndPort(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	siteDir := filepath.Join(fakes.config.Home, "sites", site.ID)
	if _, err := os.Stat(siteDir); err != nil {
		t.Fatalf("config dir missing before delete: %v", err)
	}

	if err := svc.Delete(ctx, site.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(siteDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config dir remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fakes.config.Home, "sites")); err != nil {
		t.Fatalf("sites root removed: %v", err)
	}
	if err := fakes.ports.Reserve(site.FastCGIPort); err != nil {
		t.Fatalf("FastCGI port %d not released: %v", site.FastCGIPort, err)
	}
	if !fakes.backend.stopped[site.ID] {
		t.Fatal("FPM master was not stopped")
	}
	if _, err := fakes.store.GetSite(ctx, site.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("site row remains: %v", err)
	}
}

func TestPHPVhostUsesFastCGIPort(t *testing.T) {
	svc, fakes := newTestService(t)
	useServingMode(svc, fakes, "nginx")
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php",
		PHPVersion: "8.4", PHPBuild: "php@8.4.4-r1", FrontController: true})
	if err != nil {
		t.Fatal(err)
	}
	if site.HTTPPort != 0 {
		t.Fatalf("external PHP site allocated HTTP port %d", site.HTTPPort)
	}
	req, ok := fakes.vhosts.lastSync()
	if !ok || len(req.Sites) != 1 {
		t.Fatalf("vhost request = %+v", req)
	}
	got := req.Sites[0]
	if got.BackendPort != site.FastCGIPort {
		t.Fatalf("vhost port = %d, want %d", got.BackendPort, site.FastCGIPort)
	}
	if got.Kind != string(model.SitePHP) || !got.FrontController {
		t.Fatalf("vhost site = %+v", got)
	}
	if got.DocumentRoot != filepath.Clean(fakes.root) {
		t.Fatalf("vhost document root = %q", got.DocumentRoot)
	}
	if !fakes.backend.started[site.ID] {
		t.Fatal("external PHP site did not start its FPM master")
	}
}

func TestStartPHPReusesExistingGeneration(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	hash := site.PHPConfigHash
	lint, fpm := fakes.php.counts()

	if err := svc.Stop(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, svc, site.ID); got.PHPConfigHash != hash {
		t.Fatalf("hash changed on reuse: %q -> %q", hash, got.PHPConfigHash)
	}
	if gotLint, gotFPM := fakes.php.counts(); gotLint != lint || gotFPM != fpm {
		t.Fatalf("config was recomposed on reuse: lint %d->%d fpm %d->%d", lint, gotLint, fpm, gotFPM)
	}
	if _, err := os.Stat(filepath.Join(fakes.config.Home, "sites", site.ID, hash)); err != nil {
		t.Fatalf("generation missing: %v", err)
	}
}

func TestStartPHPRecomposesWhenGenerationFilesAreMissing(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	hash := site.PHPConfigHash
	lint, fpm := fakes.php.counts()

	if err := svc.Stop(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(fakes.config.Home, "sites", site.ID, hash)); err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if gotLint, gotFPM := fakes.php.counts(); gotLint <= lint || gotFPM <= fpm {
		t.Fatalf("config was not recomposed: lint %d->%d fpm %d->%d", lint, gotLint, fpm, gotFPM)
	}
	if _, err := os.Stat(filepath.Join(fakes.config.Home, "sites", site.ID, hash, "php.ini")); err != nil {
		t.Fatalf("generation was not rewritten: %v", err)
	}
}

func TestStartPHPRecomposesAnIncompleteGeneration(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	health := filepath.Join(phpGenerationDir(t, fakes, site), "health.php")
	if err := os.Remove(health); err != nil {
		t.Fatal(err)
	}
	lint, fpm := fakes.php.counts()
	if err := svc.Stop(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if gotLint, gotFPM := fakes.php.counts(); gotLint != lint+1 || gotFPM != fpm+1 {
		t.Fatalf("incomplete generation was reused: lint %d->%d fpm %d->%d", lint, gotLint, fpm, gotFPM)
	}
	if _, err := os.Stat(health); err != nil {
		t.Fatalf("health script was not restored: %v", err)
	}
}

func TestStartPHPRecomposesWithANewPortWhenTheStoredPortIsZero(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	oldHash, oldPort := site.PHPConfigHash, site.FastCGIPort

	if err := svc.Stop(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored.FastCGIPort = 0
	if err := fakes.store.UpdateSite(ctx, stored); err != nil {
		t.Fatal(err)
	}

	if err := svc.Start(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	got := mustGet(t, svc, site.ID)
	if got.FastCGIPort == 0 || got.FastCGIPort == oldPort {
		t.Fatalf("port = %d, want a newly allocated port (old %d)", got.FastCGIPort, oldPort)
	}
	if got.PHPConfigHash == oldHash {
		t.Fatalf("hash did not change after a port change: %q", got.PHPConfigHash)
	}
	fpm := string(readFile(t, filepath.Join(phpGenerationDir(t, fakes, got), "php-fpm.conf")))
	if !strings.Contains(fpm, "listen = 127.0.0.1:"+strconv.Itoa(got.FastCGIPort)) {
		t.Fatalf("fpm config does not use the new port:\n%s", fpm)
	}
}

func TestStartPHPRequiresAnInstalledBuild(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	if err := svc.Stop(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if err := fakes.store.DeleteRuntime(ctx, site.PHPBuild); err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(ctx, site.ID); !errors.Is(err, ErrRuntimeMissing) {
		t.Fatalf("err = %v", err)
	}
}

func TestUpdatePHPFrontControllerOnlyDoesNotRecompose(t *testing.T) {
	svc, fakes := newTestService(t)
	useServingMode(svc, fakes, "nginx")
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	hash := site.PHPConfigHash
	lint, fpm := fakes.php.counts()
	startCalls := fakes.backend.startCount()

	front := true
	updated, err := svc.Update(ctx, site.ID, UpdateRequest{FrontController: &front})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.FrontController {
		t.Fatalf("updated site = %+v", updated)
	}
	if updated.FastCGIPort != site.FastCGIPort || updated.PHPConfigHash != hash {
		t.Fatalf("PHP config changed on a front-controller-only update: %+v", updated)
	}
	if gotLint, gotFPM := fakes.php.counts(); gotLint != lint || gotFPM != fpm {
		t.Fatal("validators ran on a PHP front-controller update")
	}
	if fakes.backend.startCount() != startCalls {
		t.Fatal("FPM restarted on a PHP front-controller update")
	}
	route, ok := fakes.router.Lookup("blog.test")
	if !ok {
		t.Fatal("route missing")
	}
	if !route.FrontController || route.DocumentRoot != filepath.Clean(fakes.root) {
		t.Fatalf("route = %+v", route)
	}
	if route.Kind != proxy.KindPHP || route.Upstream != "127.0.0.1:"+strconv.Itoa(site.FastCGIPort) {
		t.Fatalf("route = %+v", route)
	}
	req, ok := fakes.vhosts.lastSync()
	if !ok || len(req.Sites) != 1 {
		t.Fatalf("vhost request = %+v", req)
	}
	if !req.Sites[0].FrontController || req.Sites[0].DocumentRoot != filepath.Clean(fakes.root) {
		t.Fatalf("vhost site = %+v", req.Sites[0])
	}
	if got := phpGenerationDirs(t, fakes, site.ID); !reflect.DeepEqual(got, []string{hash}) {
		t.Fatalf("generations = %v, want only %s", got, hash)
	}
}

func TestUpdatePHPDocumentRootRecomposesGeneration(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	public := filepath.Join(fakes.root, "public")
	if err := os.MkdirAll(public, 0o755); err != nil {
		t.Fatal(err)
	}
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	oldHash := site.PHPConfigHash
	oldDir := phpGenerationDir(t, fakes, site)
	lint, fpm := fakes.php.counts()
	startCalls := fakes.backend.startCount()

	updated, err := svc.Update(ctx, site.ID, UpdateRequest{DocumentRoot: "public"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.DocumentRoot != "public" || updated.PHPConfigHash == oldHash {
		t.Fatalf("updated site = %+v (old hash %s)", updated, oldHash)
	}
	if updated.FastCGIPort != site.FastCGIPort {
		t.Fatalf("FastCGI port changed: %d -> %d", site.FastCGIPort, updated.FastCGIPort)
	}
	if gotLint, gotFPM := fakes.php.counts(); gotLint != lint+1 || gotFPM != fpm+1 {
		t.Fatalf("generation was not recomposed: lint %d->%d fpm %d->%d", lint, gotLint, fpm, gotFPM)
	}
	if fakes.backend.startCount() != startCalls {
		t.Fatal("FPM restarted on a document-root update")
	}
	if _, err := os.Stat(oldDir); err != nil {
		t.Fatalf("old generation removed: %v", err)
	}
	newDir := filepath.Join(fakes.config.Home, "sites", site.ID, updated.PHPConfigHash)
	fpmConfig := string(readFile(t, filepath.Join(newDir, "php-fpm.conf")))
	if !strings.Contains(fpmConfig, `chdir = "`+public+`"`) {
		t.Fatalf("new generation uses the old document root:\n%s", fpmConfig)
	}
	active := string(readFile(t, filepath.Join(fakes.config.Home, "sites", site.ID, "php-fpm.conf")))
	if !strings.Contains(active, `chdir = "`+public+`"`) {
		t.Fatalf("active copy was not committed:\n%s", active)
	}
	snapshot, err := fakes.store.GetSitePHPConfig(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ConfigHash != updated.PHPConfigHash {
		t.Fatalf("snapshot hash = %q, want %q", snapshot.ConfigHash, updated.PHPConfigHash)
	}

	if err := svc.Stop(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	spec, ok := fakes.backend.lastPrepared()
	if !ok {
		t.Fatal("backend was not prepared")
	}
	if spec.PHPIniPath != filepath.Join(newDir, "php.ini") || spec.FPMConfigPath != filepath.Join(newDir, "php-fpm.conf") {
		t.Fatalf("restarted master does not use the new generation: %+v", spec)
	}
	if spec.DocumentRoot != "public" {
		t.Fatalf("restarted master document root = %q", spec.DocumentRoot)
	}
	if spec.FastCGIPort != site.FastCGIPort {
		t.Fatalf("restarted master port = %d", spec.FastCGIPort)
	}
}

func TestUpdatePHPEnvironmentRecomposesGeneration(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{
		Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4",
		Env: map[string]string{"APP_ENV": "local"},
	})
	oldHash := site.PHPConfigHash
	oldDir := phpGenerationDir(t, fakes, site)
	startCalls := fakes.backend.startCount()

	updated, err := svc.Update(ctx, site.ID, UpdateRequest{
		Env: map[string]string{"APP_ENV": "prod", "FEATURE_X": "on"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.PHPConfigHash == oldHash {
		t.Fatal("environment update did not change the config hash")
	}
	if !reflect.DeepEqual(updated.Env, map[string]string{"APP_ENV": "prod", "FEATURE_X": "on"}) {
		t.Fatalf("env = %v", updated.Env)
	}
	if fakes.backend.startCount() != startCalls {
		t.Fatal("FPM restarted on an environment update")
	}
	if _, err := os.Stat(oldDir); err != nil {
		t.Fatalf("old generation removed: %v", err)
	}
	fpmConfig := string(readFile(t, filepath.Join(phpGenerationDir(t, fakes, updated), "php-fpm.conf")))
	if !strings.Contains(fpmConfig, `env[APP_ENV] = "prod"`) || !strings.Contains(fpmConfig, `env[FEATURE_X] = "on"`) {
		t.Fatalf("new generation uses the old environment:\n%s", fpmConfig)
	}
	if strings.Contains(fpmConfig, `env[APP_ENV] = "local"`) {
		t.Fatalf("old environment remains in the new generation:\n%s", fpmConfig)
	}
}

func TestUpdatePHPWithoutAPortClearsTheHash(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	public := filepath.Join(fakes.root, "public")
	if err := os.MkdirAll(public, 0o755); err != nil {
		t.Fatal(err)
	}
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	if err := svc.Stop(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored.FastCGIPort = 0
	if err := fakes.store.UpdateSite(ctx, stored); err != nil {
		t.Fatal(err)
	}
	lint, fpm := fakes.php.counts()

	updated, err := svc.Update(ctx, site.ID, UpdateRequest{DocumentRoot: "public"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.PHPConfigHash != "" {
		t.Fatalf("hash = %q, want cleared without a port", updated.PHPConfigHash)
	}
	if gotLint, gotFPM := fakes.php.counts(); gotLint != lint || gotFPM != fpm {
		t.Fatal("generation was composed before a port existed")
	}
	if got := phpGenerationDirs(t, fakes, site.ID); len(got) != 1 {
		t.Fatalf("generations = %v, want only the previous generation", got)
	}

	if err := svc.Start(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	started := mustGet(t, svc, site.ID)
	if started.PHPConfigHash == "" || started.FastCGIPort == 0 {
		t.Fatalf("start did not compose a generation: %+v", started)
	}
	spec, _ := fakes.backend.lastPrepared()
	if spec.DocumentRoot != "public" {
		t.Fatalf("start used document root %q", spec.DocumentRoot)
	}
	fpmConfig := string(readFile(t, filepath.Join(phpGenerationDir(t, fakes, started), "php-fpm.conf")))
	if !strings.Contains(fpmConfig, `chdir = "`+public+`"`) {
		t.Fatalf("recomposed generation uses the old document root:\n%s", fpmConfig)
	}
}

func TestUpdatePHPRollsBackANewGenerationOnFailure(t *testing.T) {
	t.Run("validation failure", func(t *testing.T) {
		svc, fakes := newTestService(t)
		fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
		if err := os.MkdirAll(filepath.Join(fakes.root, "public"), 0o755); err != nil {
			t.Fatal(err)
		}
		site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
		oldHash := site.PHPConfigHash
		fakes.php.lintErr = errors.New("syntax error on line 4")

		if _, err := svc.Update(ctx, site.ID, UpdateRequest{DocumentRoot: "public"}); err == nil {
			t.Fatal("want error")
		} else if !strings.Contains(err.Error(), "syntax error") {
			t.Fatalf("error = %v", err)
		}
		stored, err := fakes.store.GetSite(ctx, site.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.PHPConfigHash != oldHash || stored.DocumentRoot != "." {
			t.Fatalf("row changed after a failed update: %+v", stored)
		}
		if got := phpGenerationDirs(t, fakes, site.ID); !reflect.DeepEqual(got, []string{oldHash}) {
			t.Fatalf("generations = %v, want only %s", got, oldHash)
		}
	})

	t.Run("route publication failure", func(t *testing.T) {
		svc, fakes := newTestService(t)
		fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
		if err := os.MkdirAll(filepath.Join(fakes.root, "public"), 0o755); err != nil {
			t.Fatal(err)
		}
		site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
		oldHash := site.PHPConfigHash
		fakes.certs.loadErr = errors.New("certificate load failed")

		if _, err := svc.Update(ctx, site.ID, UpdateRequest{DocumentRoot: "public"}); err == nil {
			t.Fatal("want error")
		} else if !strings.Contains(err.Error(), "certificate load failed") {
			t.Fatalf("error = %v", err)
		}
		stored, err := fakes.store.GetSite(ctx, site.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.PHPConfigHash != oldHash || stored.DocumentRoot != "." {
			t.Fatalf("row not rolled back: %+v", stored)
		}
		if got := phpGenerationDirs(t, fakes, site.ID); !reflect.DeepEqual(got, []string{oldHash}) {
			t.Fatalf("generations = %v, want only %s", got, oldHash)
		}
	})
}

func TestUpdatePHPRejectsAnEscapingDocumentRoot(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	if _, err := svc.Update(ctx, site.ID, UpdateRequest{DocumentRoot: "../outside"}); err == nil {
		t.Fatal("want error")
	} else if !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("error = %v", err)
	}
	got := mustGet(t, svc, site.ID)
	if got.DocumentRoot != "." {
		t.Fatalf("document root changed: %q", got.DocumentRoot)
	}
}

func TestUpdateRejectsFrontControllerOnNonPHPSites(t *testing.T) {
	svc, fakes := newTestService(t)
	static := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"})
	node := mustCreate(t, svc, fakes, CreateRequest{Name: "shop", Path: fakes.root, Kind: "node", NodeVersion: "24", Command: "npm run dev"})

	front, off := true, false
	for _, tc := range []struct {
		name string
		ref  string
		want bool
	}{
		{name: "static", ref: static.ID, want: front},
		{name: "node", ref: node.ID, want: off},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.Update(ctx, tc.ref, UpdateRequest{FrontController: &tc.want}); err == nil {
				t.Fatal("want error")
			} else if !strings.Contains(err.Error(), "front controller") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestPHPExecUsesGeneratedIniAndWorksWhenStopped(t *testing.T) {
	svc, fakes := newTestService(t)
	build := fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	if err := svc.Stop(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	starts := fakes.backend.startCount()
	fakes.backend.execResult = backendResult()

	result, err := svc.PHPExec(ctx, site.ID, []string{"--version"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "PHP 8.4.4" || result.ExitCode != 0 {
		t.Fatalf("result = %+v", result)
	}
	call, ok := fakes.backend.lastExec()
	if !ok {
		t.Fatal("backend exec was not called")
	}
	wantIni := filepath.Join(phpGenerationDir(t, fakes, site), "php.ini")
	wantArgv := []string{filepath.Join(build.Path, "bin", "php"), "-c", wantIni, "--version"}
	if !reflect.DeepEqual(call.Argv, wantArgv) {
		t.Fatalf("argv = %v, want %v", call.Argv, wantArgv)
	}
	if call.ID != site.ID {
		t.Fatalf("exec site = %q", call.ID)
	}
	if fakes.backend.startCount() != starts {
		t.Fatal("PHPExec started an FPM master")
	}
	if got := mustGet(t, svc, site.ID); got.Status != model.StatusStopped {
		t.Fatalf("status = %q, want stopped", got.Status)
	}
}

func TestPHPExecRecomposesMissingConfigFiles(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	lint, fpm := fakes.php.counts()
	if err := os.RemoveAll(filepath.Join(fakes.config.Home, "sites", site.ID, site.PHPConfigHash)); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.PHPExec(ctx, site.ID, []string{"-r", "echo 1;"}); err != nil {
		t.Fatal(err)
	}
	if gotLint, gotFPM := fakes.php.counts(); gotLint != lint+1 || gotFPM != fpm+1 {
		t.Fatalf("missing config was not recomposed: lint %d->%d fpm %d->%d", lint, gotLint, fpm, gotFPM)
	}
	call, _ := fakes.backend.lastExec()
	if _, err := os.Stat(call.Argv[2]); err != nil {
		t.Fatalf("generated ini missing: %v", err)
	}
}

func TestPHPExecRejectsIniBypassFlags(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	before := len(fakes.backend.execList())

	for _, argv := range [][]string{
		{"-c", "/tmp/evil.ini"},
		{"-c/tmp/evil.ini"},
		{"--php-ini", "/tmp/evil.ini"},
		{"--php-ini=/tmp/evil.ini"},
		{"-n"},
		{"--no-php-ini"},
		{"-v", "-c", "/tmp/evil.ini"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			if _, err := svc.PHPExec(ctx, site.ID, argv); err == nil {
				t.Fatal("want error")
			} else if !strings.Contains(err.Error(), "php.ini") {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if got := len(fakes.backend.execList()); got != before {
		t.Fatalf("backend exec ran with bypass flags: %d -> %d", before, got)
	}
}

func TestPHPExecRejectsNonPHPSites(t *testing.T) {
	svc, fakes := newTestService(t)
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"})
	if _, err := svc.PHPExec(ctx, site.ID, []string{"-v"}); err == nil {
		t.Fatal("want error")
	}
}

func TestValidatePHPRunsValidatorsWithoutStartingOrMutating(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	lint, fpm := fakes.php.counts()
	starts := fakes.backend.startCount()
	storedBefore, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.ValidatePHP(ctx, site); err != nil {
		t.Fatal(err)
	}
	if gotLint, gotFPM := fakes.php.counts(); gotLint != lint+1 || gotFPM != fpm+1 {
		t.Fatalf("validators not called: lint %d->%d fpm %d->%d", lint, gotLint, fpm, gotFPM)
	}
	if fakes.backend.startCount() != starts {
		t.Fatal("ValidatePHP started a master")
	}
	storedAfter, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(storedBefore, storedAfter) {
		t.Fatalf("ValidatePHP mutated the row:\n before %+v\n after  %+v", storedBefore, storedAfter)
	}
	entries, err := os.ReadDir(filepath.Join(fakes.config.Home, "sites", site.ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".validate-") {
			t.Fatalf("temporary validation directory remains: %s", entry.Name())
		}
	}
}

func TestValidatePHPAllocatesATemporaryPortWhenStoppedWithoutOne(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	if err := svc.Stop(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored.FastCGIPort = 0
	if err := fakes.store.UpdateSite(ctx, stored); err != nil {
		t.Fatal(err)
	}
	before := reservedFastCGIPorts(t, fakes)
	lint, fpm := fakes.php.counts()

	if err := svc.ValidatePHP(ctx, stored); err != nil {
		t.Fatal(err)
	}
	if gotLint, gotFPM := fakes.php.counts(); gotLint != lint+1 || gotFPM != fpm+1 {
		t.Fatalf("validators not called: lint %d->%d fpm %d->%d", lint, gotLint, fpm, gotFPM)
	}
	if after := reservedFastCGIPorts(t, fakes); !reflect.DeepEqual(before, after) {
		t.Fatalf("validation leaked a FastCGI port: before %v, after %v", before, after)
	}
	after, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.FastCGIPort != 0 {
		t.Fatalf("ValidatePHP persisted a port: %d", after.FastCGIPort)
	}
}

func TestValidatePHPPropagatesFailuresAndCleansUp(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	fakes.php.lintErr = errors.New("syntax error on line 4")

	if err := svc.ValidatePHP(ctx, site); err == nil || !strings.Contains(err.Error(), "syntax error") {
		t.Fatalf("err = %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(fakes.config.Home, "sites", site.ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".validate-") {
			t.Fatalf("temporary validation directory remains on failure: %s", entry.Name())
		}
	}
}

func TestDeletePHPExternalRestoresMasterOnFailure(t *testing.T) {
	svc, fakes := newTestService(t)
	useServingMode(svc, fakes, "nginx")
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	fakes.hosts.failAt = fakes.hosts.callCount() + 1

	if err := svc.Delete(ctx, site.ID); err == nil {
		t.Fatal("want error")
	}
	stored, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatalf("site row removed: %v", err)
	}
	if stored.DesiredState != model.DesiredRunning {
		t.Fatalf("row not restored: %+v", stored)
	}
	if _, err := os.Stat(filepath.Join(fakes.config.Home, "sites", site.ID, site.PHPConfigHash)); err != nil {
		t.Fatalf("config dir removed on a failed delete: %v", err)
	}
	if !fakes.backend.startedGens[site.ID+"#default"] {
		t.Fatal("FPM master was not restarted after the failed delete")
	}
}

func TestDeletePHPExternalRemovesConfigDir(t *testing.T) {
	svc, fakes := newTestService(t)
	useServingMode(svc, fakes, "nginx")
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})

	if err := svc.Delete(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(fakes.config.Home, "sites", site.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config dir remains: %v", err)
	}
	if !fakes.backend.stopped[site.ID] {
		t.Fatal("master was not stopped")
	}
	if err := fakes.ports.Reserve(site.FastCGIPort); err != nil {
		t.Fatalf("port %d not released: %v", site.FastCGIPort, err)
	}
	req, ok := fakes.vhosts.lastSync()
	if !ok {
		t.Fatal("vhosts never synced")
	}
	for _, got := range req.Sites {
		if got.ID == site.ID {
			t.Fatalf("deleted site still published: %+v", got)
		}
	}
}

func TestNewServiceRequiresPHPBackendWhenBackendIsNotAStager(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	svc.deps.PHPBackend = nil
	if _, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"}); err == nil {
		t.Fatal("want error")
	} else if !strings.Contains(err.Error(), "PHP backend") {
		t.Fatalf("error = %v", err)
	}
}

// phpGenerationDir returns the immutable generation directory for a site.
func phpGenerationDir(t *testing.T, fakes *testFakes, site model.Site) string {
	t.Helper()
	if site.PHPConfigHash == "" {
		t.Fatalf("site %q has no PHP config hash", site.Name)
	}
	return filepath.Join(fakes.config.Home, "sites", site.ID, site.PHPConfigHash)
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// phpGenerationDirs returns the hash-named generation directories for a site.
func phpGenerationDirs(t *testing.T, fakes *testFakes, siteID string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(fakes.config.Home, "sites", siteID))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	directories := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || len(name) != 64 {
			continue
		}
		if _, err := hex.DecodeString(name); err != nil {
			continue
		}
		directories = append(directories, name)
	}
	sort.Strings(directories)
	return directories
}

func TestPHPConfigDirectoryRejectsTheSitesRoot(t *testing.T) {
	svc, fakes := newTestService(t)
	sitesRoot := filepath.Join(fakes.config.Home, "sites")
	if err := os.MkdirAll(sitesRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(sitesRoot, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, siteID := range []string{".", "./", "..", "site/.", "site/..", ""} {
		t.Run(strconv.Quote(siteID), func(t *testing.T) {
			if dir, err := svc.phpSiteDir(siteID); err == nil {
				t.Fatalf("phpSiteDir(%q) = %q, want error", siteID, dir)
			}
			if err := svc.removePHPConfigLocked(siteID); err == nil {
				t.Fatalf("removePHPConfigLocked(%q) succeeded", siteID)
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("sites root content removed: %v", err)
			}
		})
	}
	for _, siteID := range []string{"site", "site-2", "0123456789abcdef0123456789abcdef"} {
		if _, err := svc.phpSiteDir(siteID); err != nil {
			t.Fatalf("phpSiteDir(%q): %v", siteID, err)
		}
	}
}

func assertNoPHPConfigDir(t *testing.T, fakes *testFakes, siteID string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(fakes.config.Home, "sites", siteID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config directory for %q remains: %v", siteID, err)
	}
}

// assertNoReservedFastCGIPorts probes the allocator for leaked FastCGI ports.
// A Reserve error means the allocator still considers the port reserved.
func assertNoReservedFastCGIPorts(t *testing.T, fakes *testFakes) {
	t.Helper()
	if leaked := reservedFastCGIPorts(t, fakes); len(leaked) != 0 {
		t.Fatalf("FastCGI ports still reserved after rollback: %v", leaked)
	}
}

// reservedFastCGIPorts returns the FastCGI ports the allocator considers in
// use, without leaving any of the probed ports reserved.
func reservedFastCGIPorts(t *testing.T, fakes *testFakes) []int {
	t.Helper()
	var reserved []int
	for port := 43000; port <= 43999; port++ {
		if err := fakes.ports.Reserve(port); err != nil {
			reserved = append(reserved, port)
			continue
		}
		fakes.ports.Release(port)
	}
	return reserved
}

func backendResult() backend.Result {
	return backend.Result{ExitCode: 0, Stdout: "PHP 8.4.4"}
}
