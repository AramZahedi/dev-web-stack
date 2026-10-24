package doctor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/hosts"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

var phpTestNames = []string{"phpblog", "phpshop", "phpapi", "phpwiki", "phpapp"}

// addPHPSite installs a realistic PHP build tree and runtime row, then creates
// a running PHP site pinned to it with an in-range FastCGI port.
func (f *testFakes) addPHPSite(t *testing.T, buildID, version string) model.Site {
	t.Helper()
	f.phpSiteCount++
	index := f.phpSiteCount
	name := phpTestNames[(index-1)%len(phpTestNames)]
	if index > len(phpTestNames) {
		name = fmt.Sprintf("%s%d", name, index)
	}

	buildRoot := filepath.Join(f.dir, "runtimes", buildID)
	extDir := filepath.Join(buildRoot, "lib", "php", "extensions", "no-debug-non-zts-20240924")
	for _, dir := range []string{
		filepath.Join(buildRoot, "bin"),
		filepath.Join(buildRoot, "sbin"),
		filepath.Join(buildRoot, "etc"),
		extDir,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create PHP build directory %s: %v", dir, err)
		}
	}
	for _, file := range []struct {
		path string
		mode os.FileMode
		body string
	}{
		{filepath.Join(buildRoot, "bin", "php"), 0o755, "#!/bin/sh\nexit 0\n"},
		{filepath.Join(buildRoot, "sbin", "php-fpm"), 0o755, "#!/bin/sh\nexit 0\n"},
		{filepath.Join(buildRoot, "etc", "php.ini"), 0o644, "; base build configuration\nmemory_limit = 128M\n"},
		{filepath.Join(extDir, "opcache.so"), 0o644, "so"},
	} {
		if err := os.WriteFile(file.path, []byte(file.body), file.mode); err != nil {
			t.Fatalf("write PHP build file %s: %v", file.path, err)
		}
	}
	build := model.RuntimeBuild{
		ID:          buildID,
		Kind:        "php",
		Version:     version,
		Backend:     model.BackendLocal,
		Path:        buildRoot,
		InstalledAt: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
	}
	if err := f.store.UpsertRuntime(context.Background(), build); err != nil {
		t.Fatalf("upsert PHP runtime: %v", err)
	}

	docroot := filepath.Join(f.dir, name, "public")
	if err := os.MkdirAll(docroot, 0o755); err != nil {
		t.Fatalf("create PHP document root: %v", err)
	}
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	site := model.Site{
		ID:           fmt.Sprintf("php%d", index),
		Name:         name,
		Domain:       name + ".test",
		Backend:      model.BackendLocal,
		BackendPath:  filepath.Join(f.dir, name),
		Kind:         model.SitePHP,
		PHPVersion:   version,
		PHPBuild:     buildID,
		DocumentRoot: docroot,
		FastCGIPort:  43000 + index,
		TLS:          true,
		DesiredState: model.DesiredRunning,
		Status:       model.StatusRunning,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := f.store.CreateSite(context.Background(), site); err != nil {
		t.Fatalf("create PHP site: %v", err)
	}
	f.addHost(t, site.Domain)
	return site
}

// addHost extends the managed hosts block so adding a site does not fail the
// unrelated hosts check.
func (f *testFakes) addHost(t *testing.T, domain string) {
	t.Helper()
	data, err := os.ReadFile(f.hostsPath)
	if err != nil {
		t.Fatalf("read hosts: %v", err)
	}
	domains, found := hosts.Parse(string(data))
	if !found {
		t.Fatal("test hosts file has no managed block")
	}
	rendered, err := hosts.Render(string(data), append(domains, domain))
	if err != nil {
		t.Fatalf("render hosts: %v", err)
	}
	if err := hosts.WriteAtomic(f.hostsPath, []byte(rendered), 0o644); err != nil {
		t.Fatalf("write hosts: %v", err)
	}
}

type fakePHPValidator struct {
	err   error
	errs  map[string]error
	calls []string
}

func (f *fakePHPValidator) ValidatePHP(ctx context.Context, site model.Site) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.calls = append(f.calls, site.Name)
	if f.errs != nil {
		if err, ok := f.errs[site.ID]; ok {
			return err
		}
	}
	return f.err
}

