package php

// This is the Plan 2 acceptance test. It drives the real site service, the real
// local backend (supervised php-fpm), the real FastCGI client and proxy, and the
// real certificate manager against the system PHP installation. It deliberately
// uses the system build with a filtered catalog manifest: the production
// catalog does not ship PHP artifacts yet, and this test must not pretend that
// the system layout matches the managed all-shared build layout.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/backend"
	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/certs"
	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/doctor"
	"github.com/AramZahedi/dev-web-stack/internal/domain"
	"github.com/AramZahedi/dev-web-stack/internal/fcgi"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/logs"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/phpini"
	"github.com/AramZahedi/dev-web-stack/internal/proxy"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
	"github.com/AramZahedi/dev-web-stack/internal/store"
	"github.com/AramZahedi/dev-web-stack/internal/vhost"
)

const (
	defaultSystemPHPRoot = "/usr/local/php"
	phpRootEnv           = "DEVSTACK_PHP_ROOT"
	acceptancePHPLine    = "8.4"
)

func TestPHPSubsystemAcceptance(t *testing.T) {
	root := systemPHPRoot(t)
	phpVersion := systemPHPVersion(t, root)
	if catalog.LineOf(phpVersion) != acceptancePHPLine {
		t.Skipf("system PHP at %s is %s; this acceptance run requires PHP %s", root, phpVersion, acceptancePHPLine)
	}
	extensionDir := systemPHPExtensionDir(t, root)
	modules := systemPHPModules(t, root)
	manifest := filteredPHPManifest(t, extensionDir, modules)
	uncatalogued := uncataloguedSharedModules(t, extensionDir, manifest)
	for _, name := range uncatalogued {
		if containsString(manifest.Builtin, name) {
			t.Fatalf("uncatalogued shared module %q was misclassified as built in", name)
		}
	}
	if len(uncatalogued) == 0 {
		t.Log("acceptance: no uncatalogued shared modules are present")
	} else {
		t.Logf("acceptance: uncatalogued shared modules not loaded by the generated ini: %s", strings.Join(uncatalogued, ","))
	}

	h := newAcceptanceHarness(t, root, phpVersion, manifest)
	t.Logf("acceptance: PHP %s build %s at %s", phpVersion, h.buildID, root)
	t.Logf("acceptance: extension dir %s", extensionDir)
	t.Logf("acceptance: filtered extensions %s", extensionNames(h.manifest.Extensions))
	t.Logf("acceptance: builtin modules %s", strings.Join(h.manifest.Builtin, ","))

	blogProject := writeProject(t, h.home, "blog-project", "DOCROOT=primary")
	apiProject := writeProject(t, h.home, "api-project", "DOCROOT=secondary")

	// 1. A real PHP site with the system build and the version defaults.
	blog := h.createSite("blog", blogProject, nil)
	if blog.PHPBuild != h.buildID {
		t.Fatalf("blog build = %q, want %q", blog.PHPBuild, h.buildID)
	}
	if blog.FastCGIPort < 43000 || blog.FastCGIPort > 43999 {
		t.Fatalf("blog FastCGI port = %d, want 43000-43999", blog.FastCGIPort)
	}
	status, err := h.local.Status(context.Background(), blog.ID)
	if err != nil {
		t.Fatalf("blog backend status: %v", err)
	}
	if status.State != model.StatusRunning {
		t.Fatalf("blog backend state = %q, want running", status.State)
	}
	t.Logf("acceptance: blog site id=%s build=%s fastcgi=%d", blog.ID, blog.PHPBuild, blog.FastCGIPort)
	assertPHPResponse(t, h.directBody(blog), phpVersion, "256M", false, "primary")

	// 2. Two sites; xdebug is enabled on the first only.
	api := h.createSite("api", apiProject, nil)
	t.Logf("acceptance: api site id=%s build=%s fastcgi=%d", api.ID, api.PHPBuild, api.FastCGIPort)

	if !manifestHasExtension(h.manifest, "xdebug") {
		t.Fatal("filtered manifest does not offer xdebug")
	}
	blog = h.applyPHP(blog.Name, sites.PHPChange{Extensions: map[string]bool{"xdebug": true}})
	assertPHPResponse(t, h.directBody(blog), phpVersion, "256M", true, "primary")
	assertPHPResponse(t, h.directBody(api), phpVersion, "256M", false, "secondary")
	t.Logf("acceptance: xdebug isolation verified on blog port %d; api port %d unaffected", blog.FastCGIPort, api.FastCGIPort)

	// 3. A site ini change is visible only on that site.
	blog = h.applyPHP(blog.Name, sites.PHPChange{Ini: map[string]string{"memory_limit": "512M"}})
	assertPHPResponse(t, h.directBody(blog), phpVersion, "512M", true, "primary")
	assertPHPResponse(t, h.directBody(api), phpVersion, "256M", false, "secondary")
	t.Logf("acceptance: per-site memory_limit verified; blog=%d api=%d", blog.FastCGIPort, api.FastCGIPort)

	// 4. A malformed raw ini is rejected with a line number and the old master
	// keeps serving the old configuration.
	beforeInvalid := h.getSite(blog.Name)
	raw := "display_errors = Off\nbroken directive without equals\n"
	if _, err := h.svc.ApplyPHP(timeoutContext(t, 60*time.Second), blog.Name, sites.PHPChange{IniRaw: &raw}); err == nil {
		t.Fatal("ApplyPHP accepted a malformed raw php.ini")
	} else {
		var validation *phpini.ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("invalid raw ini error = %v, want *phpini.ValidationError", err)
		}
		if validation.Line != 2 {
			t.Fatalf("invalid raw ini line = %d, want 2 (error: %v)", validation.Line, err)
		}
		t.Logf("acceptance: malformed raw ini rejected at line %d: %v", validation.Line, err)
	}
	afterInvalid := h.getSite(blog.Name)
	assertSiteRowUnchanged(t, beforeInvalid, afterInvalid)
	assertPHPResponse(t, h.directBody(afterInvalid), phpVersion, "512M", true, "primary")
	assertRouteAndVhostPort(t, h, afterInvalid, afterInvalid.FastCGIPort)

	// 5. A build that lacks sbin/php-fpm fails validate-then-swap and leaves the
	// old row and master intact. The staged port must be released.
	badBuildID := catalog.FormatID("php", phpVersion, 2)
	badRoot := writeIncompletePHPBuild(t, root)
	h.upsertBuild(model.RuntimeBuild{
		ID:          badBuildID,
		Kind:        "php",
		Version:     phpVersion,
		Backend:     model.BackendLocal,
		Path:        badRoot,
		InstalledAt: time.Now().UTC(),
	})

	// Leave exactly one FastCGI port free so the failed apply must allocate it.
	candidate := chooseFreeFastCGIPort(t, blog.FastCGIPort, api.FastCGIPort)
	releaseReservations := reserveAllFastCGIPortsExcept(t, h.ports, candidate, blog.FastCGIPort, api.FastCGIPort)
	beforeBadSwitch := h.getSite(blog.Name)
	_, err = h.svc.ApplyPHP(timeoutContext(t, 60*time.Second), blog.Name, sites.PHPChange{Build: badBuildID})
	if err == nil {
		t.Fatal("ApplyPHP to a build without sbin/php-fpm succeeded")
	}
	if !strings.Contains(err.Error(), "php-fpm") {
		t.Fatalf("incomplete build error = %v, want a php-fpm validation failure", err)
	}
	if strings.Contains(err.Error(), "no free fastcgi port") {
		t.Fatalf("incomplete build failed before allocating the candidate port: %v", err)
	}
	t.Logf("acceptance: incomplete build switch rejected: %v", err)
	afterBadSwitch := h.getSite(blog.Name)
	if afterBadSwitch.PHPBuild != h.buildID {
		t.Fatalf("blog build after failed switch = %q, want %q", afterBadSwitch.PHPBuild, h.buildID)
	}
	if afterBadSwitch.PHPConfigHash != beforeBadSwitch.PHPConfigHash || afterBadSwitch.FastCGIPort != beforeBadSwitch.FastCGIPort {
		t.Fatalf("failed switch changed row hash/port: before %s/%d after %s/%d",
			beforeBadSwitch.PHPConfigHash, beforeBadSwitch.FastCGIPort,
			afterBadSwitch.PHPConfigHash, afterBadSwitch.FastCGIPort)
	}
	assertPHPResponse(t, h.directBody(afterBadSwitch), phpVersion, "512M", true, "primary")
	assertRouteAndVhostPort(t, h, afterBadSwitch, afterBadSwitch.FastCGIPort)
	requirePortFree(t, candidate, "failed-switch candidate", 10*time.Second)
	if err := h.ports.Reserve(candidate); err != nil {
		t.Fatalf("candidate FastCGI port %d was not released: %v", candidate, err)
	}
	h.ports.Release(candidate)
	releaseReservations()
	t.Logf("acceptance: candidate port %d was released after the failed switch", candidate)

	// 6. External template rendering.
	nginxVhost, err := h.svc.PreviewVhost(context.Background(), blog.Name, string(vhost.ServerNginx))
	if err != nil {
		t.Fatalf("preview nginx vhost: %v", err)
	}
	assertNginxPHPVhost(t, nginxVhost, afterBadSwitch.FastCGIPort, afterBadSwitch)
	apacheVhost, err := h.svc.PreviewVhost(context.Background(), blog.Name, string(vhost.ServerApache))
	if err != nil {
		t.Fatalf("preview apache vhost: %v", err)
	}
	assertApachePHPVhost(t, apacheVhost, afterBadSwitch.FastCGIPort, afterBadSwitch)
	t.Log("acceptance: nginx and Apache PHP vhost templates contain FastCGI, PATH_INFO, index, and backup-denial rules")

	t.Run("nginx configtest", func(t *testing.T) {
		testRealNginxConfig(t, h, afterBadSwitch)
	})
	t.Run("apache configtest", func(t *testing.T) {
		testRealApacheConfig(t, h, afterBadSwitch)
	})

	// 7. The built-in proxy routes the same site by Host.
	proxyBody := proxyGet(t, h.router, "blog.test", "/index.php")
	assertPHPResponse(t, proxyBody, phpVersion, "512M", true, "primary")
	t.Log("acceptance: built-in proxy served blog.test through FastCGI with 512M and xdebug")

	// 8. Update, migration, removal block, and delete.
	publicTwo := filepath.Join(blogProject, "public2")
	if err := os.MkdirAll(publicTwo, 0o755); err != nil {
		t.Fatalf("create updated document root: %v", err)
	}
	writeIndexPHP(t, publicTwo, "DOCROOT=updated")
	frontController := true
	updated, err := h.svc.Update(timeoutContext(t, 60*time.Second), blog.Name, sites.UpdateRequest{
		DocumentRoot:    "public2",
		FrontController: &frontController,
	})
	if err != nil {
		t.Fatalf("update blog document root: %v", err)
	}
	if updated.DocumentRoot != "public2" || !updated.FrontController || updated.PHPBuild != h.buildID {
		t.Fatalf("updated blog row = document_root %q front_controller %v build %q",
			updated.DocumentRoot, updated.FrontController, updated.PHPBuild)
	}
	t.Logf("acceptance: updated blog document root=%s front_controller=%v", updated.DocumentRoot, updated.FrontController)

	var inUse *sites.RuntimeInUseError
	if err := h.svc.CheckRuntimeRemoval(context.Background(), h.buildID); !errors.As(err, &inUse) {
		t.Fatalf("CheckRuntimeRemoval(%s) = %v, want RuntimeInUseError", h.buildID, err)
	} else {
		sort.Strings(inUse.Sites)
		if strings.Join(inUse.Sites, ",") != "api,blog" {
			t.Fatalf("runtime in-use sites = %v, want [api blog]", inUse.Sites)
		}
	}
	t.Logf("acceptance: removal of %s blocked by %v", h.buildID, inUse.Sites)

	validBuildID := catalog.FormatID("php", phpVersion, 2)
	h.upsertBuild(model.RuntimeBuild{
		ID:          validBuildID,
		Kind:        "php",
		Version:     phpVersion,
		Backend:     model.BackendLocal,
		Path:        root,
		InstalledAt: time.Now().UTC(),
	})
	if err := h.svc.CheckRuntimeRemoval(context.Background(), h.buildID); !errors.As(err, &inUse) {
		t.Fatalf("CheckRuntimeRemoval before migration = %v, want RuntimeInUseError", err)
	}

	results, err := h.svc.MigratePHP(timeoutContext(t, 60*time.Second), sites.PHPMigrationRequest{
		FromVersion: acceptancePHPLine,
		ToVersion:   acceptancePHPLine,
		ToBuild:     validBuildID,
	})
	if err != nil {
		t.Fatalf("migrate PHP sites: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("migration results = %d, want 2 (%+v)", len(results), results)
	}
	for _, result := range results {
		if !result.OK {
			t.Fatalf("migration of %s failed: %s", result.Site, result.Error)
		}
	}
	blog = h.getSite(blog.Name)
	api = h.getSite(api.Name)
	if blog.PHPBuild != validBuildID || api.PHPBuild != validBuildID {
		t.Fatalf("builds after migration = blog %q api %q, want %q", blog.PHPBuild, api.PHPBuild, validBuildID)
	}
	if err := h.svc.CheckRuntimeRemoval(context.Background(), h.buildID); err != nil {
		t.Fatalf("CheckRuntimeRemoval(%s) after migration = %v, want nil", h.buildID, err)
	}
	t.Logf("acceptance: migrated both sites to %s; %s is no longer referenced", validBuildID, h.buildID)

	apiPort := api.FastCGIPort
	apiConfigDir := filepath.Join(h.paths.Sites, api.ID)
	if err := h.svc.Delete(timeoutContext(t, 60*time.Second), api.Name); err != nil {
		t.Fatalf("delete api site: %v", err)
	}
	if _, err := h.store.GetSiteByName(context.Background(), api.Name); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted api row is still present: %v", err)
	}
	if _, err := os.Stat(apiConfigDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("api config dir %s still exists after delete: %v", apiConfigDir, err)
	}
	requirePortFree(t, apiPort, "deleted api", 10*time.Second)
	if err := h.ports.Reserve(apiPort); err != nil {
		t.Fatalf("api FastCGI port %d was not released: %v", apiPort, err)
	}
	h.ports.Release(apiPort)
	if _, ok := h.router.Lookup(api.Domain); ok {
		t.Fatalf("api route %s survived site deletion", api.Domain)
	}
	t.Logf("acceptance: deleted api site; config dir removed and port %d released", apiPort)

	// 9. Stop/start keeps the build and a healthy master.
	beforeStop := h.getSite(blog.Name)
	if err := h.svc.Stop(timeoutContext(t, 60*time.Second), blog.Name); err != nil {
		t.Fatalf("stop blog: %v", err)
	}
	requirePortFree(t, beforeStop.FastCGIPort, "stopped blog", 10*time.Second)
	stopped := h.getSite(blog.Name)
	if stopped.Status != model.StatusStopped {
		t.Fatalf("blog status after stop = %q, want stopped", stopped.Status)
	}
	if err := h.svc.Start(timeoutContext(t, 60*time.Second), blog.Name); err != nil {
		t.Fatalf("start blog: %v", err)
	}
	afterStart := h.getSite(blog.Name)
	if afterStart.PHPBuild != validBuildID {
		t.Fatalf("blog build after start = %q, want %q", afterStart.PHPBuild, validBuildID)
	}
	if afterStart.FastCGIPort != beforeStop.FastCGIPort {
		t.Fatalf("blog FastCGI port after start = %d, want %d", afterStart.FastCGIPort, beforeStop.FastCGIPort)
	}
	if afterStart.Status != model.StatusRunning {
		t.Fatalf("blog status after start = %q, want running", afterStart.Status)
	}
	assertPHPResponse(t, h.directBody(afterStart), phpVersion, "512M", true, "updated")
	t.Logf("acceptance: stop/start kept build %s and healthy master on port %d", afterStart.PHPBuild, afterStart.FastCGIPort)

	// Doctor PHP checks use the real store and the real PHP validator.
	report := h.runDoctor()
	phpChecks := 0
	for _, check := range report.Checks {
		if !strings.HasPrefix(check.ID, "php-") {
			continue
		}
		phpChecks++
		if check.Status != doctor.StatusOK {
			t.Fatalf("doctor %s = %s: %s", check.ID, check.Status, check.Detail)
		}
		t.Logf("acceptance: doctor %s: %s", check.ID, check.Detail)
	}
	if phpChecks != 4 {
		t.Fatalf("doctor returned %d PHP checks, want 4", phpChecks)
	}
}

