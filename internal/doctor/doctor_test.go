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
	store     *store.Store
	hostsPath string
	home      string
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
		store:     st,
		hostsPath: hostsPath,
		home:      filepath.Join(dir, "user"),
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
	for _, id := range []string{"helper", "ca", "hosts", "trust", "ports", "runtimes", "disk", "wsl"} {
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
