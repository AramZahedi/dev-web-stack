//go:build !windows

package terminal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/tooling"
)

func TestPrepareUsesSiteShellEnvironment(t *testing.T) {
	home := t.TempDir()
	paths, site, build, info := testPHPFixture(t, home)
	testGlobalPHPScript(t, paths)

	deps := testDeps(paths)
	deps.Runtimes = staticRuntimes{build}
	deps.PHP = staticPHP{info: info}
	manager := NewManager(deps)
	t.Cleanup(manager.Close)

	prepared, err := manager.prepare(context.Background(), site)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	if prepared.Dir != site.HostPath {
		t.Fatalf("cwd = %q, want %q", prepared.Dir, site.HostPath)
	}
	env := envMap(t, prepared.Env)
	if env["DEVSTACK_SITE"] != site.Name {
		t.Fatalf("DEVSTACK_SITE = %q", env["DEVSTACK_SITE"])
	}
	if env["DEVSTACK_HOME"] != paths.Home {
		t.Fatalf("DEVSTACK_HOME = %q, want %q", env["DEVSTACK_HOME"], paths.Home)
	}
	if env["DEVSTACK_PROJECT"] != site.HostPath {
		t.Fatalf("DEVSTACK_PROJECT = %q, want %q", env["DEVSTACK_PROJECT"], site.HostPath)
	}
	if env["APP_ENV"] != "local" {
		t.Fatalf("site env was not forwarded: %q", env["APP_ENV"])
	}
	if env["TERM"] != "xterm-256color" {
		t.Fatalf("TERM = %q, want xterm-256color", env["TERM"])
	}
	wantIni := filepath.Join(paths.Sites, site.ID, site.PHPConfigHash, "php.ini")
	if env["PHPRC"] != wantIni {
		t.Fatalf("PHPRC = %q, want %q", env["PHPRC"], wantIni)
	}
	shim := tooling.SiteShimDir(paths, site.Name)
	parts := filepath.SplitList(env["PATH"])
	if len(parts) == 0 || parts[0] != shim {
		t.Fatalf("PATH starts with %v, want site shim %q first", parts, shim)
	}
	if prepared.Shell != "/bin/sh" {
		t.Fatalf("shell = %q", prepared.Shell)
	}
}

func TestPrepareNodeSitePutsNodeShimFirst(t *testing.T) {
	home := t.TempDir()
	paths, site, build := testNodeFixture(t, home)
	deps := testDeps(paths)
	deps.Runtimes = staticRuntimes{build}
	manager := NewManager(deps)
	t.Cleanup(manager.Close)

	prepared, err := manager.prepare(context.Background(), site)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	env := envMap(t, prepared.Env)
	shim := tooling.SiteShimDir(paths, site.Name)
	parts := filepath.SplitList(env["PATH"])
	if len(parts) == 0 || parts[0] != shim {
		t.Fatalf("PATH starts with %v, want site shim %q first", parts, shim)
	}
	if _, ok := env["PHPRC"]; ok {
		t.Fatalf("Node site unexpectedly sets PHPRC")
	}
}

func TestPrepareAddsDatabaseEnvironment(t *testing.T) {
	home := t.TempDir()
	paths, site, build := testNodeFixture(t, home)
	site.Env = map[string]string{"DB_DATABASE": "old", "APP_ENV": "local"}
	deps := testDeps(paths)
	deps.Runtimes = staticRuntimes{build}
	deps.DatabaseEnv = func(context.Context, model.Site) (map[string]string, error) {
		return map[string]string{"DB_DATABASE": "app", "DB_PASSWORD": "s3cret"}, nil
	}
	manager := NewManager(deps)
	t.Cleanup(manager.Close)

	prepared, err := manager.prepare(context.Background(), site)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	env := envMap(t, prepared.Env)
	if env["DB_DATABASE"] != "app" {
		t.Fatalf("DB_DATABASE = %q, want app", env["DB_DATABASE"])
	}
	if env["DB_PASSWORD"] != "s3cret" {
		t.Fatalf("DB_PASSWORD missing from terminal environment")
	}
	if env["APP_ENV"] != "local" {
		t.Fatalf("site env was not forwarded: %q", env["APP_ENV"])
	}
}

