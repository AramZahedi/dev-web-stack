package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/client"
	"github.com/AramZahedi/dev-web-stack/internal/daemon"
	"github.com/AramZahedi/dev-web-stack/internal/doctor"
	"github.com/AramZahedi/dev-web-stack/internal/helperclient"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
	"github.com/AramZahedi/dev-web-stack/internal/trust"
)

// fakeClient implements Client for CLI tests. It never touches a daemon.
type fakeClient struct {
	sites     []model.Site
	site      model.Site
	addReq    sites.CreateRequest
	updateReq sites.UpdateRequest
	action    string
	actionRef string
	deleted   string

	logsRef    string
	logsFollow bool
	logsData   string

	doctorReport doctor.Report
	trust        []trust.Target
	runtimes     []model.RuntimeBuild
	available    []catalog.Entry

	installs     []string
	removals     []string
	lastHome     string
	newClientErr error
	err          error
}

func (f *fakeClient) maybeErr() error { return f.err }

func (f *fakeClient) ListSites(_ context.Context) ([]model.Site, error) {
	if err := f.maybeErr(); err != nil {
		return nil, err
	}
	if f.sites == nil {
		return []model.Site{}, nil
	}
	return f.sites, nil
}

func (f *fakeClient) GetSite(_ context.Context, ref string) (model.Site, error) {
	if err := f.maybeErr(); err != nil {
		return model.Site{}, err
	}
	if f.site.ID != "" || f.site.Name != "" {
		return f.site, nil
	}
	for _, site := range f.sites {
		if site.ID == ref || site.Name == ref {
			return site, nil
		}
	}
	return model.Site{}, &client.Error{Code: "NOT_FOUND", Message: "site " + ref + " not found"}
}

func (f *fakeClient) AddSite(_ context.Context, req sites.CreateRequest) (model.Site, error) {
	if err := f.maybeErr(); err != nil {
		return model.Site{}, err
	}
	f.addReq = req
	if f.site.ID != "" {
		return f.site, nil
	}
	return model.Site{
		ID:       "site-1",
		Name:     req.Name,
		Domain:   req.Name + ".test",
		Kind:     model.SiteKind(req.Kind),
		TLS:      req.TLS,
		Status:   model.StatusRunning,
		HostPath: req.Path,
	}, nil
}

func (f *fakeClient) UpdateSite(_ context.Context, _ string, req sites.UpdateRequest) (model.Site, error) {
	if err := f.maybeErr(); err != nil {
		return model.Site{}, err
	}
	f.updateReq = req
	return f.site, nil
}

func (f *fakeClient) SiteAction(_ context.Context, ref, action string) error {
	if err := f.maybeErr(); err != nil {
		return err
	}
	f.actionRef = ref
	f.action = action
	return nil
}

func (f *fakeClient) DeleteSite(_ context.Context, ref string) error {
	if err := f.maybeErr(); err != nil {
		return err
	}
	f.deleted = ref
	return nil
}

func (f *fakeClient) Logs(_ context.Context, ref string, follow bool) (io.ReadCloser, error) {
	if err := f.maybeErr(); err != nil {
		return nil, err
	}
	f.logsRef = ref
	f.logsFollow = follow
	return io.NopCloser(strings.NewReader(f.logsData)), nil
}

func (f *fakeClient) Doctor(_ context.Context) (doctor.Report, error) {
	if err := f.maybeErr(); err != nil {
		return doctor.Report{}, err
	}
	report := f.doctorReport
	if report.Checks == nil {
		report.Checks = []doctor.Check{}
	}
	return report, nil
}

func (f *fakeClient) Repair(_ context.Context) (doctor.Report, error) {
	return f.Doctor(context.Background())
}

func (f *fakeClient) TrustStatus(_ context.Context) ([]trust.Target, error) {
	if err := f.maybeErr(); err != nil {
		return nil, err
	}
	return f.targets(), nil
}

func (f *fakeClient) TrustRepair(_ context.Context) ([]trust.Target, error) {
	if err := f.maybeErr(); err != nil {
		return nil, err
	}
	return f.targets(), nil
}