// systemPHPRoot resolves the PHP installation used by this run and skips when
// it is absent or when the test runs as root.
func systemPHPRoot(t *testing.T) string {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("PHP acceptance starts an unprivileged php-fpm master; run as a non-root user")
	}
	root := strings.TrimSpace(os.Getenv(phpRootEnv))
	if root == "" {
		root = defaultSystemPHPRoot
	}
	root = filepath.Clean(root)
	for _, rel := range []string{"bin/php", "sbin/php-fpm"} {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Skipf("PHP acceptance skipped: %s is not available under %s: %v", rel, root, err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			t.Skipf("PHP acceptance skipped: %s under %s is not executable", rel, root)
		}
	}
	return root
}

func systemPHPVersion(t *testing.T, root string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, filepath.Join(root, "bin", "php"), "-r", `echo PHP_VERSION;`).Output()
	if err != nil {
		t.Fatalf("read system PHP version: %v", err)
	}
	version := strings.TrimSpace(string(output))
	if _, err := catalog.ParseID(catalog.FormatID("php", version, 1)); err != nil {
		t.Fatalf("system PHP reported an unusable version %q: %v", version, err)
	}
	return version
}

// systemPHPExtensionDir mirrors sites.phpExtensionDir without importing the
// unexported helper: a relocatable system build keeps the shared objects either
// directly under lib/php/extensions or in exactly one architecture subdirectory.
func systemPHPExtensionDir(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "lib", "php", "extensions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read PHP extension directory %s: %v", dir, err)
	}
	var subdirectories []string
	hasSharedObject := false
	for _, entry := range entries {
		if entry.IsDir() {
			subdirectories = append(subdirectories, entry.Name())
			continue
		}
		if strings.HasSuffix(entry.Name(), ".so") {
			hasSharedObject = true
		}
	}
	switch {
	case hasSharedObject:
		return dir
	case len(subdirectories) == 1:
		return filepath.Join(dir, subdirectories[0])
	default:
		t.Fatalf("PHP extension directory %s has no unambiguous shared-object location", dir)
		return ""
	}
}