func TestDoctorReportsPHPConfigFailure(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	fakes.addPHPSite(t, "php@8.4.4-r1", "8.4.4")
	fakes.php.err = errors.New("php.ini line 9: syntax error")

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if hasCheck(rep, "php-config", StatusOK) {
		t.Fatalf("php-config should fail: %+v", rep.Checks)
	}
	check, ok := checkByID(rep, "php-config")
	if !ok || check.Status != StatusFail {
		t.Fatalf("php-config = %+v", check)
	}
	if !strings.Contains(check.Detail, "phpblog") || !strings.Contains(check.Detail, "syntax error") {
		t.Fatalf("php-config detail = %q", check.Detail)
	}
	if !strings.Contains(check.Remediation, "devstack site php phpblog 8.4.4") ||
		!strings.Contains(check.Remediation, "devstack site restart phpblog") {
		t.Fatalf("php-config remediation = %q", check.Remediation)
	}
}

func TestDoctorSkipsPHPWhenUnused(t *testing.T) {
	svc, _ := newTestDoctor(t)
	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"php-builds", "php-config", "php-ports", "php-master"} {
		if !hasCheck(rep, id, StatusSkipped) {
			t.Fatalf("%s not skipped: %+v", id, rep.Checks)
		}
		check, _ := checkByID(rep, id)
		if check.Remediation == "" {
			t.Errorf("%s skipped without a remediation", id)
		}
	}
	if !rep.OK {
		t.Fatalf("unused PHP checks must not fail the report: %+v", rep.Checks)
	}
}

func TestDoctorPHPHealthy(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	site := fakes.addPHPSite(t, "php@8.4.4-r1", "8.4.4")
	_, port := listenPHPTestPort(t)
	updatePHPSitePort(t, fakes, site, port)

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"php-builds", "php-config", "php-ports", "php-master"} {
		if !hasCheck(rep, id, StatusOK) {
			t.Fatalf("%s should pass: %+v", id, rep.Checks)
		}
	}
	if !rep.OK {
		t.Fatalf("healthy PHP report: %+v", rep.Checks)
	}
	if len(fakes.php.calls) != 1 || fakes.php.calls[0] != "phpblog" {
		t.Fatalf("ValidatePHP calls = %v", fakes.php.calls)
	}
}

func TestDoctorPHPBuildVerification(t *testing.T) {
	tests := []struct {
		name   string
		remove string
	}{
		{name: "missing php binary", remove: filepath.Join("bin", "php")},
		{name: "missing php-fpm", remove: filepath.Join("sbin", "php-fpm")},
		{name: "empty extension directory", remove: filepath.Join("lib", "php", "extensions", "no-debug-non-zts-20240924", "opcache.so")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, fakes := newTestDoctor(t)
			fakes.addPHPSite(t, "php@8.4.4-r1", "8.4.4")
			path := filepath.Join(fakes.dir, "runtimes", "php@8.4.4-r1", tc.remove)
			if err := os.Remove(path); err != nil {
				t.Fatalf("remove %s: %v", path, err)
			}

			rep, err := svc.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			check, ok := checkByID(rep, "php-builds")
			if !ok || check.Status != StatusFail {
				t.Fatalf("php-builds = %+v", check)
			}
			if !strings.Contains(check.Detail, "php@8.4.4-r1") {
				t.Fatalf("php-builds detail = %q, want the build ID", check.Detail)
			}
			if !strings.Contains(check.Remediation, "devstack runtime install php@8.4.4-r1") {
				t.Fatalf("php-builds remediation = %q", check.Remediation)
			}
		})
	}
}