func TestPrepareRejectsSymlinkedProject(t *testing.T) {
	home := t.TempDir()
	paths, site, build, info := testPHPFixture(t, home)
	outside := filepath.Join(home, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(paths.Projects, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink project: %v", err)
	}
	site.HostPath = link
	site.BackendPath = link

	deps := testDeps(paths)
	deps.Runtimes = staticRuntimes{build}
	deps.PHP = staticPHP{info: info}
	manager := NewManager(deps)
	t.Cleanup(manager.Close)

	if _, err := manager.prepare(context.Background(), site); !errors.Is(err, ErrSiteInvalid) {
		t.Fatalf("prepare error = %v, want ErrSiteInvalid", err)
	}
}

func TestPrepareRejectsDocumentRootSymlinkEscape(t *testing.T) {
	home := t.TempDir()
	paths, site, build, info := testPHPFixture(t, home)
	outside := filepath.Join(home, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	projectPublic := filepath.Join(site.HostPath, "public")
	if err := os.Symlink(outside, projectPublic); err != nil {
		t.Fatalf("symlink document root: %v", err)
	}
	site.DocumentRoot = "public"

	deps := testDeps(paths)
	deps.Runtimes = staticRuntimes{build}
	deps.PHP = staticPHP{info: info}
	manager := NewManager(deps)
	t.Cleanup(manager.Close)

	if _, err := manager.prepare(context.Background(), site); err == nil {
		t.Fatal("prepare accepted an escaped document root")
	}
}

func TestPrepareRejectsMissingProjectDirectory(t *testing.T) {
	home := t.TempDir()
	paths, site, build, info := testPHPFixture(t, home)
	site.HostPath = filepath.Join(paths.Projects, "missing")

	deps := testDeps(paths)
	deps.Runtimes = staticRuntimes{build}
	deps.PHP = staticPHP{info: info}
	manager := NewManager(deps)
	t.Cleanup(manager.Close)

	if _, err := manager.prepare(context.Background(), site); err == nil {
		t.Fatal("prepare accepted a missing project directory")
	}
}

func TestManagerRefusesRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not use POSIX root UID 0 checks")
	}
	home := t.TempDir()
	paths, site, build, info := testPHPFixture(t, home)
	deps := testDeps(paths)
	deps.UID = 0
	deps.Runtimes = staticRuntimes{build}
	deps.PHP = staticPHP{info: info}
	manager := NewManager(deps)

	if _, err := manager.IssueTicket(context.Background(), site); !errors.Is(err, ErrRoot) {
		t.Fatalf("IssueTicket error = %v, want ErrRoot", err)
	}
	if _, err := manager.prepare(context.Background(), site); !errors.Is(err, ErrRoot) {
		t.Fatalf("prepare error = %v, want ErrRoot", err)
	}
}

func TestManagerEnforcesSessionLimits(t *testing.T) {
	home := t.TempDir()
	paths, site, build, info := testPHPFixture(t, home)
	other := site
	other.ID = "site-2"
	other.Name = "other"
	other.HostPath = filepath.Join(paths.Projects, "other")
	if err := os.MkdirAll(other.HostPath, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(paths.Sites, other.ID, other.PHPConfigHash, "php.ini"), "[PHP]\n")

	deps := testDeps(paths)
	deps.Runtimes = staticRuntimes{build}
	deps.PHP = staticPHP{info: info}
	deps.MaxTotal = 2
	deps.MaxPerSite = 1
	manager := NewManager(deps)
	t.Cleanup(manager.Close)
	ctx := context.Background()

	first, err := manager.prepare(ctx, site)
	if err != nil {
		t.Fatalf("prepare first: %v", err)
	}
	if err := manager.reserve(first); err != nil {
		t.Fatalf("reserve first: %v", err)
	}
	if got := manager.ActiveSessions(); got != 1 {
		t.Fatalf("active = %d, want 1", got)
	}

	second, err := manager.prepare(ctx, site)
	if err != nil {
		t.Fatalf("prepare second: %v", err)
	}
	if err := manager.reserve(second); !errors.Is(err, ErrLimit) {
		t.Fatalf("reserve second error = %v, want ErrLimit", err)
	}

	otherPrepared, err := manager.prepare(ctx, other)
	if err != nil {
		t.Fatalf("prepare other: %v", err)
	}
	if err := manager.reserve(otherPrepared); err != nil {
		t.Fatalf("reserve other: %v", err)
	}

	manager.release(first)
	manager.release(otherPrepared)
	if got := manager.ActiveSessions(); got != 0 {
		t.Fatalf("active after release = %d, want 0", got)
	}
}