// systemPHPModules returns the module names printed by the system `php -m`.
func systemPHPModules(t *testing.T, root string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, filepath.Join(root, "bin", "php"), "-m").Output()
	if err != nil {
		t.Fatalf("read system PHP modules: %v", err)
	}
	var modules []string
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "[") {
			continue
		}
		modules = append(modules, line)
	}
	return modules
}

var manifestModulePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// filteredPHPManifest builds a catalog matching the real system installation.
// It keeps only shared modules whose .so exists and which support PHP 8.4,
// preserves their type/SO/default/requires fields, drops requires that cannot be
// satisfied without pretending an absent .so exists, and classifies a remaining
// `php -m` module as built in only when no matching .so exists. Uncatalogued
// shared modules stay out of both lists: the generated ini cannot load them and
// they are not compiled into the binary.
func filteredPHPManifest(t *testing.T, extensionDir string, modules []string) catalog.PHPManifest {
	t.Helper()
	base, err := catalog.LoadPHP()
	if err != nil {
		t.Fatalf("load embedded PHP manifest: %v", err)
	}
	defaults, ok := base.DefaultsFor(acceptancePHPLine)
	if !ok {
		t.Fatalf("embedded manifest has no PHP %s defaults", acceptancePHPLine)
	}
	presentSO := extensionSharedObjects(t, extensionDir)

	shared := make(map[string]bool)
	var extensions []catalog.Extension
	for _, extension := range base.Extensions {
		if !containsString(extension.Versions, acceptancePHPLine) {
			continue
		}
		if !presentSO[extension.SO] {
			continue
		}
		extension.Versions = []string{acceptancePHPLine}
		shared[extension.Name] = true
		extensions = append(extensions, extension)
	}
	if len(extensions) == 0 {
		t.Fatalf("no embedded PHP extension has a .so in %s", extensionDir)
	}
	for i := range extensions {
		requires := make([]string, 0, len(extensions[i].Requires))
		for _, dependency := range extensions[i].Requires {
			if shared[dependency] {
				requires = append(requires, dependency)
			}
		}
		extensions[i].Requires = requires
	}

	builtin := make([]string, 0, len(base.Builtin)+len(modules))
	seen := make(map[string]bool)
	addBuiltin := func(name string) {
		name = normalizePHPMODule(name)
		if name == "" || shared[name] || seen[name] {
			return
		}
		// A matching shared object means the system loaded this module from
		// php.d; it is not compiled into the binary. It may be uncatalogued
		// (for example mongodb or swoole), in which case devstack must neither
		// toggle it nor claim it is built in.
		if presentSO[name+".so"] {
			return
		}
		if !manifestModulePattern.MatchString(name) {
			return
		}
		seen[name] = true
		builtin = append(builtin, name)
	}
	for _, name := range base.Builtin {
		addBuiltin(name)
	}
	for _, name := range modules {
		addBuiltin(name)
	}

	versionExtensions := make(map[string]bool)
	for name, enabled := range defaults.Extensions {
		if shared[name] {
			versionExtensions[name] = enabled
		}
	}

	manifest := catalog.PHPManifest{
		Builtin:    builtin,
		Extensions: extensions,
		Versions: map[string]catalog.PHPVersionDefaults{
			acceptancePHPLine: {
				Ini:        copyStringMap(defaults.Ini),
				Extensions: versionExtensions,
			},
		},
	}
	// Round-trip through the catalog parser so the test fails loudly if the
	// filtered manifest ever violates the production invariants.
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode filtered PHP manifest: %v", err)
	}
	validated, err := catalog.ParsePHP(data)
	if err != nil {
		t.Fatalf("filtered PHP manifest is invalid: %v", err)
	}
	return validated
}

