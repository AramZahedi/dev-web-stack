package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/hosts"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
	"github.com/AramZahedi/dev-web-stack/internal/trust"
)

// testFakes groups the simulated edges of the doctor with the real store and a
// real temporary hosts file.
type testFakes struct {
	helper    *fakeHelper
	trust     *fakeTrust
	ports     *fakePorts
	runtimes  *fakeRuntimes
	disk      *fakeDisk
	vhosts    *fakeVhosts
	php       *fakePHPValidator
	store     *store.Store
	hostsPath string
	home      string
	dir       string

	phpSiteCount int
}

// newTestDoctor builds a Service against a temporary SQLite database and hosts
// file. The default world is healthy: one site, matching hosts, all ports free,
// intact runtimes, and plenty of disk.
func newTestDoctor(t *testing.T) (*Service, *testFakes) {
	t.Helper()

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	site := model.Site{
		ID:        "site",
		Name:      "blog",
		Domain:    "blog.test",
		WWW:       true,
		Backend:   model.BackendLocal,
		Kind:      model.SiteStatic,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := st.CreateSite(context.Background(), site); err != nil {
		t.Fatalf("create site: %v", err)
	}

	hostsPath := filepath.Join(dir, "hosts")
	initial := []string{"blog.test", "www.blog.test", "devstack.test"}
	rendered, err := hosts.Render("", initial)
	if err != nil {
		t.Fatalf("render hosts: %v", err)
	}
	if err := hosts.WriteAtomic(hostsPath, []byte(rendered), 0o644); err != nil {
		t.Fatalf("write hosts: %v", err)
	}

	ft := &fakeTrust{installed: true, targets: healthyTargets()}
	fakes := &testFakes{
		trust: ft,
		ports: &fakePorts{errs: map[string]error{
			":80":   syscall.EADDRINUSE,
			":443":  syscall.EADDRINUSE,
			":7070": syscall.EADDRINUSE,
			":8080": syscall.EADDRINUSE,
			":8443": syscall.EADDRINUSE,
		}},
		runtimes:  &fakeRuntimes{},
		disk:      &fakeDisk{free: 20 << 30},
		vhosts:    &fakeVhosts{status: helperproto.VhostStatusResponse{Mode: "builtin", ConfigOK: true}},
		php:       &fakePHPValidator{},
		store:     st,
		hostsPath: hostsPath,
		home:      filepath.Join(dir, "user"),
		dir:       dir,
	}
	fakes.helper = &fakeHelper{
		status: helperproto.StatusResponse{
			Version:       helperproto.Version,
			CAFingerprint: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			HostsSynced:   true,
		},
		hostsPath: hostsPath,
		trust:     ft,
	}

	cfg := config.Default()
	cfg.Home = dir

	svc := NewService(Deps{
		Config:    cfg,
		Home:      fakes.home,
		Store:     st,
		Helper:    fakes.helper,
		Trust:     fakes.trust,
		Ports:     fakes.ports,
		Runtimes:  fakes.runtimes,
		Disk:      fakes.disk,
		Vhosts:    fakes.vhosts,
		PHP:       fakes.php,
		HostsPath: hostsPath,
	})
	return svc, fakes
}

func healthyTargets() []trust.Target {
	return []trust.Target{
		{Name: "OS", OK: true, Detail: "installed"},
		{Name: "Chrome", OK: true, Detail: "installed"},
		{Name: "Firefox", OK: true, Detail: "installed"},
	}
}

func TestReportFailsWhenHelperUnavailable(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	fakes.helper.err = errors.New("down")

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK {
		t.Fatal("want not OK")
	}
	if !hasCheck(rep, "helper", StatusFail) {
		t.Fatalf("%+v", rep.Checks)
	}
	if !hasCheck(rep, "ca", StatusSkipped) {
		t.Fatalf("ca should be skipped when the helper is down: %+v", rep.Checks)
	}
}

func TestCASucceedsWithFingerprint(t *testing.T) {
	svc, _ := newTestDoctor(t)
	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check, ok := checkByID(rep, "ca")
	if !ok {
		t.Fatalf("missing ca check: %+v", rep.Checks)
	}
	if check.Status != StatusOK {
		t.Fatalf("ca = %+v", check)
	}
	if !strings.Contains(check.Detail, "0123456789abcdef") {
		t.Fatalf("ca detail does not include the fingerprint: %q", check.Detail)
	}
}

func TestCAEmptyFingerprintFails(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	fakes.helper.status.CAFingerprint = ""

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check, ok := checkByID(rep, "ca")
	if !ok {
		t.Fatalf("missing ca check: %+v", rep.Checks)
	}
	if check.Status != StatusFail {
		t.Fatalf("ca = %+v", check)
	}
	if !strings.Contains(check.Remediation, "devstack init") {
		t.Fatalf("ca remediation = %q", check.Remediation)
	}
	if rep.OK {
		t.Fatal("missing CA must fail the report")
	}
}

func TestRepairResyncsHostsAndTrust(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	fakes.helper.hostsMissing = true
	fakes.trust.installed = false

	rep, err := svc.Repair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !fakes.helper.synced || !fakes.trust.installed {
		t.Fatal("repair did not run")
	}
	if !rep.OK {
		t.Fatalf("repair report: %+v", rep.Checks)
	}
}

func TestHostsMismatchFails(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	rendered, err := hosts.Render("", []string{"devstack.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := hosts.WriteAtomic(fakes.hostsPath, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK {
		t.Fatal("want not OK for missing managed domains")
	}
	if !hasCheck(rep, "hosts", StatusFail) {
		t.Fatalf("%+v", rep.Checks)
	}
}

func TestHostsMatchSucceeds(t *testing.T) {
	svc, _ := newTestDoctor(t)
	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(rep, "hosts", StatusOK) {
		t.Fatalf("%+v", rep.Checks)
	}
}

func TestHostsMalformedBlockFails(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	content := hosts.BeginMarker + "\n127.0.0.1 blog.test\n" +
		hosts.BeginMarker + "\n127.0.0.1 www.blog.test\n" +
		hosts.EndMarker + "\n"
	if err := os.WriteFile(fakes.hostsPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(rep, "hosts", StatusFail) {
		t.Fatalf("%+v", rep.Checks)
	}
}

func TestHelperVersionMismatchWarns(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	fakes.helper.status.Version = "2"

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(rep, "helper", StatusWarn) {
		t.Fatalf("%+v", rep.Checks)
	}
	if !rep.OK {
		t.Fatal("a helper version warning must not fail the report")
	}
}

func TestTrustTargetFailureFails(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	fakes.trust.targets = []trust.Target{
		{Name: "OS", OK: true, Detail: "installed"},
		{Name: "Chrome", OK: false, Detail: "certificate missing"},
	}

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK {
		t.Fatal("want not OK")
	}
	if !hasCheck(rep, "trust", StatusFail) {
		t.Fatalf("%+v", rep.Checks)
	}
}

func TestTrustWithoutBrowserTargetsWarns(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	fakes.trust.targets = []trust.Target{{Name: "OS", OK: true, Detail: "installed"}}

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(rep, "trust", StatusWarn) {
		t.Fatalf("%+v", rep.Checks)
	}
	if !rep.OK {
		t.Fatal("a warning must not make the report fail")
	}
}

func TestTrustMissingBrowsersWarnsDespiteOKDetails(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	fakes.trust.targets = []trust.Target{
		{Name: "OS", OK: true, Detail: "installed"},
		{Name: "Chrome", OK: true, Detail: "not installed"},
		{Name: "Firefox", OK: true, Detail: "not installed"},
	}

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check, ok := checkByID(rep, "trust")
	if !ok {
		t.Fatalf("missing trust check: %+v", rep.Checks)
	}
	if check.Status != StatusWarn {
		t.Fatalf("trust = %+v", check)
	}
	if strings.Contains(check.Detail, "trusted by 3 targets") {
		t.Fatalf("missing browsers were counted as trusted: %q", check.Detail)
	}
	if !strings.Contains(check.Detail, "no installed browser") {
		t.Fatalf("trust detail = %q", check.Detail)
	}
	if !rep.OK {
		t.Fatal("missing browsers must not fail the report")
	}
}

func TestTrustInstalledBrowserSucceeds(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	fakes.trust.targets = []trust.Target{
		{Name: "OS", OK: true, Detail: "installed"},
		{Name: "Chrome", OK: true, Detail: "not installed"},
		{Name: "Firefox (default)", OK: true, Detail: "installed"},
	}

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(rep, "trust", StatusOK) {
		t.Fatalf("%+v", rep.Checks)
	}
}

func TestPortChecks(t *testing.T) {
	tests := []struct {
		name           string
		mutate         func(*fakePorts)
		want           Status
		detailContains string
	}{
		{
			name:           "expected services own every port",
			want:           StatusOK,
			detailContains: "expected services",
		},
		{
			name: "free helper port warns",
			mutate: func(ports *fakePorts) {
				delete(ports.errs, ":80")
			},
			want:           StatusWarn,
			detailContains: "port 80 is free",
		},
		{
			name: "free dashboard port warns",
			mutate: func(ports *fakePorts) {
				delete(ports.errs, ":7070")
			},
			want:           StatusWarn,
			detailContains: "port 7070 is free",
		},
		{
			name: "probe permission is a warning",
			mutate: func(ports *fakePorts) {
				ports.errs[":443"] = syscall.EACCES
			},
			want:           StatusWarn,
			detailContains: "cannot probe port 443",
		},
		{
			name: "unexpected probe error is a warning",
			mutate: func(ports *fakePorts) {
				ports.errs[":8080"] = errors.New("network unreachable")
			},
			want:           StatusWarn,
			detailContains: "could not probe port 8080",
		},
		{
			name: "windows socket in use probe error is ok",
			mutate: func(ports *fakePorts) {
				ports.errs[":80"] = errors.New("bind: Only one usage of each socket address (protocol/network address/port) is normally permitted.")
				ports.errs[":443"] = errors.New("bind: Only one usage of each socket address (protocol/network address/port) is normally permitted.")
			},
			want:           StatusOK,
			detailContains: "expected services",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, fakes := newTestDoctor(t)
			if tt.mutate != nil {
				tt.mutate(fakes.ports)
			}

			rep, err := svc.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			check, ok := checkByID(rep, "ports")
			if !ok {
				t.Fatalf("missing ports check: %+v", rep.Checks)
			}
			if check.Status != tt.want {
				t.Fatalf("ports = %+v", check)
			}
			if !strings.Contains(check.Detail, tt.detailContains) {
				t.Fatalf("ports detail = %q, want it to contain %q", check.Detail, tt.detailContains)
			}
			if !rep.OK {
				t.Fatalf("port diagnostics must never fail the report: %+v", rep.Checks)
			}
			if strings.Contains(strings.ToLower(check.Remediation), "stop") {
				t.Fatalf("remediation must not tell users to stop services: %q", check.Remediation)
			}
		})
	}
}

func TestDiskBelowThresholdFails(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	fakes.disk.free = 512 << 20

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(rep, "disk", StatusFail) {
		t.Fatalf("%+v", rep.Checks)
	}
}

func TestDiskAtThresholdFails(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	fakes.disk.free = 1 << 30

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(rep, "disk", StatusFail) {
		t.Fatalf("exactly 1 GiB must fail when more than 1 GiB is required: %+v", rep.Checks)
	}
}

func TestRuntimeCorruptionFails(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	fakes.runtimes.err = errors.New("checksum mismatch")

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(rep, "runtimes", StatusFail) {
		t.Fatalf("%+v", rep.Checks)
	}
}

func TestWSLSkippedKeepsReportOK(t *testing.T) {
	svc, _ := newTestDoctor(t)
	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(rep, "wsl", StatusSkipped) {
		t.Fatalf("%+v", rep.Checks)
	}
	if !rep.OK {
		t.Fatalf("skipped WSL must not fail the report: %+v", rep.Checks)
	}
}

func TestRepairIsIdempotentAndSyncsFullDomainSet(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	fakes.trust.installed = false
	if err := os.Remove(fakes.hostsPath); err != nil {
		t.Fatal(err)
	}

	first, err := svc.Repair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !first.OK {
		t.Fatalf("first repair: %+v", first.Checks)
	}
	firstHosts, err := os.ReadFile(fakes.hostsPath)
	if err != nil {
		t.Fatal(err)
	}

	wantDomains := []string{"blog.test", "devstack.test", "www.blog.test"}
	gotDomains := append([]string(nil), fakes.helper.syncedDomains...)
	sort.Strings(gotDomains)
	if !reflect.DeepEqual(gotDomains, wantDomains) {
		t.Fatalf("synced domains = %v, want %v", gotDomains, wantDomains)
	}

	second, err := svc.Repair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !second.OK {
		t.Fatalf("second repair: %+v", second.Checks)
	}
	secondHosts, err := os.ReadFile(fakes.hostsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstHosts) != string(secondHosts) {
		t.Fatalf("repair is not idempotent:\nfirst:\n%s\nsecond:\n%s", firstHosts, secondHosts)
	}
	if fakes.helper.syncCalls != 2 {
		t.Fatalf("SyncHosts calls = %d, want 2", fakes.helper.syncCalls)
	}
	if !fakes.runtimes.called {
		t.Fatal("repair did not re-verify runtimes")
	}
}

func TestRepairWithMissingDependenciesDoesNotPanic(t *testing.T) {
	_, fakes := newTestDoctor(t)
	svc := NewService(Deps{
		Config:    config.Default(),
		Home:      fakes.home,
		Store:     fakes.store,
		HostsPath: fakes.hostsPath,
	})

	rep, err := svc.Repair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"trust", "ports", "runtimes", "disk"} {
		if !hasCheck(rep, id, StatusSkipped) {
			t.Fatalf("%s should be skipped: %+v", id, rep.Checks)
		}
	}
	if !hasCheck(rep, "hosts", StatusOK) {
		t.Fatalf("hosts check should still pass: %+v", rep.Checks)
	}
}

func TestRepairReportsTrustInstallFailure(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	fakes.trust.installed = false
	fakes.helper.installErr = errors.New("helper denied trust install")

	rep, err := svc.Repair(context.Background())
	if err != nil {
		t.Fatalf("repair should report, not return, installation failures: %v", err)
	}
	if rep.OK {
		t.Fatal("want not OK")
	}
	if !hasCheck(rep, "trust", StatusFail) {
		t.Fatalf("%+v", rep.Checks)
	}
}

func TestRepairSkipsMalformedHostsBlock(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	content := hosts.BeginMarker + "\n127.0.0.1 blog.test\n" +
		hosts.BeginMarker + "\n127.0.0.1 www.blog.test\n" +
		hosts.EndMarker + "\n"
	if err := os.WriteFile(fakes.hostsPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Repair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fakes.helper.synced {
		t.Fatal("repair must not attempt to rewrite a malformed hosts block")
	}
	if rep.OK {
		t.Fatal("malformed hosts must fail the report")
	}
	check, ok := checkByID(rep, "hosts")
	if !ok {
		t.Fatalf("missing hosts check: %+v", rep.Checks)
	}
	if check.Status != StatusFail {
		t.Fatalf("hosts = %+v", check)
	}
	if !strings.Contains(check.Remediation, "Remove the duplicated or unbalanced") {
		t.Fatalf("hosts remediation = %q", check.Remediation)
	}
}

func TestRepairRestoresMissingHostsBlock(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	if err := os.WriteFile(fakes.hostsPath, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Repair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !fakes.helper.synced {
		t.Fatal("repair did not resync a missing managed block")
	}
	if !hasCheck(rep, "hosts", StatusOK) {
		t.Fatalf("hosts should be repaired: %+v", rep.Checks)
	}
	if !rep.OK {
		t.Fatalf("repair report: %+v", rep.Checks)
	}
}

func TestEveryNonOKCheckHasRemediationAndNoSudo(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	fakes.helper.err = errors.New("down")
	writeMalformedHosts(t, fakes)
	fakes.trust.targets = []trust.Target{
		{Name: "OS", OK: true, Detail: "installed"},
		{Name: "Chrome", OK: false, Detail: "certificate missing"},
	}
	delete(fakes.ports.errs, ":80")
	fakes.runtimes.err = errors.New("checksum mismatch")
	fakes.disk.free = 1 << 30

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK {
		t.Fatal("want a failing report")
	}
	for _, check := range rep.Checks {
		if check.Status != StatusOK && check.Remediation == "" {
			t.Errorf("%s (%s) has no remediation", check.ID, check.Status)
		}
		if strings.Contains(strings.ToLower(check.Remediation), "sudo ") {
			t.Errorf("%s remediation uses sudo: %q", check.ID, check.Remediation)
		}
	}
	for _, id := range []string{"helper", "ca", "hosts", "trust", "ports", "runtimes", "disk", "wsl",
		"serving-mode", "vhost-server", "vhost-include", "vhost-config", "vhost-freshness", "vhost-tls",
		"php-builds", "php-config", "php-ports", "php-master"} {
		if _, ok := checkByID(rep, id); !ok {
			t.Errorf("missing %s check: %+v", id, rep.Checks)
		}
	}
}

func writeMalformedHosts(t *testing.T, fakes *testFakes) {
	t.Helper()
	content := hosts.BeginMarker + "\n127.0.0.1 blog.test\n" +
		hosts.BeginMarker + "\n127.0.0.1 www.blog.test\n" +
		hosts.EndMarker + "\n"
	if err := os.WriteFile(fakes.hostsPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasCheck(rep Report, id string, status Status) bool {
	check, ok := checkByID(rep, id)
	return ok && check.Status == status
}

func checkByID(rep Report, id string) (Check, bool) {
	for _, check := range rep.Checks {
		if check.ID == id {
			return check, true
		}
	}
	return Check{}, false
}

type fakeHelper struct {
	status        helperproto.StatusResponse
	err           error
	hostsPath     string
	trust         *fakeTrust
	hostsMissing  bool
	synced        bool
	syncCalls     int
	syncedDomains []string
	installErr    error
}

func (f *fakeHelper) Status(ctx context.Context) (helperproto.StatusResponse, error) {
	if f.err != nil {
		return helperproto.StatusResponse{}, f.err
	}
	status := f.status
	if f.hostsMissing {
		status.HostsSynced = false
	}
	return status, nil
}

func (f *fakeHelper) SyncHosts(ctx context.Context, domains []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.syncCalls++
	f.synced = true
	f.syncedDomains = append([]string(nil), domains...)
	f.hostsMissing = false

	existing, err := os.ReadFile(f.hostsPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	rendered, err := hosts.Render(string(existing), domains)
	if err != nil {
		return err
	}
	return hosts.WriteAtomic(f.hostsPath, []byte(rendered), 0o644)
}

func (f *fakeHelper) InstallTrust(ctx context.Context, userHome string) ([]trust.Target, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.installErr != nil {
		return nil, f.installErr
	}
	if f.trust != nil {
		f.trust.installed = true
		if len(f.trust.targets) == 0 {
			f.trust.targets = healthyTargets()
		}
	}
	return healthyTargets(), nil
}

type fakeTrust struct {
	targets   []trust.Target
	err       error
	installed bool
}

func (f *fakeTrust) TrustStatus(ctx context.Context, userHome string) ([]trust.Target, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.err != nil {
		return nil, f.err
	}
	if !f.installed {
		return []trust.Target{{Name: "OS", OK: false, Detail: "not installed"}}, nil
	}
	return append([]trust.Target(nil), f.targets...), nil
}

type fakePorts struct {
	errs map[string]error
}

func (f *fakePorts) Listenable(addr string) error {
	return f.errs[addr]
}

type fakeRuntimes struct {
	err    error
	called bool
}

func (f *fakeRuntimes) VerifyAll(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.called = true
	return f.err
}

type fakeDisk struct {
	free  uint64
	err   error
	path  string
	calls int
}

func (f *fakeDisk) FreeBytes(path string) (uint64, error) {
	f.calls++
	f.path = path
	return f.free, f.err
}

func TestDoctorVhostChecksSkippedInBuiltin(t *testing.T) {
	svc, _ := newTestDoctor(t)
	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(rep, "serving-mode", StatusOK) {
		t.Fatalf("serving-mode should pass in builtin mode: %+v", rep.Checks)
	}
	for _, id := range []string{"vhost-server", "vhost-include", "vhost-config", "vhost-freshness", "vhost-tls"} {
		if !hasCheck(rep, id, StatusSkipped) {
			t.Fatalf("%s should be skipped in builtin mode: %+v", id, rep.Checks)
		}
	}
	if !rep.OK {
		t.Fatalf("builtin vhost checks must not fail the report: %+v", rep.Checks)
	}
}

func TestDoctorHealthyExternalVhosts(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	enableExternalVhosts(t, svc, fakes, config.ServingModeNginx)

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"serving-mode", "vhost-server", "vhost-include", "vhost-config", "vhost-freshness", "vhost-tls"} {
		if !hasCheck(rep, id, StatusOK) {
			t.Fatalf("%s should pass: %+v", id, rep.Checks)
		}
	}
	if !rep.OK {
		t.Fatalf("healthy external report: %+v", rep.Checks)
	}
}

func TestDoctorExternalServiceInactiveFails(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	enableExternalVhosts(t, svc, fakes, config.ServingModeNginx)
	fakes.vhosts.status.ServiceActive = false

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check, ok := checkByID(rep, "vhost-server")
	if !ok || check.Status != StatusFail {
		t.Fatalf("vhost-server = %+v", check)
	}
	if !strings.Contains(check.Detail, "service is not active") {
		t.Fatalf("vhost-server detail = %q", check.Detail)
	}
	if !strings.Contains(check.Remediation, "systemctl start nginx") {
		t.Fatalf("vhost-server remediation = %q", check.Remediation)
	}
	if rep.OK {
		t.Fatal("an inactive external server must fail the report")
	}
}

func TestDoctorDefaultServerConflictWarns(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	enableExternalVhosts(t, svc, fakes, config.ServingModeNginx)
	fakes.vhosts.status.DefaultServerConflict = true

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check, ok := checkByID(rep, "vhost-default-server")
	if !ok || check.Status != StatusWarn {
		t.Fatalf("vhost-default-server = %+v", check)
	}
	if !strings.Contains(check.Detail, "default_server") {
		t.Fatalf("vhost-default-server detail = %q", check.Detail)
	}
	if !strings.Contains(check.Remediation, "vhost sync") {
		t.Fatalf("vhost-default-server remediation = %q", check.Remediation)
	}
	if !rep.OK {
		t.Fatal("a default-server conflict must warn, not fail")
	}
}

func TestDoctorExternalPorts(t *testing.T) {
	t.Run("external mode expects proxy ports free", func(t *testing.T) {
		svc, fakes := newTestDoctor(t)
		enableExternalVhosts(t, svc, fakes, config.ServingModeNginx)

		rep, err := svc.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		check, ok := checkByID(rep, "ports")
		if !ok || check.Status != StatusOK {
			t.Fatalf("ports = %+v", check)
		}
		if !strings.Contains(check.Detail, "nginx server") || !strings.Contains(check.Detail, "8080, 8443 are free") {
			t.Fatalf("ports detail = %q", check.Detail)
		}
	})

	t.Run("occupied proxy port warns in external mode", func(t *testing.T) {
		svc, fakes := newTestDoctor(t)
		enableExternalVhosts(t, svc, fakes, config.ServingModeNginx)
		fakes.ports.errs[":8080"] = syscall.EADDRINUSE

		rep, err := svc.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		check, ok := checkByID(rep, "ports")
		if !ok || check.Status != StatusWarn {
			t.Fatalf("ports = %+v", check)
		}
		if !strings.Contains(check.Detail, "port 8080 is in use") {
			t.Fatalf("ports detail = %q", check.Detail)
		}
		if !rep.OK {
			t.Fatal("port diagnostics must never fail the report")
		}
	})
}

func TestDoctorVhostTLSUsesReportedDir(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	enableExternalVhosts(t, svc, fakes, config.ServingModeNginx)
	fakes.vhosts.status.TLSDir = "/srv/devstack/tls"
	setTLSFileMode(&fakes.vhosts.status, "blog.key", 0o644)

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check, ok := checkByID(rep, "vhost-tls")
	if !ok || check.Status != StatusFail {
		t.Fatalf("vhost-tls = %+v", check)
	}
	if !strings.Contains(check.Detail, "/srv/devstack/tls/blog.key") {
		t.Fatalf("vhost-tls detail = %q, want the reported TLS dir", check.Detail)
	}
	if !strings.Contains(check.Remediation, "/srv/devstack/tls") {
		t.Fatalf("vhost-tls remediation = %q, want the reported TLS dir", check.Remediation)
	}
}

func TestDoctorVhostModeMismatchFails(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	enableExternalVhosts(t, svc, fakes, config.ServingModeNginx)
	fakes.vhosts.status.Mode = config.ServingModeApache

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check, ok := checkByID(rep, "serving-mode")
	if !ok || check.Status != StatusFail {
		t.Fatalf("serving-mode = %+v (found %v)", check, ok)
	}
	if !strings.Contains(check.Remediation, "devstack vhost use nginx") || !strings.Contains(check.Remediation, "doctor") {
		t.Fatalf("serving-mode remediation = %q", check.Remediation)
	}
	if rep.OK {
		t.Fatal("a mode mismatch must fail the report")
	}
}

func TestDoctorVhostMissingVersionAndIncludeFail(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	enableExternalVhosts(t, svc, fakes, config.ServingModeNginx)
	fakes.vhosts.status.ServerVersion = ""
	fakes.vhosts.status.IncludePresent = false
	fakes.vhosts.status.CurrentRelease = ""

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(rep, "vhost-server", StatusFail) {
		t.Fatalf("vhost-server should fail without a version: %+v", rep.Checks)
	}
	include, ok := checkByID(rep, "vhost-include")
	if !ok || include.Status != StatusFail {
		t.Fatalf("vhost-include = %+v", include)
	}
	if !strings.Contains(include.Detail, fakes.vhosts.status.BridgePath) {
		t.Fatalf("vhost-include detail = %q, want the bridge path", include.Detail)
	}
}

func TestDoctorReportsVhostConfigFailure(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	enableExternalVhosts(t, svc, fakes, config.ServingModeNginx)
	fakes.vhosts.status.ConfigOK = false
	fakes.vhosts.status.LastError = "nginx: [emerg] unknown directive"

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if hasCheck(rep, "vhost-config", StatusOK) {
		t.Fatalf("%+v", rep.Checks)
	}
	check, ok := checkByID(rep, "vhost-config")
	if !ok || check.Status != StatusFail {
		t.Fatalf("vhost-config = %+v", check)
	}
	if !strings.Contains(check.Detail, "unknown directive") {
		t.Fatalf("vhost-config detail = %q, want the last error", check.Detail)
	}
	if !strings.Contains(check.Remediation, "vhost sync") {
		t.Fatalf("vhost-config remediation = %q", check.Remediation)
	}
}

func TestDoctorVhostStaleReleaseFails(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	enableExternalVhosts(t, svc, fakes, config.ServingModeNginx)
	fakes.vhosts.status.LastSync = time.Date(2026, 10, 23, 11, 0, 0, 0, time.UTC)

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check, ok := checkByID(rep, "vhost-freshness")
	if !ok || check.Status != StatusFail {
		t.Fatalf("vhost-freshness = %+v", check)
	}
	if !strings.Contains(check.Detail, "vhost release is older than site state") {
		t.Fatalf("vhost-freshness detail = %q", check.Detail)
	}
}

func TestDoctorVhostFreshnessWithoutStoreIsSkipped(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	enableExternalVhosts(t, svc, fakes, config.ServingModeNginx)
	svc.deps.Store = nil

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(rep, "vhost-freshness", StatusSkipped) {
		t.Fatalf("vhost-freshness should be skipped without a store: %+v", rep.Checks)
	}
}

func TestDoctorVhostStatusNotConfigured(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	enableExternalVhosts(t, svc, fakes, config.ServingModeNginx)
	svc.deps.Vhosts = nil

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check, ok := checkByID(rep, "serving-mode")
	if !ok || check.Status != StatusFail {
		t.Fatalf("serving-mode = %+v", check)
	}
	if !strings.Contains(check.Detail, "vhost status is not configured") {
		t.Fatalf("serving-mode detail = %q", check.Detail)
	}
	for _, id := range []string{"vhost-server", "vhost-include", "vhost-config", "vhost-freshness", "vhost-tls"} {
		if !hasCheck(rep, id, StatusSkipped) {
			t.Fatalf("%s should be skipped: %+v", id, rep.Checks)
		}
	}
}

func TestDoctorVhostStatusReadFailure(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	enableExternalVhosts(t, svc, fakes, config.ServingModeNginx)
	fakes.vhosts.err = errors.New("helper is down")

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check, ok := checkByID(rep, "serving-mode")
	if !ok || check.Status != StatusFail {
		t.Fatalf("serving-mode = %+v", check)
	}
	if !strings.Contains(check.Detail, "helper is down") {
		t.Fatalf("serving-mode detail = %q", check.Detail)
	}
	if !hasCheck(rep, "vhost-tls", StatusSkipped) {
		t.Fatalf("vhost-tls should be skipped: %+v", rep.Checks)
	}
}

func TestDoctorUsesLiveServingMode(t *testing.T) {
	t.Run("runtime switch to external", func(t *testing.T) {
		svc, fakes := newTestDoctor(t)
		// Config still says builtin, matching a daemon started before the
		// runtime switch. The live mode must win.
		svc.deps.Config.ServingMode = config.ServingModeBuiltin
		fakes.vhosts.servingMode = config.ServingModeNginx
		fakes.vhosts.status = externalVhostStatus(config.ServingModeNginx)

		rep, err := svc.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		check, ok := checkByID(rep, "serving-mode")
		if !ok || check.Status != StatusOK {
			t.Fatalf("serving-mode = %+v", check)
		}
		if !strings.Contains(check.Detail, "nginx") {
			t.Fatalf("serving-mode detail = %q, want the live nginx mode", check.Detail)
		}
		if hasCheck(rep, "vhost-server", StatusSkipped) {
			t.Fatalf("vhost-server must not be skipped after a runtime switch: %+v", rep.Checks)
		}
	})

	t.Run("runtime switch to builtin", func(t *testing.T) {
		svc, fakes := newTestDoctor(t)
		svc.deps.Config.ServingMode = config.ServingModeNginx
		fakes.vhosts.servingMode = config.ServingModeBuiltin
		fakes.vhosts.status = helperproto.VhostStatusResponse{Mode: "builtin", ConfigOK: true}

		rep, err := svc.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		check, ok := checkByID(rep, "serving-mode")
		if !ok || check.Status != StatusOK {
			t.Fatalf("serving-mode = %+v", check)
		}
		if !strings.Contains(check.Detail, "builtin") {
			t.Fatalf("serving-mode detail = %q, want builtin", check.Detail)
		}
		if !hasCheck(rep, "vhost-server", StatusSkipped) {
			t.Fatalf("vhost-server should be skipped in live builtin mode: %+v", rep.Checks)
		}
	})
}

func TestDoctorBuiltinWithoutVhosts(t *testing.T) {
	svc, _ := newTestDoctor(t)
	svc.deps.Config.ServingMode = config.ServingModeBuiltin
	svc.deps.Vhosts = nil

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(rep, "serving-mode", StatusOK) {
		t.Fatalf("serving-mode should pass without Vhosts in builtin mode: %+v", rep.Checks)
	}
	if !hasCheck(rep, "vhost-server", StatusSkipped) {
		t.Fatalf("vhost-server should be skipped: %+v", rep.Checks)
	}
}

func TestDoctorVhostTLS(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*helperproto.VhostStatusResponse)
		want       Status
		detailPart string
		remedyPart string
	}{
		{
			name:       "healthy",
			want:       StatusOK,
			detailPart: "0600",
		},
		{
			name: "bad key mode",
			mutate: func(status *helperproto.VhostStatusResponse) {
				setTLSFileMode(status, "blog.key", 0o644)
			},
			want:       StatusFail,
			detailPart: "blog.key",
			remedyPart: "chmod 600",
		},
		{
			name: "bad cert mode",
			mutate: func(status *helperproto.VhostStatusResponse) {
				setTLSFileMode(status, "blog.crt", 0o600)
			},
			want:       StatusFail,
			detailPart: "blog.crt",
			remedyPart: "chmod 644",
		},
		{
			name: "bad directory mode",
			mutate: func(status *helperproto.VhostStatusResponse) {
				status.TLSDirMode = 0o755
			},
			want:       StatusFail,
			detailPart: "mode",
			remedyPart: "chmod 700",
		},
		{
			name: "missing directory",
			mutate: func(status *helperproto.VhostStatusResponse) {
				status.TLSDirMode = 0
				status.TLSFiles = nil
			},
			want:       StatusFail,
			detailPart: "no TLS directory",
		},
		{
			name: "non-regular key",
			mutate: func(status *helperproto.VhostStatusResponse) {
				setTLSFileRegular(status, "blog.key", false)
			},
			want:       StatusFail,
			detailPart: "not a regular file",
			remedyPart: "chmod 600",
		},
		{
			name: "no certificate files",
			mutate: func(status *helperproto.VhostStatusResponse) {
				status.TLSFiles = []helperproto.VhostTLSFile{
					{Name: "blog.key", Mode: 0o600, Regular: true},
				}
			},
			want:       StatusWarn,
			detailPart: "no TLS certificate",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, fakes := newTestDoctor(t)
			enableExternalVhosts(t, svc, fakes, config.ServingModeNginx)
			if tt.mutate != nil {
				tt.mutate(&fakes.vhosts.status)
			}

			rep, err := svc.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			check, ok := checkByID(rep, "vhost-tls")
			if !ok {
				t.Fatalf("missing vhost-tls check: %+v", rep.Checks)
			}
			if check.Status != tt.want {
				t.Fatalf("vhost-tls = %+v", check)
			}
			if !strings.Contains(check.Detail, tt.detailPart) {
				t.Fatalf("vhost-tls detail = %q, want %q", check.Detail, tt.detailPart)
			}
			if tt.remedyPart != "" && !strings.Contains(check.Remediation, tt.remedyPart) {
				t.Fatalf("vhost-tls remediation = %q, want %q", check.Remediation, tt.remedyPart)
			}
		})
	}
}

func TestDoctorVhostTLSSkippedWithoutRelease(t *testing.T) {
	svc, fakes := newTestDoctor(t)
	enableExternalVhosts(t, svc, fakes, config.ServingModeNginx)
	fakes.vhosts.status.CurrentRelease = ""

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check, ok := checkByID(rep, "vhost-tls")
	if !ok {
		t.Fatalf("missing vhost-tls check: %+v", rep.Checks)
	}
	if check.Status != StatusSkipped && check.Status != StatusWarn {
		t.Fatalf("vhost-tls = %+v, want skipped or warn without a release", check)
	}
}

func enableExternalVhosts(t *testing.T, svc *Service, fakes *testFakes, mode string) {
	t.Helper()
	svc.deps.Config.ServingMode = mode
	fakes.vhosts.servingMode = mode
	fakes.vhosts.status = externalVhostStatus(mode)
	// External mode expects the built-in proxy ports to be unused.
	delete(fakes.ports.errs, ":8080")
	delete(fakes.ports.errs, ":8443")
}

func externalVhostStatus(mode string) helperproto.VhostStatusResponse {
	return helperproto.VhostStatusResponse{
		Mode:           mode,
		Server:         mode,
		ServerVersion:  "1.26.1",
		BridgePath:     "/etc/" + mode + "/conf.d/devstack-vhosts.conf",
		IncludePresent: true,
		CurrentRelease: filepath.Join("/etc/devstack/vhosts", mode, "releases", "20261023T120000Z"),
		ConfigOK:       true,
		LastSync:       time.Date(2026, 10, 23, 12, 30, 0, 0, time.UTC),
		ServiceActive:  true,
		TLSDir:         "/etc/devstack/tls",
		TLSDirMode:     0o700,
		TLSFiles: []helperproto.VhostTLSFile{
			{Name: "__dashboard__.crt", Mode: 0o644, Regular: true},
			{Name: "__dashboard__.key", Mode: 0o600, Regular: true},
			{Name: "blog.crt", Mode: 0o644, Regular: true},
			{Name: "blog.key", Mode: 0o600, Regular: true},
		},
	}
}

func setTLSFileMode(status *helperproto.VhostStatusResponse, name string, mode uint32) {
	for i := range status.TLSFiles {
		if status.TLSFiles[i].Name == name {
			status.TLSFiles[i].Mode = mode
			return
		}
	}
}

func setTLSFileRegular(status *helperproto.VhostStatusResponse, name string, regular bool) {
	for i := range status.TLSFiles {
		if status.TLSFiles[i].Name == name {
			status.TLSFiles[i].Regular = regular
			return
		}
	}
}

type fakeVhosts struct {
	status      helperproto.VhostStatusResponse
	servingMode string
	err         error
	calls       int
}

func (f *fakeVhosts) VhostStatus(ctx context.Context) (helperproto.VhostStatusResponse, error) {
	if err := ctx.Err(); err != nil {
		return helperproto.VhostStatusResponse{}, err
	}
	f.calls++
	if f.err != nil {
		return helperproto.VhostStatusResponse{}, f.err
	}
	return f.status, nil
}

func (f *fakeVhosts) ServingMode() string {
	return f.servingMode
}