func TestManagerEnforcesGlobalSessionLimit(t *testing.T) {
	home := t.TempDir()
	paths, site, build, info := testPHPFixture(t, home)
	deps := testDeps(paths)
	deps.Runtimes = staticRuntimes{build}
	deps.PHP = staticPHP{info: info}
	deps.MaxTotal = 1
	deps.MaxPerSite = 1
	manager := NewManager(deps)
	t.Cleanup(manager.Close)
	ctx := context.Background()

	first, err := manager.prepare(ctx, site)
	if err != nil {
		t.Fatalf("prepare first: %v", err)
	}
	if err := manager.reserve(first); err != nil {
		t.Fatalf("reserve first: %v", err)
	}
	second, err := manager.prepare(ctx, site)
	if err != nil {
		t.Fatalf("prepare second: %v", err)
	}
	if err := manager.reserve(second); !errors.Is(err, ErrLimit) {
		t.Fatalf("reserve second error = %v, want ErrLimit", err)
	}
	manager.release(first)
}

func TestPrepareRejectsInvalidSiteKindsAndEmptyPaths(t *testing.T) {
	home := t.TempDir()
	paths, site, build, info := testPHPFixture(t, home)
	deps := testDeps(paths)
	deps.Runtimes = staticRuntimes{build}
	deps.PHP = staticPHP{info: info}
	manager := NewManager(deps)
	t.Cleanup(manager.Close)

	empty := site
	empty.HostPath = ""
	if _, err := manager.prepare(context.Background(), empty); err == nil {
		t.Fatal("prepare accepted an empty project path")
	}

	unknown := site
	unknown.Kind = "python"
	if _, err := manager.prepare(context.Background(), unknown); err == nil {
		t.Fatal("prepare accepted an unknown site kind")
	}
}

func TestPrepareUsesConfiguredShellAndFallback(t *testing.T) {
	home := t.TempDir()
	paths, site, build, info := testPHPFixture(t, home)
	deps := testDeps(paths)
	deps.Runtimes = staticRuntimes{build}
	deps.PHP = staticPHP{info: info}
	deps.Shell = "/bin/sh"
	manager := NewManager(deps)
	t.Cleanup(manager.Close)

	prepared, err := manager.prepare(context.Background(), site)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if prepared.Shell != "/bin/sh" {
		t.Fatalf("shell = %q", prepared.Shell)
	}
}

func TestManagerNeverUsesPrivilegeWrapperAsShell(t *testing.T) {
	home := t.TempDir()
	paths, site, build, info := testPHPFixture(t, home)
	wrapper := filepath.Join(home, "sudo")
	writeExecutable(t, wrapper, "#!/bin/sh\nexit 1\n")

	deps := testDeps(paths)
	deps.Runtimes = staticRuntimes{build}
	deps.PHP = staticPHP{info: info}
	deps.Shell = wrapper
	manager := NewManager(deps)
	t.Cleanup(manager.Close)

	prepared, err := manager.prepare(context.Background(), site)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if prepared.Shell == wrapper || strings.Contains(filepath.Base(prepared.Shell), "sudo") {
		t.Fatalf("shell = %q, want a safe fallback", prepared.Shell)
	}
}

func TestManagerRejectsInvalidTickets(t *testing.T) {
	home := t.TempDir()
	paths, site, build, info := testPHPFixture(t, home)
	deps := testDeps(paths)
	deps.Runtimes = staticRuntimes{build}
	deps.PHP = staticPHP{info: info}
	manager := NewManager(deps)
	t.Cleanup(manager.Close)

	if _, err := manager.IssueTicket(context.Background(), site); err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}
	// Serve has no HTTP request here; only the ticket lookup is exercised by
	// calling the store through the manager seam.
	if _, err := manager.tickets.Consume("not-a-ticket", site.ID, deps.UID); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("Consume error = %v, want ErrTicketInvalid", err)
	}
}

func TestManagerDefaultsClampLimits(t *testing.T) {
	manager := NewManager(Deps{UID: 1000, Logger: testLogger()})
	if manager.maxTotal != DefaultMaxSessionsTotal {
		t.Fatalf("maxTotal = %d", manager.maxTotal)
	}
	if manager.maxPerSite != DefaultMaxSessionsPerSite {
		t.Fatalf("maxPerSite = %d", manager.maxPerSite)
	}
	if manager.ticketTTL > 30*time.Second {
		t.Fatalf("ticket TTL = %v", manager.ticketTTL)
	}
}