// extensionSharedObjects returns the basenames of every shared object in the
// real extension directory.
func extensionSharedObjects(t *testing.T, extensionDir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(extensionDir)
	if err != nil {
		t.Fatalf("read PHP extension directory %s: %v", extensionDir, err)
	}
	shared := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".so") {
			shared[entry.Name()] = true
		}
	}
	if len(shared) == 0 {
		t.Fatalf("PHP extension directory %s has no shared objects", extensionDir)
	}
	return shared
}

// normalizePHPMODule folds the display names `php -m` uses into catalog names.
func normalizePHPMODule(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "zend opcache":
		return "opcache"
	default:
		return name
	}
}

// uncataloguedSharedModules lists .so files present on the system that the
// filtered manifest does not represent. The generated devstack ini does not
// load them, but they must not be misclassified as built into PHP.
func uncataloguedSharedModules(t *testing.T, extensionDir string, manifest catalog.PHPManifest) []string {
	t.Helper()
	present := extensionSharedObjects(t, extensionDir)
	represented := make(map[string]bool, len(manifest.Extensions))
	for _, extension := range manifest.Extensions {
		represented[extension.SO] = true
	}
	var uncatalogued []string
	for name := range present {
		if represented[name] {
			continue
		}
		uncatalogued = append(uncatalogued, strings.TrimSuffix(name, ".so"))
	}
	sort.Strings(uncatalogued)
	return uncatalogued
}