func (f *fakeClient) TrustRemove(_ context.Context) ([]trust.Target, error) {
	if err := f.maybeErr(); err != nil {
		return nil, err
	}
	return f.targets(), nil
}

func (f *fakeClient) targets() []trust.Target {
	if f.trust == nil {
		return []trust.Target{}
	}
	return f.trust
}

func (f *fakeClient) Runtimes(_ context.Context) ([]model.RuntimeBuild, error) {
	if err := f.maybeErr(); err != nil {
		return nil, err
	}
	if f.runtimes == nil {
		return []model.RuntimeBuild{}, nil
	}
	return f.runtimes, nil
}

func (f *fakeClient) AvailableRuntimes(_ context.Context) ([]catalog.Entry, error) {
	if err := f.maybeErr(); err != nil {
		return nil, err
	}
	if f.available == nil {
		return []catalog.Entry{}, nil
	}
	return f.available, nil
}

func (f *fakeClient) InstallRuntime(_ context.Context, id string) (model.RuntimeBuild, error) {
	if err := f.maybeErr(); err != nil {
		return model.RuntimeBuild{}, err
	}
	f.installs = append(f.installs, id)
	return model.RuntimeBuild{ID: id, Kind: "node", Version: strings.TrimPrefix(id, "node@")}, nil
}

func (f *fakeClient) RemoveRuntime(_ context.Context, buildID string) error {
	if err := f.maybeErr(); err != nil {
		return err
	}
	f.removals = append(f.removals, buildID)
	return nil
}

// useTestSeams replaces every seam that would touch the host. Tests that need
// to observe a seam override it again after calling this helper; cleanups run
// in LIFO order and restore the original values.
func useTestSeams(t *testing.T) {
	t.Helper()
	oldEnsure, oldRun, oldDial, oldStop, oldOpen, oldHome := ensureRunning, runDaemon, dialHelper, stopDaemon, openBrowser, userHomeDir
	ensureRunning = func(context.Context, daemon.Options) error { return nil }
	runDaemon = func(context.Context, daemon.Options) error { return nil }
	dialHelper = func(string) (*helperclient.Client, error) {
		return nil, errors.New("test: unexpected helper dial")
	}
	stopDaemon = func(context.Context, string) (bool, error) { return false, nil }
	openBrowser = func(string) error { return nil }
	userHome := t.TempDir()
	userHomeDir = func() (string, error) { return userHome, nil }
	t.Cleanup(func() {
		ensureRunning, runDaemon, dialHelper, stopDaemon, openBrowser, userHomeDir = oldEnsure, oldRun, oldDial, oldStop, oldOpen, oldHome
	})
}

func newTestRoot(fake *fakeClient) *cobra.Command {
	return NewRootCmd(func(home string) (Client, error) {
		fake.lastHome = home
		if fake.newClientErr != nil {
			return nil, fake.newClientErr
		}
		return fake, nil
	})
}

func runCLI(t *testing.T, fake *fakeClient, args ...string) string {
	t.Helper()
	code, stdout, stderr := runCLIExit(t, fake, args...)
	if code != 0 {
		t.Fatalf("devstack %s exited %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), code, stdout, stderr)
	}
	return stdout
}

func runCLIExit(t *testing.T, fake *fakeClient, args ...string) (int, string, string) {
	t.Helper()
	root := newTestRoot(fake)
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(args)
	err := root.Execute()
	if err != nil {
		PrintError(root, err)
		return 1, stdout.String(), stderr.String()
	}
	return 0, stdout.String(), stderr.String()
}

func TestCLIAddAndList(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{sites: []model.Site{{Name: "blog", Domain: "blog.test", Status: model.StatusRunning}}}
	out := runCLI(t, fake, "site", "list")
	if !strings.Contains(out, "blog") || !strings.Contains(out, "https://blog.test") {
		t.Fatalf("output = %q", out)
	}
}

func TestCLIJSONOutputAndExitCode(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{sites: []model.Site{{Name: "blog", Domain: "blog.test"}}}
	out := runCLI(t, fake, "site", "list", "--json")
	var got []model.Site
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got) != 1 {
		t.Fatalf("%q %v", out, err)
	}
	fake.err = errors.New("boom")
	code, stdout, _ := runCLIExit(t, fake, "site", "start", "blog", "--json")
	if code == 0 {
		t.Fatal("want non-zero exit")
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("stdout = %q, want no JSON on stdout when the command fails", stdout)
	}
}