func TestDoctorPHPBuildMissingForSite(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	site := model.Site{
		ID:           "php1",
		Name:         "phpblog",
		Domain:       "phpblog.test",
		Kind:         model.SitePHP,
		PHPVersion:   "8.4",
		PHPBuild:     "php@8.4.4-r1",
		FastCGIPort:  43001,
		DesiredState: model.DesiredRunning,
		Status:       model.StatusRunning,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := fakes.store.CreateSite(context.Background(), site); err != nil {
		t.Fatal(err)
	}
	fakes.addHost(t, site.Domain)

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check, ok := checkByID(rep, "php-builds")
	if !ok || check.Status != StatusFail {
		t.Fatalf("php-builds = %+v", check)
	}
	if !strings.Contains(check.Detail, "no PHP build") {
		t.Fatalf("php-builds detail = %q", check.Detail)
	}
	if !strings.Contains(check.Remediation, "devstack runtime install") ||
		!strings.Contains(check.Remediation, "devstack site php phpblog 8.4") {
		t.Fatalf("php-builds remediation = %q", check.Remediation)
	}
}

func TestDoctorPHPConfigReportsFirstThreeFailures(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	sites := make([]model.Site, 0, 5)
	for i := 0; i < 5; i++ {
		sites = append(sites, fakes.addPHPSite(t, "php@8.4.4-r1", "8.4.4"))
	}
	fakes.php.errs = make(map[string]error, len(sites))
	for _, site := range sites {
		fakes.php.errs[site.ID] = fmt.Errorf("%s: php.ini line 9: syntax error", site.Name)
	}

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check, ok := checkByID(rep, "php-config")
	if !ok || check.Status != StatusFail {
		t.Fatalf("php-config = %+v", check)
	}
	// ListSites orders by name: phpapi, phpapp, phpblog, phpshop, phpwiki.
	for _, want := range []string{"phpapi", "phpapp", "phpblog"} {
		if !strings.Contains(check.Detail, want) {
			t.Errorf("php-config detail is missing %q: %q", want, check.Detail)
		}
	}
	for _, hidden := range []string{"phpshop", "phpwiki"} {
		if strings.Contains(check.Detail, hidden) {
			t.Errorf("php-config detail includes the fourth or later failure %q: %q", hidden, check.Detail)
		}
	}
	if !strings.Contains(check.Detail, "2 more") {
		t.Fatalf("php-config detail does not count the remaining failures: %q", check.Detail)
	}
	if len(fakes.php.calls) != len(sites) {
		t.Fatalf("ValidatePHP calls = %d, want %d", len(fakes.php.calls), len(sites))
	}
}

func TestDoctorPHPConfigNilValidator(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	fakes.addPHPSite(t, "php@8.4.4-r1", "8.4.4")
	svc.deps.PHP = nil

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check, ok := checkByID(rep, "php-config")
	if !ok || check.Status != StatusFail {
		t.Fatalf("php-config = %+v", check)
	}
	if !strings.Contains(check.Detail, "validator") {
		t.Fatalf("php-config detail = %q", check.Detail)
	}
	if !strings.Contains(check.Remediation, "devstack init") {
		t.Fatalf("php-config remediation = %q", check.Remediation)
	}
}

func TestDoctorPHPPorts(t *testing.T) {
	t.Run("duplicate port", func(t *testing.T) {
		svc, fakes := newTestDoctor(t)
		first := fakes.addPHPSite(t, "php@8.4.4-r1", "8.4.4")
		second := fakes.addPHPSite(t, "php@8.4.4-r1", "8.4.4")
		updatePHPSitePort(t, fakes, second, first.FastCGIPort)

		rep, err := svc.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		check, ok := checkByID(rep, "php-ports")
		if !ok || check.Status != StatusFail {
			t.Fatalf("php-ports = %+v", check)
		}
		for _, want := range []string{first.Name, second.Name, "duplicate"} {
			if !strings.Contains(check.Detail, want) {
				t.Errorf("php-ports detail is missing %q: %q", want, check.Detail)
			}
		}
		if !strings.Contains(check.Remediation, "devstack site restart") {
			t.Fatalf("php-ports remediation = %q", check.Remediation)
		}
	})

	t.Run("out of range port", func(t *testing.T) {
		svc, fakes := newTestDoctor(t)
		site := fakes.addPHPSite(t, "php@8.4.4-r1", "8.4.4")
		updatePHPSitePort(t, fakes, site, 41001)

		rep, err := svc.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		check, ok := checkByID(rep, "php-ports")
		if !ok || check.Status != StatusFail {
			t.Fatalf("php-ports = %+v", check)
		}
		if !strings.Contains(check.Detail, "43000-43999") || !strings.Contains(check.Detail, "41001") {
			t.Fatalf("php-ports detail = %q", check.Detail)
		}
	})
}

func TestDoctorPHPMasterDialFailure(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	site := fakes.addPHPSite(t, "php@8.4.4-r1", "8.4.4")
	port := closedPHPTestPort(t)
	updatePHPSitePort(t, fakes, site, port)

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check, ok := checkByID(rep, "php-master")
	if !ok || check.Status != StatusFail {
		t.Fatalf("php-master = %+v", check)
	}
	if !strings.Contains(check.Detail, site.Name) || !strings.Contains(check.Detail, fmt.Sprint(port)) {
		t.Fatalf("php-master detail = %q", check.Detail)
	}
	if !strings.Contains(check.Remediation, "devstack site restart "+site.Name) {
		t.Fatalf("php-master remediation = %q", check.Remediation)
	}
}

func TestDoctorPHPBuildCheckedWithoutSites(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	site := fakes.addPHPSite(t, "php@8.4.4-r1", "8.4.4")
	if err := fakes.store.DeleteSite(context.Background(), site.ID); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(rep, "php-builds", StatusOK) {
		t.Fatalf("php-builds should still be checked without sites: %+v", rep.Checks)
	}
	for _, id := range []string{"php-config", "php-ports", "php-master"} {
		if !hasCheck(rep, id, StatusSkipped) {
			t.Fatalf("%s should be skipped without PHP sites: %+v", id, rep.Checks)
		}
	}
}

func TestDoctorPHPBuildDirectExtensionModules(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	fakes.addPHPSite(t, "php@8.4.4-r1", "8.4.4")
	extDir := filepath.Join(fakes.dir, "runtimes", "php@8.4.4-r1", "lib", "php", "extensions")
	module := filepath.Join(extDir, "no-debug-non-zts-20240924", "opcache.so")
	if err := os.Rename(module, filepath.Join(extDir, "opcache.so")); err != nil {
		t.Fatalf("move extension module: %v", err)
	}

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(rep, "php-builds", StatusOK) {
		t.Fatalf("php-builds should accept a direct extension module: %+v", rep.Checks)
	}
}

func TestDoctorPHPSkipsPortsAndMasterForStoppedSites(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	site := fakes.addPHPSite(t, "php@8.4.4-r1", "8.4.4")
	site.DesiredState = model.DesiredStopped
	site.UpdatedAt = site.UpdatedAt.Add(time.Minute)
	if err := fakes.store.UpdateSite(context.Background(), site); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if hasCheck(rep, "php-config", StatusSkipped) {
		t.Fatalf("php-config must still validate stopped PHP sites: %+v", rep.Checks)
	}
	for _, id := range []string{"php-ports", "php-master"} {
		if !hasCheck(rep, id, StatusSkipped) {
			t.Fatalf("%s should be skipped when every PHP site is stopped: %+v", id, rep.Checks)
		}
	}
}

func TestDoctorPHPSkippedWithoutStore(t *testing.T) {
	svc, _ := newTestDoctor(t)
	svc.deps.Store = nil

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"php-builds", "php-config", "php-ports", "php-master"} {
		if !hasCheck(rep, id, StatusSkipped) {
			t.Fatalf("%s should be skipped without a store: %+v", id, rep.Checks)
		}
		check, _ := checkByID(rep, id)
		if check.Remediation == "" {
			t.Errorf("%s skipped without a remediation", id)
		}
	}
}

// listenPHPTestPort binds a free loopback port in the PHP FastCGI range and
// keeps the listener open until the test ends.
func listenPHPTestPort(t *testing.T) (net.Listener, int) {
	t.Helper()
	for port := 43000; port <= 43999; port++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		t.Cleanup(func() { _ = ln.Close() })
		return ln, port
	}
	t.Skip("no free loopback port in 43000-43999")
	return nil, 0
}

// closedPHPTestPort returns a loopback port in the PHP FastCGI range with no
// listener.
func closedPHPTestPort(t *testing.T) int {
	t.Helper()
	for port := 43000; port <= 43999; port++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		_ = ln.Close()
		return port
	}
	t.Skip("no free loopback port in 43000-43999")
	return 0
}

func updatePHPSitePort(t *testing.T, fakes *testFakes, site model.Site, port int) {
	t.Helper()
	site.FastCGIPort = port
	site.UpdatedAt = site.UpdatedAt.Add(time.Minute)
	if err := fakes.store.UpdateSite(context.Background(), site); err != nil {
		t.Fatalf("update PHP site port: %v", err)
	}
}