func copyStringMap(source map[string]string) map[string]string {
	copied := make(map[string]string, len(source))
	for key, value := range source {
		copied[key] = value
	}
	return copied
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func manifestHasExtension(manifest catalog.PHPManifest, name string) bool {
	for _, extension := range manifest.Extensions {
		if extension.Name == name {
			return true
		}
	}
	return false
}

func extensionNames(extensions []catalog.Extension) string {
	names := make([]string, 0, len(extensions))
	for _, extension := range extensions {
		names = append(names, extension.Name)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// acceptanceHarness groups the real services used by the test.
type acceptanceHarness struct {
	t         *testing.T
	home      string
	paths     config.Paths
	cfg       config.Config
	store     *store.Store
	logs      *logs.Store
	local     *backend.Local
	router    *proxy.Router
	ports     *domain.PortAllocator
	certs     *certs.Manager
	svc       *sites.Service
	manifest  catalog.PHPManifest
	buildID   string
	siteNames []string
	// trackedPorts is every FastCGI port a site in this run has owned. Cleanup
	// proves each one is free after StopAll so a leaked master cannot pass.
	trackedPorts map[int]string
}

func newAcceptanceHarness(t *testing.T, root, phpVersion string, manifest catalog.PHPManifest) *acceptanceHarness {
	t.Helper()
	home := t.TempDir()
	paths := config.NewPaths(home)
	if err := paths.Ensure(); err != nil {
		t.Fatalf("create acceptance home: %v", err)
	}
	st, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("open acceptance store: %v", err)
	}
	logStore, err := logs.New(paths.Logs, 10, 5)
	if err != nil {
		_ = st.Close()
		t.Fatalf("open acceptance log store: %v", err)
	}
	cfg := config.Default()
	cfg.Home = home

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	local, err := backend.NewLocal(cfg, st, logStore, nil, self)
	if err != nil {
		t.Fatalf("create local backend: %v", err)
	}
	router := proxy.NewRouter()
	ports := domain.NewPortAllocator()
	certManager := certs.NewManager(st, newAcceptanceSigner(t), paths.PKI, nil)
	buildID := catalog.FormatID("php", phpVersion, 1)
	if err := st.UpsertRuntime(context.Background(), model.RuntimeBuild{
		ID:          buildID,
		Kind:        "php",
		Version:     phpVersion,
		Backend:     model.BackendLocal,
		Path:        root,
		InstalledAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("register %s: %v", buildID, err)
	}

	h := &acceptanceHarness{
		t:            t,
		home:         home,
		paths:        paths,
		cfg:          cfg,
		store:        st,
		logs:         logStore,
		local:        local,
		router:       router,
		ports:        ports,
		certs:        certManager,
		manifest:     manifest,
		buildID:      buildID,
		trackedPorts: make(map[int]string),
	}
	h.svc = sites.NewService(sites.Deps{
		Store:       st,
		Backend:     local,
		Helper:      noopHosts{},
		Certs:       certManager,
		Vhosts:      noopVhosts{},
		Proxy:       noopProxy{},
		Router:      router,
		Ports:       ports,
		Config:      cfg,
		Logs:        logStore,
		PHP:         sites.NewPHPCommands(),
		PHPBackend:  local,
		PHPManifest: &h.manifest,
	})
	t.Cleanup(h.cleanup)
	return h
}

func (h *acceptanceHarness) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	for _, name := range h.siteNames {
		if err := h.svc.Delete(ctx, name); err != nil && !errors.Is(err, store.ErrNotFound) {
			h.t.Errorf("cleanup: delete %s: %v", name, err)
		}
	}
	if err := h.local.StopAll(ctx); err != nil {
		h.t.Errorf("cleanup: stop all backends: %v", err)
	}
	for _, port := range sortedPorts(h.trackedPorts) {
		if err := waitPortFree(port, 10*time.Second); err != nil {
			h.t.Errorf("cleanup: %s port %d is still bound after StopAll: %v", h.trackedPorts[port], port, err)
			continue
		}
		h.t.Logf("cleanup: %s FastCGI port %d is free after StopAll (127.0.0.1 bind succeeded)", h.trackedPorts[port], port)
	}
	if err := h.logs.Close(); err != nil {
		h.t.Errorf("cleanup: close logs: %v", err)
	}
	if err := h.store.Close(); err != nil {
		h.t.Errorf("cleanup: close store: %v", err)
	}
}

// notePort records a FastCGI port owned by a site so cleanup can prove every
// listener is gone after StopAll.
func (h *acceptanceHarness) notePort(site model.Site) {
	if site.FastCGIPort == 0 {
		return
	}
	h.trackedPorts[site.FastCGIPort] = site.Name
}

func sortedPorts(ports map[int]string) []int {
	sorted := make([]int, 0, len(ports))
	for port := range ports {
		sorted = append(sorted, port)
	}
	sort.Ints(sorted)
	return sorted
}

func (h *acceptanceHarness) createSite(name, project string, extensions map[string]bool) model.Site {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	site, err := h.svc.Create(ctx, sites.CreateRequest{
		Name:          name,
		Path:          project,
		Kind:          string(model.SitePHP),
		PHPVersion:    acceptancePHPLine,
		PHPBuild:      h.buildID,
		PHPExtensions: extensions,
	})
	if err != nil {
		h.t.Fatalf("create PHP site %s: %v", name, err)
	}
	h.siteNames = append(h.siteNames, site.Name)
	h.notePort(site)
	return site
}

func (h *acceptanceHarness) applyPHP(name string, change sites.PHPChange) model.Site {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	site, err := h.svc.ApplyPHP(ctx, name, change)
	if err != nil {
		h.t.Fatalf("apply PHP change to %s: %v", name, err)
	}
	h.notePort(site)
	return site
}

func (h *acceptanceHarness) getSite(name string) model.Site {
	h.t.Helper()
	site, err := h.store.GetSiteByName(context.Background(), name)
	if err != nil {
		h.t.Fatalf("read site %s: %v", name, err)
	}
	h.notePort(site)
	return site
}

func (h *acceptanceHarness) upsertBuild(build model.RuntimeBuild) {
	h.t.Helper()
	if err := h.store.UpsertRuntime(context.Background(), build); err != nil {
		h.t.Fatalf("register runtime %s: %v", build.ID, err)
	}
}

func (h *acceptanceHarness) directBody(site model.Site) string {
	h.t.Helper()
	return fastCGIBody(h.t, site.FastCGIPort, documentRootOf(site))
}

func (h *acceptanceHarness) runDoctor() doctor.Report {
	h.t.Helper()
	hostsPath := filepath.Join(h.home, "acceptance-hosts")
	if err := os.WriteFile(hostsPath, nil, 0o644); err != nil {
		h.t.Fatalf("create doctor hosts file: %v", err)
	}
	service := doctor.NewService(doctor.Deps{
		Config:    h.cfg,
		Home:      h.home,
		Store:     h.store,
		PHP:       h.svc,
		Vhosts:    noopVhosts{},
		HostsPath: hostsPath,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	report, err := service.Run(ctx)
	if err != nil {
		h.t.Fatalf("run doctor: %v", err)
	}
	return report
}

func documentRootOf(site model.Site) string {
	root := strings.TrimSpace(site.DocumentRoot)
	if root == "" || root == "." {
		return filepath.Clean(site.BackendPath)
	}
	if filepath.IsAbs(root) {
		return filepath.Clean(root)
	}
	return filepath.Clean(filepath.Join(site.BackendPath, root))
}

func writeProject(t *testing.T, home, name, docrootLine string) string {
	t.Helper()
	project := filepath.Join(home, "projects", name)
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("create project %s: %v", project, err)
	}
	writeIndexPHP(t, project, docrootLine)
	return project
}

func writeIndexPHP(t *testing.T, dir, docrootLine string) {
	t.Helper()
	body := fmt.Sprintf(`<?php
header('Content-Type: text/plain');
echo "PHP_VERSION=", PHP_VERSION, "\n";
echo "memory_limit=", ini_get('memory_limit'), "\n";
echo "xdebug=", extension_loaded('xdebug') ? "on" : "off", "\n";
echo %q, "\n";
`, docrootLine)
	if err := os.WriteFile(filepath.Join(dir, "index.php"), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s/index.php: %v", dir, err)
	}
}

func assertPHPResponse(t *testing.T, body, wantVersion, wantMemory string, wantXdebug bool, wantDocroot string) {
	t.Helper()
	if got := responseField(body, "PHP_VERSION"); got != wantVersion {
		t.Fatalf("PHP_VERSION = %q, want %q (body: %q)", got, wantVersion, body)
	}
	if got := responseField(body, "memory_limit"); got != wantMemory {
		t.Fatalf("memory_limit = %q, want %q (body: %q)", got, wantMemory, body)
	}
	wantXdebugValue := "off"
	if wantXdebug {
		wantXdebugValue = "on"
	}
	if got := responseField(body, "xdebug"); got != wantXdebugValue {
		t.Fatalf("xdebug = %q, want %q (body: %q)", got, wantXdebugValue, body)
	}
	if got := responseField(body, "DOCROOT"); got != wantDocroot {
		t.Fatalf("DOCROOT = %q, want %q (body: %q)", got, wantDocroot, body)
	}
}

func responseField(body, key string) string {
	prefix := key + "="
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	return ""
}

func fastCGIBody(t *testing.T, port int, documentRoot string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	client, err := fcgi.Dial(ctx, addr)
	if err != nil {
		t.Fatalf("dial FastCGI %s: %v", addr, err)
	}
	defer func() { _ = client.Close() }()
	script := filepath.Join(documentRoot, "index.php")
	response, err := client.Do(ctx, fcgi.Request{
		Params: map[string]string{
			"GATEWAY_INTERFACE": "CGI/1.1",
			"REQUEST_METHOD":    "GET",
			"REQUEST_URI":       "/index.php",
			"SCRIPT_FILENAME":   script,
			"SCRIPT_NAME":       "/index.php",
			"SERVER_PROTOCOL":   "HTTP/1.1",
			"SERVER_SOFTWARE":   "devstack",
			"REMOTE_ADDR":       "127.0.0.1",
			"SERVER_ADDR":       "127.0.0.1",
			"SERVER_NAME":       "localhost",
			"SERVER_PORT":       strconv.Itoa(port),
			"DOCUMENT_ROOT":     documentRoot,
			"QUERY_STRING":      "",
		},
		BodyLength: 0,
	})
	if err != nil {
		t.Fatalf("FastCGI request to %s: %v", addr, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatalf("read FastCGI response: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("FastCGI response status = %d, want 200 (body: %s)", response.StatusCode, body)
	}
	return string(body)
}

// waitPortFree waits until a TCP listener can bind 127.0.0.1:port and closes
// it. Unlike PortAllocator.Reserve, this proves the kernel listener is gone.
func waitPortFree(port int, timeout time.Duration) error {
	if port <= 0 {
		return fmt.Errorf("invalid port %d", port)
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		listener, err := net.Listen("tcp", addr)
		if err == nil {
			_ = listener.Close()
			return nil
		}
		lastErr = err
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("port %d on 127.0.0.1 is still bound after %s: %w", port, timeout, lastErr)
}

func requirePortFree(t *testing.T, port int, label string, timeout time.Duration) {
	t.Helper()
	if err := waitPortFree(port, timeout); err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	t.Logf("acceptance: %s FastCGI port %d is free (bind 127.0.0.1:%d succeeded)", label, port, port)
}

func proxyGet(t *testing.T, router *proxy.Router, host, path string) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://"+host+path, nil)
	recorder := httptest.NewRecorder()
	router.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("proxy %s%s status = %d, want 200 (body: %s)", host, path, recorder.Code, recorder.Body.String())
	}
	return recorder.Body.String()
}

func assertSiteRowUnchanged(t *testing.T, before, after model.Site) {
	t.Helper()
	if before.PHPBuild != after.PHPBuild ||
		before.PHPIniRaw != after.PHPIniRaw ||
		before.PHPConfigHash != after.PHPConfigHash ||
		before.FastCGIPort != after.FastCGIPort ||
		before.PHPVersion != after.PHPVersion ||
		before.DocumentRoot != after.DocumentRoot ||
		!equalStringMap(before.PHPIni, after.PHPIni) ||
		!equalBoolMap(before.PHPExtensions, after.PHPExtensions) {
		t.Fatalf("failed apply changed the site row: before %+v after %+v", before, after)
	}
}

func equalStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func equalBoolMap(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func assertRouteAndVhostPort(t *testing.T, h *acceptanceHarness, site model.Site, wantPort int) {
	t.Helper()
	route, ok := h.router.Lookup(site.Domain)
	if !ok {
		t.Fatalf("route for %s is missing", site.Domain)
	}
	wantUpstream := net.JoinHostPort("127.0.0.1", strconv.Itoa(wantPort))
	if route.Upstream != wantUpstream {
		t.Fatalf("route upstream for %s = %q, want %q", site.Domain, route.Upstream, wantUpstream)
	}
	if route.DocumentRoot != documentRootOf(site) {
		t.Fatalf("route document root for %s = %q, want %q", site.Domain, route.DocumentRoot, documentRootOf(site))
	}
	preview, err := h.svc.PreviewVhost(context.Background(), site.Name, string(vhost.ServerNginx))
	if err != nil {
		t.Fatalf("preview vhost after failed apply: %v", err)
	}
	if !strings.Contains(preview, fmt.Sprintf("fastcgi_pass 127.0.0.1:%d;", wantPort)) {
		t.Fatalf("vhost after failed apply does not point at port %d:\n%s", wantPort, preview)
	}
}

func assertNginxPHPVhost(t *testing.T, config string, port int, site model.Site) {
	t.Helper()
	for _, want := range []string{
		"fastcgi_pass 127.0.0.1:" + strconv.Itoa(port) + ";",
		"fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;",
		"fastcgi_param PATH_INFO $fastcgi_path_info;",
		"fastcgi_index index.php;",
		"index index.php index.html;",
		`location ~* \.php(?:~|\.(?:bak|orig|save|old|swp|tmp))$`,
		"root " + documentRootOf(site) + ";",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("nginx PHP vhost is missing %q:\n%s", want, config)
		}
	}
}

func assertApachePHPVhost(t *testing.T, config string, port int, site model.Site) {
	t.Helper()
	for _, want := range []string{
		fmt.Sprintf(`SetHandler "proxy:fcgi://127.0.0.1:%d"`, port),
		"DirectoryIndex index.php index.html",
		`<FilesMatch "(?i)\.php(~|\.(bak|orig|save|old|swp|tmp))$">`,
		`DocumentRoot "` + documentRootOf(site) + `"`,
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("Apache PHP vhost is missing %q:\n%s", want, config)
		}
	}
}

// writeIncompletePHPBuild creates a candidate tree with bin/php and the
// extension directory but deliberately no sbin/php-fpm.
func writeIncompletePHPBuild(t *testing.T, root string) string {
	t.Helper()
	buildRoot := filepath.Join(t.TempDir(), "php-incomplete")
	if err := os.MkdirAll(filepath.Join(buildRoot, "bin"), 0o755); err != nil {
		t.Fatalf("create incomplete build bin: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "bin", "php"), filepath.Join(buildRoot, "bin", "php")); err != nil {
		t.Fatalf("link incomplete build php: %v", err)
	}
	libDir := filepath.Join(buildRoot, "lib", "php")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatalf("create incomplete build lib: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "lib", "php", "extensions"), filepath.Join(libDir, "extensions")); err != nil {
		t.Fatalf("link incomplete build extensions: %v", err)
	}
	return buildRoot
}

// chooseFreeFastCGIPort selects a port that can be bound right now and is not
// already owned by a live site master.
func chooseFreeFastCGIPort(t *testing.T, avoid ...int) int {
	t.Helper()
	skip := make(map[int]bool, len(avoid))
	for _, port := range avoid {
		skip[port] = true
	}
	for port := 43999; port >= 43000; port-- {
		if skip[port] {
			continue
		}
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err == nil {
			_ = listener.Close()
			return port
		}
	}
	t.Fatal("no free FastCGI port is available for the failed-switch check")
	return 0
}

// reserveAllFastCGIPortsExcept reserves every FastCGI port except candidate and
// the ports already owned by live sites, forcing the attempted switch to use
// candidate. The returned function releases the reservations.
func reserveAllFastCGIPortsExcept(t *testing.T, ports *domain.PortAllocator, candidate int, inUse ...int) func() {
	t.Helper()
	owned := make(map[int]bool, len(inUse))
	for _, port := range inUse {
		owned[port] = true
	}
	var reserved []int
	for port := 43000; port <= 43999; port++ {
		if port == candidate || owned[port] {
			continue
		}
		if err := ports.Reserve(port); err != nil {
			if strings.Contains(err.Error(), "already reserved") {
				continue
			}
			t.Fatalf("reserve FastCGI port %d: %v", port, err)
		}
		reserved = append(reserved, port)
	}
	return func() {
		for _, port := range reserved {
			ports.Release(port)
		}
	}
}

// newAcceptanceSigner creates an in-memory root CA for the real certificate
// manager. It issues a leaf from the CSR and exposes the root as the only trust
// anchor, matching the production helper's contract.
type acceptanceSigner struct {
	mu     sync.Mutex
	caKey  *ecdsa.PrivateKey
	caCert *x509.Certificate
	caPEM  []byte
}

func newAcceptanceSigner(t *testing.T) *acceptanceSigner {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate acceptance CA key: %v", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("generate acceptance CA serial: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "devstack acceptance CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		SignatureAlgorithm:    x509.ECDSAWithSHA256,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create acceptance CA certificate: %v", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse acceptance CA certificate: %v", err)
	}
	return &acceptanceSigner{
		caKey:  key,
		caCert: certificate,
		caPEM:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
	}
}

func (s *acceptanceSigner) RootPEM() []byte {
	return append([]byte(nil), s.caPEM...)
}

func (s *acceptanceSigner) SignCertificate(ctx context.Context, csrPEM []byte, domains []string) (helperproto.SignResponse, error) {
	if err := ctx.Err(); err != nil {
		return helperproto.SignResponse{}, err
	}
	if len(domains) == 0 {
		return helperproto.SignResponse{}, errors.New("acceptance signer: no domains")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	block, _ := pem.Decode(csrPEM)
	if block == nil {
		return helperproto.SignResponse{}, errors.New("acceptance signer: CSR is not PEM")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return helperproto.SignResponse{}, fmt.Errorf("acceptance signer: parse CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return helperproto.SignResponse{}, fmt.Errorf("acceptance signer: CSR signature: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return helperproto.SignResponse{}, err
	}
	now := time.Now().UTC().Truncate(time.Second)
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: domains[0]},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(90 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              append([]string(nil), domains...),
		SignatureAlgorithm:    x509.ECDSAWithSHA256,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, s.caCert, csr.PublicKey, s.caKey)
	if err != nil {
		return helperproto.SignResponse{}, fmt.Errorf("acceptance signer: sign: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return helperproto.SignResponse{}, err
	}
	return helperproto.SignResponse{
		CertPEM:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		Serial:   serial.Text(16),
		NotAfter: leaf.NotAfter,
	}, nil
}

// noopHosts makes the hosts block a no-op for the acceptance run.
type noopHosts struct{}

func (noopHosts) SyncHosts(context.Context, []string) error { return nil }

// noopVhosts implements both the sites and doctor vhost surfaces for builtin
// serving mode.
type noopVhosts struct{}

func (noopVhosts) VhostStatus(context.Context) (helperproto.VhostStatusResponse, error) {
	return helperproto.VhostStatusResponse{Mode: config.ServingModeBuiltin, ConfigOK: true}, nil
}

func (noopVhosts) SyncVhosts(context.Context, helperproto.VHostSyncRequest) error { return nil }

func (noopVhosts) SetServingMode(context.Context, string, helperproto.VHostSyncRequest) (helperproto.VhostStatusResponse, error) {
	return helperproto.VhostStatusResponse{Mode: config.ServingModeBuiltin, ConfigOK: true}, nil
}

func (noopVhosts) RemoveVhosts(context.Context) error { return nil }

func (noopVhosts) ServingMode() string { return config.ServingModeBuiltin }

// noopProxy is enough because builtin mode never starts the real listener in
// this test; the router is exercised directly through its Handler.
type noopProxy struct{}

func (noopProxy) Start(context.Context) error { return nil }
func (noopProxy) Stop(context.Context) error  { return nil }

func timeoutContext(t *testing.T, timeout time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	return ctx
}

// vhostSet renders the full external-server file set for the acceptance
// templates using high listen ports and a temporary TLS directory.
func (h *acceptanceHarness) vhostSet(t *testing.T, server vhost.Server, site model.Site, httpPort, httpsPort int, tlsDir string) map[string][]byte {
	t.Helper()
	dashboardRecord, err := h.certs.Ensure(context.Background(), certs.DashboardOwner, []string{"devstack.test"})
	if err != nil {
		t.Fatalf("ensure dashboard certificate: %v", err)
	}
	dashboardCert, dashboardKey, err := h.certs.PEM(dashboardRecord)
	if err != nil {
		t.Fatalf("read dashboard certificate: %v", err)
	}
	spec := vhost.SiteSpec{
		ID:              site.ID,
		Name:            site.Name,
		Domain:          site.Domain,
		WWW:             site.WWW,
		Kind:            site.Kind,
		DocumentRoot:    documentRootOf(site),
		BackendPort:     site.FastCGIPort,
		TLS:             site.TLS,
		ForceHTTPS:      site.ForceHTTPS,
		SPAFallback:     site.SPAFallback,
		FrontController: site.FrontController,
		DesiredState:    site.DesiredState,
	}
	if site.TLS {
		record, err := h.store.GetCertBySite(context.Background(), site.ID)
		if err != nil {
			t.Fatalf("read site certificate: %v", err)
		}
		spec.CertPEM, spec.KeyPEM, err = h.certs.PEM(record)
		if err != nil {
			t.Fatalf("read site certificate PEM: %v", err)
		}
	}
	files, err := vhost.Render(vhost.Set{
		Server:          server,
		Sites:           []vhost.SiteSpec{spec},
		Dashboard:       vhost.DashboardSpec{Domain: "devstack.test", UIPort: h.cfg.UIPort, CertPEM: dashboardCert, KeyPEM: dashboardKey},
		MaxBodyBytes:    int64(h.cfg.MaxRequestBodyMB) << 20,
		TLSDir:          tlsDir,
		HTTPListenPort:  httpPort,
		HTTPSListenPort: httpsPort,
	})
	if err != nil {
		t.Fatalf("render %s vhost set: %v", server, err)
	}
	return files
}

func writeFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}

func testRealNginxConfig(t *testing.T, h *acceptanceHarness, site model.Site) {
	t.Helper()
	nginx := findExecutable("nginx", "/usr/local/nginx/sbin/nginx", "/usr/sbin/nginx")
	if nginx == "" {
		t.Skip("nginx is not installed; external nginx config test skipped")
	}
	prefix := t.TempDir()
	tlsDir := filepath.Join(prefix, "tls")
	vhostsDir := filepath.Join(prefix, "vhosts")
	logsDir := filepath.Join(prefix, "logs")
	for _, dir := range []string{tlsDir, vhostsDir, logsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create nginx prefix directory: %v", err)
		}
	}
	// Render the TLS branch too, with the real site certificate, so nginx -t
	// parses both the HTTP and HTTPS PHP server blocks.
	site.TLS = true
	site.ForceHTTPS = false
	files := h.vhostSet(t, vhost.ServerNginx, site, 18080, 18443, tlsDir)
	siteConfig, ok := files[vhostSiteFilename(site)]
	if !ok {
		t.Fatalf("rendered nginx set is missing the site vhost")
	}
	for _, want := range []string{"listen 18080;", "listen 18443 ssl http2;"} {
		if !strings.Contains(string(siteConfig), want) {
			t.Fatalf("nginx site vhost is missing %q", want)
		}
	}
	dashboardRecord, err := h.certs.Ensure(context.Background(), certs.DashboardOwner, []string{"devstack.test"})
	if err != nil {
		t.Fatalf("ensure dashboard certificate: %v", err)
	}
	dashboardCert, dashboardKey, err := h.certs.PEM(dashboardRecord)
	if err != nil {
		t.Fatalf("read dashboard certificate: %v", err)
	}
	writeFile(t, filepath.Join(tlsDir, "__dashboard__.crt"), dashboardCert, 0o644)
	writeFile(t, filepath.Join(tlsDir, "__dashboard__.key"), dashboardKey, 0o600)
	siteRecord, err := h.store.GetCertBySite(context.Background(), site.ID)
	if err != nil {
		t.Fatalf("read site certificate: %v", err)
	}
	siteCert, siteKey, err := h.certs.PEM(siteRecord)
	if err != nil {
		t.Fatalf("read site certificate PEM: %v", err)
	}
	writeFile(t, filepath.Join(tlsDir, site.ID+".crt"), siteCert, 0o644)
	writeFile(t, filepath.Join(tlsDir, site.ID+".key"), siteKey, 0o600)
	for name, data := range files {
		writeFile(t, filepath.Join(vhostsDir, name), data, 0o644)
	}
	nginxConfig := fmt.Sprintf(`worker_processes 1;
pid %s;
error_log %s notice;
events { worker_connections 128; }
http {
    include %s;
}
`, filepath.Join(prefix, "nginx.pid"), filepath.Join(prefix, "error.log"), filepath.Join(vhostsDir, "*.conf"))
	configPath := filepath.Join(prefix, "nginx.conf")
	writeFile(t, configPath, []byte(nginxConfig), 0o644)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, nginx, "-t", "-p", prefix+string(os.PathSeparator), "-c", configPath).CombinedOutput()
	if err != nil {
		t.Fatalf("nginx -t failed: %v\n%s", err, output)
	}
	t.Logf("nginx -t: %s", strings.TrimSpace(string(output)))
}

func testRealApacheConfig(t *testing.T, h *acceptanceHarness, site model.Site) {
	t.Helper()
	httpd := findExecutable("httpd", "apache2", "apachectl")
	if httpd == "" {
		t.Skip("Apache httpd/apachectl is not installed; external Apache config test skipped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	modules, err := exec.CommandContext(ctx, httpd, "-M").Output()
	if err != nil {
		t.Skipf("cannot enumerate Apache modules with %s -M: %v", httpd, err)
	}
	moduleList := string(modules)
	var missing []string
	for _, module := range []string{"ssl_module", "proxy_fcgi_module"} {
		if !strings.Contains(moduleList, module) {
			missing = append(missing, module)
		}
	}
	if len(missing) != 0 {
		version := apacheVersion(ctx, httpd)
		t.Skipf("%s is installed but %s not loaded; PHP vhost configtest skipped", version, strings.Join(missing, " and "))
	}
	configFile := apacheConfigFile(ctx, httpd)
	if configFile == "" {
		t.Skip("cannot locate the Apache server configuration; configtest skipped")
	}
	prefix := t.TempDir()
	files := h.vhostSet(t, vhost.ServerApache, site, 18080, 18443, filepath.Join(prefix, "tls"))
	content, ok := files[vhostSiteFilename(site)]
	if !ok {
		t.Fatalf("rendered Apache set is missing the site vhost")
	}
	httpOnly := firstApacheVirtualHost(t, string(content), 18080)
	vhostPath := filepath.Join(prefix, "php-http-vhost.conf")
	writeFile(t, vhostPath, []byte(httpOnly), 0o644)
	// -c processes the directive after the main config, so Include sees the
	// real module and handler definitions. -C would include the vhost first and
	// fail on SetHandler/proxy_fcgi even when the server is healthy.
	output, err := exec.CommandContext(ctx, httpd, "-t", "-f", configFile, "-c", "Include "+vhostPath).CombinedOutput()
	if err != nil {
		t.Fatalf("Apache configtest failed: %v\n%s", err, output)
	}
	t.Logf("Apache configtest: %s", strings.TrimSpace(string(output)))
}

func vhostSiteFilename(site model.Site) string {
	id := site.ID
	if len(id) > 8 {
		id = id[:8]
	}
	return site.Name + "-" + id + ".conf"
}

func firstApacheVirtualHost(t *testing.T, config string, port int) string {
	t.Helper()
	open := fmt.Sprintf("<VirtualHost *:%d>", port)
	start := strings.Index(config, open)
	if start < 0 {
		t.Fatalf("Apache config has no %s block", open)
	}
	end := strings.Index(config[start:], "</VirtualHost>")
	if end < 0 {
		t.Fatalf("Apache %s block is not closed", open)
	}
	return config[start : start+end+len("</VirtualHost>")]
}

func apacheVersion(ctx context.Context, binary string) string {
	output, err := exec.CommandContext(ctx, binary, "-v").Output()
	if err != nil {
		return "unknown"
	}
	line := strings.TrimSpace(string(output))
	if index := strings.IndexByte(line, '\n'); index >= 0 {
		line = strings.TrimSpace(line[:index])
	}
	return strings.TrimPrefix(line, "Server version: ")
}

func apacheConfigFile(ctx context.Context, binary string) string {
	output, err := exec.CommandContext(ctx, binary, "-V").Output()
	if err == nil {
		match := regexp.MustCompile(`SERVER_CONFIG_FILE="([^"]+)"`).FindStringSubmatch(string(output))
		if match != nil {
			if _, err := os.Stat(match[1]); err == nil {
				return match[1]
			}
		}
	}
	for _, candidate := range []string{"/etc/httpd/conf/httpd.conf", "/etc/apache2/apache2.conf"} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func findExecutable(names ...string) string {
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
		if info, err := os.Stat(name); err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0 {
			return name
		}
	}
	return ""
}