func TestCLISiteAddEnvAndValidation(t *testing.T) {
	useTestSeams(t)

	t.Run("env parsing", func(t *testing.T) {
		fake := &fakeClient{}
		out := runCLI(t, fake, "site", "add",
			"--name", "blog",
			"--path", "/tmp/blog",
			"--env", "A=1",
			"--env", "B=x=y",
			"--www",
			"--force-https",
			"--json",
		)
		var got model.Site
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("json: %v (%q)", err, out)
		}
		wantEnv := map[string]string{"A": "1", "B": "x=y"}
		if !reflect.DeepEqual(fake.addReq.Env, wantEnv) {
			t.Fatalf("Env = %#v, want %#v", fake.addReq.Env, wantEnv)
		}
		if !fake.addReq.TLS || !fake.addReq.ForceHTTPS || !fake.addReq.WWW {
			t.Fatalf("TLS/ForceHTTPS/WWW = %v/%v/%v, want true/true/true", fake.addReq.TLS, fake.addReq.ForceHTTPS, fake.addReq.WWW)
		}
		if got.Name != "blog" {
			t.Fatalf("site = %+v", got)
		}
	})

	t.Run("no-https", func(t *testing.T) {
		fake := &fakeClient{}
		out := runCLI(t, fake, "site", "add", "--name", "plain", "--path", "/tmp/plain", "--no-https")
		if fake.addReq.TLS {
			t.Fatal("TLS = true, want false with --no-https")
		}
		if !strings.Contains(out, "http://plain.test") {
			t.Fatalf("output = %q, want http URL", out)
		}
	})

	t.Run("invalid kind", func(t *testing.T) {
		fake := &fakeClient{}
		code, _, stderr := runCLIExit(t, fake, "site", "add", "--name", "php", "--path", "/tmp/php", "--kind", "php")
		if code == 0 {
			t.Fatal("want non-zero exit for kind php")
		}
		if !strings.Contains(stderr, "INVALID_KIND") || fake.addReq.Name != "" {
			t.Fatalf("stderr = %q, add request = %+v", stderr, fake.addReq)
		}
	})

	t.Run("missing name and path", func(t *testing.T) {
		fake := &fakeClient{}
		for _, args := range [][]string{
			{"site", "add", "--path", "/tmp/x"},
			{"site", "add", "--name", "x"},
		} {
			code, _, _ := runCLIExit(t, fake, args...)
			if code == 0 {
				t.Fatalf("%v: want non-zero exit", args)
			}
		}
	})

	t.Run("node requires version and command", func(t *testing.T) {
		fake := &fakeClient{}
		code, _, stderr := runCLIExit(t, fake, "site", "add", "--name", "app", "--path", "/tmp/app", "--kind", "node")
		if code == 0 || !strings.Contains(stderr, "--node") {
			t.Fatalf("code=%d stderr=%q, want missing --node error", code, stderr)
		}
		code, _, stderr = runCLIExit(t, fake, "site", "add", "--name", "app", "--path", "/tmp/app", "--kind", "node", "--node", "22")
		if code == 0 || !strings.Contains(stderr, "--command") {
			t.Fatalf("code=%d stderr=%q, want missing --command error", code, stderr)
		}
	})

	t.Run("no-https conflicts with force-https", func(t *testing.T) {
		fake := &fakeClient{}
		code, _, stderr := runCLIExit(t, fake, "site", "add", "--name", "x", "--path", "/tmp/x", "--no-https", "--force-https")
		if code == 0 || !strings.Contains(stderr, "no-https") {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
	})

	t.Run("invalid env", func(t *testing.T) {
		fake := &fakeClient{}
		code, _, stderr := runCLIExit(t, fake, "site", "add", "--name", "x", "--path", "/tmp/x", "--env", "MISSING_EQUALS")
		if code == 0 || !strings.Contains(stderr, "KEY=VALUE") {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
	})
}

func TestCLISiteAddInstallsMissingNodeRuntime(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{
		available: []catalog.Entry{{ID: "node@22.23.4", Kind: "node", Version: "22.23.4"}},
	}
	out := runCLI(t, fake, "site", "add",
		"--name", "app",
		"--path", "/tmp/app",
		"--kind", "node",
		"--node", "22",
		"--command", "npm start",
		"--yes",
	)
	if !reflect.DeepEqual(fake.installs, []string{"node@22.23.4"}) {
		t.Fatalf("installs = %v, want node@22.23.4", fake.installs)
	}
	if fake.addReq.NodeVersion != "22.23.4" || fake.addReq.Command != "npm start" {
		t.Fatalf("add request = %+v", fake.addReq)
	}
	if !strings.Contains(out, "https://app.test") || !strings.Contains(out, "running") {
		t.Fatalf("output = %q, want URL and status", out)
	}

	t.Run("already installed", func(t *testing.T) {
		installed := &fakeClient{
			available: []catalog.Entry{{ID: "node@22.23.4", Kind: "node", Version: "22.23.4"}},
			runtimes:  []model.RuntimeBuild{{ID: "node@22.23.4", Kind: "node", Version: "22.23.4"}},
		}
		runCLI(t, installed, "site", "add", "--name", "app2", "--path", "/tmp/app2", "--kind", "node", "--node", "22.23.4", "--command", "node server.js")
		if len(installed.installs) != 0 {
			t.Fatalf("installs = %v, want none", installed.installs)
		}
	})

	t.Run("unavailable version", func(t *testing.T) {
		empty := &fakeClient{}
		code, _, stderr := runCLIExit(t, empty, "site", "add", "--name", "app3", "--path", "/tmp/app3", "--kind", "node", "--node", "22", "--command", "npm start")
		if code == 0 || !strings.Contains(stderr, "RUNTIME_UNAVAILABLE") {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
	})
}

func TestCLIJSONReports(t *testing.T) {
	useTestSeams(t)

	t.Run("doctor", func(t *testing.T) {
		fake := &fakeClient{doctorReport: doctor.Report{OK: true, Checks: []doctor.Check{{ID: "helper", Title: "Helper", Status: doctor.StatusOK, Detail: "ready"}}}}
		out := runCLI(t, fake, "doctor", "--json")
		var got doctor.Report
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%q %v", out, err)
		}
		if !got.OK || len(got.Checks) != 1 || got.Checks[0].ID != "helper" {
			t.Fatalf("report = %+v", got)
		}
	})

	t.Run("runtimes", func(t *testing.T) {
		fake := &fakeClient{
			runtimes:  []model.RuntimeBuild{{ID: "node@24.22.1", Kind: "node", Version: "24.22.1"}},
			available: []catalog.Entry{{ID: "node@22.23.4", Kind: "node", Version: "22.23.4"}},
		}
		out := runCLI(t, fake, "runtime", "list", "--json")
		var got struct {
			Installed []model.RuntimeBuild `json:"installed"`
			Available []catalog.Entry      `json:"available"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%q %v", out, err)
		}
		if len(got.Installed) != 1 || len(got.Available) != 1 {
			t.Fatalf("runtimes = %+v", got)
		}
	})

	t.Run("trust", func(t *testing.T) {
		fake := &fakeClient{trust: []trust.Target{{Name: "system", OK: true, Detail: "installed"}}}
		out := runCLI(t, fake, "trust", "status", "--json")
		var got []trust.Target
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%q %v", out, err)
		}
		if len(got) != 1 || got[0].Name != "system" {
			t.Fatalf("targets = %+v", got)
		}
	})
}

func TestCLIDoctorFailureExitCode(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{doctorReport: doctor.Report{OK: false, Checks: []doctor.Check{{
		ID: "helper", Title: "Helper", Status: doctor.StatusFail, Detail: "down", Remediation: "Run `devstack init`.",
	}}}}
	code, stdout, stderr := runCLIExit(t, fake, "doctor")
	if code == 0 {
		t.Fatal("want non-zero exit for a failing doctor report")
	}
	if !strings.Contains(stdout, "helper") {
		t.Fatalf("stdout = %q, want the report", stdout)
	}
	if !strings.Contains(stderr, "DOCTOR_FAILED") || !strings.Contains(stderr, "Remediation:") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestCLIInit(t *testing.T) {
	useTestSeams(t)
	helper, socket := startFakeHelper(t)
	home := t.TempDir()
	userHome := t.TempDir()

	oldDial := dialHelper
	dialHelper = func(string) (*helperclient.Client, error) { return helperclient.Dial(socket) }
	t.Cleanup(func() { dialHelper = oldDial })
	oldHome := userHomeDir
	userHomeDir = func() (string, error) { return userHome, nil }
	t.Cleanup(func() { userHomeDir = oldHome })

	var ensured daemon.Options
	oldEnsure := ensureRunning
	ensureRunning = func(_ context.Context, opts daemon.Options) error {
		ensured = opts
		return nil
	}
	t.Cleanup(func() { ensureRunning = oldEnsure })

	fake := &fakeClient{
		sites: []model.Site{{Name: "blog", Domain: "blog.test", WWW: true}},
		doctorReport: doctor.Report{OK: true, Checks: []doctor.Check{{
			ID: "helper", Title: "Helper", Status: doctor.StatusOK, Detail: "ready",
		}}},
	}
	out := runCLI(t, fake, "init", "--home", home, "--helper-socket", socket, "--json")

	var result initResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("%q %v", out, err)
	}
	if result.CAFingerprint != "test-fingerprint" {
		t.Fatalf("fingerprint = %q", result.CAFingerprint)
	}
	if len(result.Trust) != 1 || result.Trust[0].Name != "system" {
		t.Fatalf("trust = %+v", result.Trust)
	}
	if !result.Doctor.OK {
		t.Fatalf("doctor = %+v", result.Doctor)
	}
	if result.DashboardURL != "https://devstack.test" {
		t.Fatalf("dashboard = %q", result.DashboardURL)
	}
	for _, want := range []string{"devstack.test", "blog.test", "www.blog.test"} {
		if !contains(result.Hosts, want) {
			t.Fatalf("hosts = %v, missing %q", result.Hosts, want)
		}
	}
	if fake.lastHome != home {
		t.Fatalf("client home = %q, want %q", fake.lastHome, home)
	}
	if ensured.Home != home || ensured.HelperSocket != socket {
		t.Fatalf("ensure options = %+v", ensured)
	}

	calls := helper.snapshot()
	wantPaths := []string{
		helperproto.PathEnsureCA,
		helperproto.PathForwarders,
		helperproto.PathTrustInstall,
		helperproto.PathHostsSync,
	}
	if got := callPaths(calls); !reflect.DeepEqual(got, wantPaths) {
		t.Fatalf("helper calls = %v, want %v", got, wantPaths)
	}
	var forward helperproto.ForwardersRequest
	if err := json.Unmarshal([]byte(calls[1].Body), &forward); err != nil {
		t.Fatal(err)
	}
	if !forward.Enable || forward.HTTPPort != 8080 || forward.HTTPSPort != 8443 {
		t.Fatalf("forwarders = %+v", forward)
	}
	var install helperproto.TrustRequest
	if err := json.Unmarshal([]byte(calls[2].Body), &install); err != nil {
		t.Fatal(err)
	}
	if install.UserHome != userHome {
		t.Fatalf("trust user home = %q, want %q", install.UserHome, userHome)
	}
	var sync helperproto.HostsSyncRequest
	if err := json.Unmarshal([]byte(calls[3].Body), &sync); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"devstack.test", "blog.test", "www.blog.test"} {
		if !contains(sync.Domains, want) {
			t.Fatalf("synced domains = %v, missing %q", sync.Domains, want)
		}
	}
}

func TestCLIInitHelperError(t *testing.T) {
	useTestSeams(t)
	helper, socket := startFakeHelper(t)
	helper.failPath = helperproto.PathEnsureCA
	helper.failCode = helperproto.CodeInternal
	helper.failMessage = "helper is down"

	oldDial := dialHelper
	dialHelper = func(string) (*helperclient.Client, error) { return helperclient.Dial(socket) }
	t.Cleanup(func() { dialHelper = oldDial })

	var ensureCalls int
	oldEnsure := ensureRunning
	ensureRunning = func(context.Context, daemon.Options) error { ensureCalls++; return nil }
	t.Cleanup(func() { ensureRunning = oldEnsure })

	fake := &fakeClient{}
	code, stdout, stderr := runCLIExit(t, fake, "init", "--home", t.TempDir(), "--helper-socket", socket)
	if code == 0 {
		t.Fatal("want non-zero exit")
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	for _, want := range []string{"INTERNAL", "helper is down", "Remediation:"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, missing %q", stderr, want)
		}
	}
	if ensureCalls != 0 {
		t.Fatalf("ensure calls = %d, want 0 after a helper failure", ensureCalls)
	}
}

func TestCLIUninstall(t *testing.T) {
	useTestSeams(t)
	helper, socket := startFakeHelper(t)
	home := t.TempDir()

	oldDial := dialHelper
	dialHelper = func(string) (*helperclient.Client, error) { return helperclient.Dial(socket) }
	t.Cleanup(func() { dialHelper = oldDial })

	var events []string
	var mu sync.Mutex
	record := func(value string) {
		mu.Lock()
		events = append(events, value)
		mu.Unlock()
	}
	oldStop := stopDaemon
	stopDaemon = func(_ context.Context, gotHome string) (bool, error) {
		if gotHome != home {
			t.Errorf("stop home = %q, want %q", gotHome, home)
		}
		record("stop")
		return true, nil
	}
	t.Cleanup(func() { stopDaemon = oldStop })
	helper.onRequest = func(path string) {
		if path == helperproto.PathUninstall {
			record("uninstall")
		}
	}

	fake := &fakeClient{}
	out := runCLI(t, fake, "uninstall", "--home", home, "--yes", "--json")
	var result uninstallResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("%q %v", out, err)
	}
	if !result.Uninstalled || !result.DaemonStopped {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(result.Command, "sudo") {
		t.Fatalf("elevated command = %q", result.Command)
	}
	if result.ProjectFilesRemoved {
		t.Fatal("ProjectFilesRemoved = true")
	}
	if !containsFold(result.Checklist, "project files") {
		t.Fatalf("checklist = %v", result.Checklist)
	}
	if !reflect.DeepEqual(callPaths(helper.snapshot()), []string{helperproto.PathUninstall}) {
		t.Fatalf("helper calls = %v", callPaths(helper.snapshot()))
	}
	mu.Lock()
	gotEvents := append([]string(nil), events...)
	mu.Unlock()
	if !reflect.DeepEqual(gotEvents, []string{"stop", "uninstall"}) {
		t.Fatalf("events = %v, want stop before uninstall", gotEvents)
	}

	t.Run("requires yes", func(t *testing.T) {
		helper.reset()
		code, _, stderr := runCLIExit(t, fake, "uninstall", "--home", home)
		if code == 0 || !strings.Contains(stderr, "--yes") {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
		if len(helper.snapshot()) != 0 {
			t.Fatalf("helper calls = %v, want none", callPaths(helper.snapshot()))
		}
	})
}

func TestCLISiteLogsFollowAndTail(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{
		sites: []model.Site{{Name: "blog", Domain: "blog.test"}},
		logsData: "data: {\"time\":\"2026-10-23T10:00:00Z\",\"source\":\"app\",\"text\":\"one\"}\n\n" +
			"data: {\"time\":\"2026-10-23T10:00:01Z\",\"source\":\"app\",\"text\":\"two\"}\n\n" +
			"data: {\"time\":\"2026-10-23T10:00:02Z\",\"source\":\"app\",\"text\":\"three\"}\n\n",
	}
	out := runCLI(t, fake, "site", "logs", "blog", "--follow")
	if !fake.logsFollow || fake.logsRef != "blog" {
		t.Fatalf("Logs(%q, follow=%v), want blog/true", fake.logsRef, fake.logsFollow)
	}
	for _, want := range []string{"one", "two", "three"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output = %q, missing %q", out, want)
		}
	}

	out = runCLI(t, fake, "site", "logs", "blog", "--follow=false", "--tail", "2")
	if fake.logsFollow {
		t.Fatal("Logs follow = true, want false with --follow=false")
	}
	if strings.Contains(out, "one") {
		t.Fatalf("output = %q, want only the last two lines", out)
	}
	for _, want := range []string{"two", "three"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output = %q, missing %q", out, want)
		}
	}
}

func TestCLIHumanErrorIncludesCodeMessageRemediation(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{err: &client.Error{Code: "NOT_FOUND", Message: `site "blog" not found`}}
	code, stdout, stderr := runCLIExit(t, fake, "site", "start", "blog")
	if code == 0 {
		t.Fatal("want non-zero exit")
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	for _, want := range []string{"NOT_FOUND", `site "blog" not found`, "Remediation:", "devstack site list"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, missing %q", stderr, want)
		}
	}
}

func TestCLIHomeResolution(t *testing.T) {
	useTestSeams(t)
	base := t.TempDir()
	envHome := filepath.Join(base, "env-home")
	flagHome := filepath.Join(base, "flag-home")
	t.Setenv("DEVSTACK_HOME", envHome)

	fake := &fakeClient{}
	runCLI(t, fake, "site", "list", "--home", flagHome)
	wantFlag, err := filepath.Abs(flagHome)
	if err != nil {
		t.Fatal(err)
	}
	if fake.lastHome != wantFlag {
		t.Fatalf("home = %q, want %q (flag wins over DEVSTACK_HOME)", fake.lastHome, wantFlag)
	}

	runCLI(t, fake, "site", "list")
	wantEnv, err := filepath.Abs(envHome)
	if err != nil {
		t.Fatal(err)
	}
	if fake.lastHome != wantEnv {
		t.Fatalf("home = %q, want %q from DEVSTACK_HOME", fake.lastHome, wantEnv)
	}
}

func TestCLIRemoveRequiresYes(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{sites: []model.Site{{Name: "blog", Domain: "blog.test"}}}

	code, _, stderr := runCLIExit(t, fake, "site", "remove", "blog")
	if code == 0 || !strings.Contains(stderr, "--yes") {
		t.Fatalf("site remove: code=%d stderr=%q", code, stderr)
	}
	if fake.deleted != "" {
		t.Fatalf("deleted = %q, want none", fake.deleted)
	}
	runCLI(t, fake, "site", "remove", "blog", "--yes")
	if fake.deleted != "blog" {
		t.Fatalf("deleted = %q, want blog", fake.deleted)
	}

	code, _, stderr = runCLIExit(t, fake, "runtime", "remove", "node@24.22.1")
	if code == 0 || !strings.Contains(stderr, "--yes") {
		t.Fatalf("runtime remove: code=%d stderr=%q", code, stderr)
	}
	if len(fake.removals) != 0 {
		t.Fatalf("removals = %v, want none", fake.removals)
	}
	runCLI(t, fake, "runtime", "remove", "node@24.22.1", "--yes")
	if !reflect.DeepEqual(fake.removals, []string{"node@24.22.1"}) {
		t.Fatalf("removals = %v", fake.removals)
	}
}

func TestCLIVersion(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	out := runCLI(t, fake, "version")
	if strings.TrimSpace(out) == "" {
		t.Fatal("version output is empty")
	}
	out = runCLI(t, fake, "version", "--json")
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%q %v", out, err)
	}
	if got["version"] == "" {
		t.Fatalf("version = %+v", got)
	}
}

func TestCLIUIOpensDashboard(t *testing.T) {
	useTestSeams(t)
	var opened []string
	oldOpen := openBrowser
	openBrowser = func(url string) error {
		opened = append(opened, url)
		return nil
	}
	t.Cleanup(func() { openBrowser = oldOpen })

	fake := &fakeClient{}
	out := runCLI(t, fake, "ui", "--home", t.TempDir())
	if !strings.Contains(out, "https://devstack.test") || !strings.Contains(out, "127.0.0.1") {
		t.Fatalf("output = %q", out)
	}
	if !reflect.DeepEqual(opened, []string{"https://devstack.test"}) {
		t.Fatalf("opened = %v", opened)
	}
}

func TestCLIDaemonStatusStopped(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	home := t.TempDir()
	out := runCLI(t, fake, "daemon", "status", "--home", home)
	if !strings.Contains(out, "stopped") {
		t.Fatalf("output = %q", out)
	}
	out = runCLI(t, fake, "daemon", "status", "--home", home, "--json")
	var got daemonStatus
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%q %v", out, err)
	}
	if got.Running {
		t.Fatalf("status = %+v", got)
	}
}

// fakeHelper is an HTTP helper served over a Unix socket. It records the calls
// the CLI makes so init/uninstall tests can assert the protocol surface.
type fakeHelper struct {
	mu          sync.Mutex
	calls       []helperCall
	failPath    string
	failCode    string
	failMessage string
	onRequest   func(path string)
}

type helperCall struct {
	Path string
	Body string
}

func (f *fakeHelper) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.calls = append(f.calls, helperCall{Path: r.URL.Path, Body: string(body)})
	failPath, failCode, failMessage, onRequest := f.failPath, f.failCode, f.failMessage, f.onRequest
	f.mu.Unlock()
	if onRequest != nil {
		onRequest(r.URL.Path)
	}
	if r.URL.Path == failPath {
		if failCode == "" {
			failCode = "INTERNAL"
		}
		writeHelperJSON(w, http.StatusInternalServerError, helperproto.ErrorResponse{Code: failCode, Message: failMessage})
		return
	}
	switch r.URL.Path {
	case helperproto.PathEnsureCA:
		writeHelperJSON(w, http.StatusOK, helperproto.EnsureCAResponse{RootPEM: []byte("test-root-pem"), Fingerprint: "test-fingerprint"})
	case helperproto.PathForwarders, helperproto.PathUninstall:
		writeHelperJSON(w, http.StatusOK, struct{}{})
	case helperproto.PathHostsSync:
		writeHelperJSON(w, http.StatusOK, helperproto.HostsSyncResponse{Changed: true})
	case helperproto.PathTrustInstall, helperproto.PathTrustRemove, helperproto.PathTrustStatus:
		writeHelperJSON(w, http.StatusOK, helperproto.TrustResponse{Targets: []trust.Target{{Name: "system", OK: true, Detail: "installed"}}})
	default:
		writeHelperJSON(w, http.StatusNotFound, helperproto.ErrorResponse{Code: "NOT_FOUND", Message: "unknown helper path"})
	}
}

func writeHelperJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (f *fakeHelper) snapshot() []helperCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]helperCall(nil), f.calls...)
}

func (f *fakeHelper) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

func startFakeHelper(t *testing.T) (*fakeHelper, string) {
	t.Helper()
	fake := &fakeHelper{}
	socket := filepath.Join(t.TempDir(), "helper.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen helper socket: %v", err)
	}
	server := &http.Server{Handler: fake}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = os.Remove(socket)
	})
	return fake, socket
}

func callPaths(calls []helperCall) []string {
	paths := make([]string, len(calls))
	for i, call := range calls {
		paths[i] = call.Path
	}
	return paths
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsFold(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), strings.ToLower(want)) {
			return true
		}
	}
	return false
}
