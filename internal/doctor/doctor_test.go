package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
		trust:     ft,
		ports:     &fakePorts{errs: map[string]error{}},
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

func TestPortChecks(t *testing.T) {
	tests := []struct {
		name string
		addr string
		err  error
		want Status
		ok   bool
	}{
		{name: "all free", want: StatusOK, ok: true},
		{name: "required port occupied", addr: ":443", err: syscall.EADDRINUSE, want: StatusFail, ok: false},
		{name: "proxy port occupied is expected", addr: ":8080", err: syscall.EADDRINUSE, want: StatusWarn, ok: true},
		{name: "unexpected proxy error fails", addr: ":8443", err: errors.New("permission denied"), want: StatusFail, ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, fakes := newTestDoctor(t)
			if tt.addr != "" {
				fakes.ports.errs[tt.addr] = tt.err
			}

			rep, err := svc.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !hasCheck(rep, "ports", tt.want) {
				t.Fatalf("%+v", rep.Checks)
			}
			if rep.OK != tt.ok {
				t.Fatalf("OK = %v, want %v: %+v", rep.OK, tt.ok, rep.Checks)
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

func hasCheck(rep Report, id string, status Status) bool {
	for _, check := range rep.Checks {
		if check.ID == id {
			return check.Status == status
		}
	}
	return false
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
