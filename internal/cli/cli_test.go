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
	"github.com/AramZahedi/dev-web-stack/internal/config"
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

	vhostStatus    helperproto.VhostStatusResponse
	servingModeSet bool
	servingMode    string
	vhostSyncs     int
	previewRef     string
	previewServer  string
	previewContent string
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

func (f *fakeClient) VhostStatus(_ context.Context) (helperproto.VhostStatusResponse, error) {
	if err := f.maybeErr(); err != nil {
		return helperproto.VhostStatusResponse{}, err
	}
	status := f.vhostStatus
	if strings.TrimSpace(status.Mode) == "" {
		status.Mode = "builtin"
	}
	return status, nil
}

func (f *fakeClient) SetServingMode(_ context.Context, mode string) (helperproto.VhostStatusResponse, error) {
	if err := f.maybeErr(); err != nil {
		return helperproto.VhostStatusResponse{}, err
	}
	f.servingModeSet = true
	f.servingMode = mode
	f.vhostStatus.Mode = mode
	return f.vhostStatus, nil
}

func (f *fakeClient) SyncVhosts(_ context.Context) (helperproto.VhostStatusResponse, error) {
	if err := f.maybeErr(); err != nil {
		return helperproto.VhostStatusResponse{}, err
	}
	f.vhostSyncs++
	return f.vhostStatus, nil
}

func (f *fakeClient) PreviewVhost(_ context.Context, ref, server string) (string, error) {
	if err := f.maybeErr(); err != nil {
		return "", err
	}
	f.previewRef = ref
	f.previewServer = server
	return f.previewContent, nil
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
	return runCLIExitInput(t, fake, "", args...)
}

func runCLIExitInput(t *testing.T, fake *fakeClient, input string, args ...string) (int, string, string) {
	t.Helper()
	root := newTestRoot(fake)
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(input))
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

func TestCLITrustJSONFailureExitsNonZero(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{trust: []trust.Target{{Name: "system", OK: false, Detail: "missing"}}}
	code, stdout, stderr := runCLIExit(t, fake, "trust", "status", "--json")
	if code == 0 {
		t.Fatal("want non-zero exit when a trust target fails")
	}
	var got []trust.Target
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not JSON: %q: %v", stdout, err)
	}
	if len(got) != 1 || got[0].OK {
		t.Fatalf("targets = %+v", got)
	}
	for _, want := range []string{"TRUST_FAILED", `"remediation"`} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, missing %q", stderr, want)
		}
	}
}

func TestCLITrustRemoveRequiresYes(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{trust: []trust.Target{{Name: "system", OK: true, Detail: "removed"}}}

	code, _, stderr := runCLIExit(t, fake, "trust", "remove")
	if code == 0 || !strings.Contains(stderr, "--yes") {
		t.Fatalf("code=%d stderr=%q, want --yes error", code, stderr)
	}
	runCLI(t, fake, "trust", "remove", "--yes")
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
	var eventsMu sync.Mutex
	var events []string
	recordEvent := func(value string) {
		eventsMu.Lock()
		events = append(events, value)
		eventsMu.Unlock()
	}
	helper.onRequest = recordEvent
	oldEnsure := ensureRunning
	ensureRunning = func(_ context.Context, opts daemon.Options) error {
		ensured = opts
		recordEvent("ensure")
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
		helperproto.PathVhostsStatus,
		helperproto.PathForwarders,
		helperproto.PathTrustInstall,
		helperproto.PathHostsSync,
		helperproto.PathHostsSync,
	}
	if got := callPaths(calls); !reflect.DeepEqual(got, wantPaths) {
		t.Fatalf("helper calls = %v, want %v", got, wantPaths)
	}
	eventsMu.Lock()
	gotEvents := append([]string(nil), events...)
	eventsMu.Unlock()
	wantEvents := []string{
		helperproto.PathEnsureCA,
		helperproto.PathVhostsStatus,
		helperproto.PathForwarders,
		helperproto.PathTrustInstall,
		helperproto.PathHostsSync,
		"ensure",
		helperproto.PathHostsSync,
	}
	if !reflect.DeepEqual(gotEvents, wantEvents) {
		t.Fatalf("events = %v, want baseline hosts sync before daemon startup", gotEvents)
	}
	var forward helperproto.ForwardersRequest
	if err := json.Unmarshal([]byte(calls[2].Body), &forward); err != nil {
		t.Fatal(err)
	}
	if !forward.Enable || forward.HTTPPort != 8080 || forward.HTTPSPort != 8443 {
		t.Fatalf("forwarders = %+v", forward)
	}
	var install helperproto.TrustRequest
	if err := json.Unmarshal([]byte(calls[3].Body), &install); err != nil {
		t.Fatal(err)
	}
	if install.UserHome != userHome {
		t.Fatalf("trust user home = %q, want %q", install.UserHome, userHome)
	}
	var baseline helperproto.HostsSyncRequest
	if err := json.Unmarshal([]byte(calls[4].Body), &baseline); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(baseline.Domains, []string{"devstack.test"}) {
		t.Fatalf("baseline domains = %v, want [devstack.test]", baseline.Domains)
	}
	var sync helperproto.HostsSyncRequest
	if err := json.Unmarshal([]byte(calls[5].Body), &sync); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"devstack.test", "blog.test", "www.blog.test"} {
		if !contains(sync.Domains, want) {
			t.Fatalf("synced domains = %v, missing %q", sync.Domains, want)
		}
	}
}

func TestCLIInitExternalModeSkipsForwarders(t *testing.T) {
	useTestSeams(t)
	helper, socket := startFakeHelper(t)
	helper.vhostStatus = helperproto.VhostStatusResponse{Mode: config.ServingModeNginx, Server: "nginx", ConfigOK: true}
	home := t.TempDir()
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ServingMode = config.ServingModeNginx
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	oldDial := dialHelper
	dialHelper = func(string) (*helperclient.Client, error) { return helperclient.Dial(socket) }
	t.Cleanup(func() { dialHelper = oldDial })
	oldEnsure := ensureRunning
	ensureRunning = func(context.Context, daemon.Options) error { return nil }
	t.Cleanup(func() { ensureRunning = oldEnsure })

	fake := &fakeClient{
		doctorReport: doctor.Report{OK: true, Checks: []doctor.Check{}},
	}
	runCLI(t, fake, "init", "--home", home, "--helper-socket", socket, "--json")

	if got := callPaths(helper.snapshot()); contains(got, helperproto.PathForwarders) {
		t.Fatalf("init enabled the built-in forwarders in external mode: %v", got)
	}
	if fake.vhostSyncs != 1 {
		t.Fatalf("vhost syncs = %d, want 1 after external-mode init", fake.vhostSyncs)
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

func TestCLIInitDaemonErrorUsesResolvedHome(t *testing.T) {
	useTestSeams(t)
	_, socket := startFakeHelper(t)
	home := t.TempDir()

	oldDial := dialHelper
	dialHelper = func(string) (*helperclient.Client, error) { return helperclient.Dial(socket) }
	t.Cleanup(func() { dialHelper = oldDial })
	oldEnsure := ensureRunning
	ensureRunning = func(context.Context, daemon.Options) error { return errors.New("daemon failed to start") }
	t.Cleanup(func() { ensureRunning = oldEnsure })

	fake := &fakeClient{}
	code, stdout, stderr := runCLIExit(t, fake, "init", "--home", home, "--helper-socket", socket)
	if code == 0 {
		t.Fatal("want non-zero exit")
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	wantLog := filepath.Join(home, "run", "daemon.log")
	if !strings.Contains(stderr, wantLog) {
		t.Fatalf("stderr = %q, want resolved log path %q", stderr, wantLog)
	}
	if strings.Contains(stderr, "~/.devstack") {
		t.Fatalf("stderr = %q, must not contain a hardcoded home", stderr)
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
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete", result.Status)
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
	if !containsFold(result.Checklist, home) || containsFold(result.Checklist, "~/.devstack") {
		t.Fatalf("checklist = %v, want resolved home %q and no hardcoded path", result.Checklist, home)
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

func TestCLIUninstallHelperUnavailable(t *testing.T) {
	useTestSeams(t)
	home := t.TempDir()

	var stopped bool
	oldStop := stopDaemon
	stopDaemon = func(_ context.Context, gotHome string) (bool, error) {
		if gotHome != home {
			t.Errorf("stop home = %q, want %q", gotHome, home)
		}
		stopped = true
		return true, nil
	}
	t.Cleanup(func() { stopDaemon = oldStop })
	oldDial := dialHelper
	dialHelper = func(string) (*helperclient.Client, error) { return nil, errors.New("helper socket is missing") }
	t.Cleanup(func() { dialHelper = oldDial })

	fake := &fakeClient{}
	code, stdout, stderr := runCLIExit(t, fake, "uninstall", "--home", home, "--yes")
	if code == 0 {
		t.Fatal("want non-zero exit")
	}
	if !stopped {
		t.Fatal("daemon was not stopped before the helper failure")
	}
	for _, want := range []string{"Uninstall incomplete.", "To remove the helper unit", "sudo", "Checklist:", "Project files were left untouched", home} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, missing %q", stdout, want)
		}
	}
	if !strings.Contains(stderr, "HELPER_UNAVAILABLE") || !strings.Contains(stderr, "Remediation:") {
		t.Fatalf("stderr = %q", stderr)
	}
	if strings.Contains(stdout, "Uninstalled devstack integration.") {
		t.Fatalf("stdout = %q, must not report success", stdout)
	}
}

func TestCLIUninstallHelperError(t *testing.T) {
	useTestSeams(t)
	helper, socket := startFakeHelper(t)
	helper.failPath = helperproto.PathUninstall
	helper.failCode = helperproto.CodeInternal
	helper.failMessage = "uninstall blew up"
	home := t.TempDir()

	oldDial := dialHelper
	dialHelper = func(string) (*helperclient.Client, error) { return helperclient.Dial(socket) }
	t.Cleanup(func() { dialHelper = oldDial })
	oldStop := stopDaemon
	stopDaemon = func(context.Context, string) (bool, error) { return true, nil }
	t.Cleanup(func() { stopDaemon = oldStop })

	fake := &fakeClient{}
	code, stdout, stderr := runCLIExit(t, fake, "uninstall", "--home", home, "--yes", "--json")
	if code == 0 {
		t.Fatal("want non-zero exit")
	}
	var result uninstallResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("stdout is not JSON: %q: %v", stdout, err)
	}
	if result.Status != "incomplete" || result.Uninstalled {
		t.Fatalf("result = %+v, want incomplete and not uninstalled", result)
	}
	if !strings.Contains(result.Command, "sudo") || !containsFold(result.Checklist, home) {
		t.Fatalf("instructions = %+v", result)
	}
	for _, want := range []string{"INTERNAL", "uninstall blew up", `"remediation"`} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, missing %q", stderr, want)
		}
	}
	if !reflect.DeepEqual(callPaths(helper.snapshot()), []string{helperproto.PathUninstall}) {
		t.Fatalf("helper calls = %v", callPaths(helper.snapshot()))
	}
}

func TestCLIUninstallDaemonStopFailureStillPrintsInstructions(t *testing.T) {
	useTestSeams(t)
	home := t.TempDir()
	oldStop := stopDaemon
	stopDaemon = func(context.Context, string) (bool, error) { return false, errors.New("daemon is wedged") }
	t.Cleanup(func() { stopDaemon = oldStop })
	oldDial := dialHelper
	dialHelper = func(string) (*helperclient.Client, error) {
		t.Fatal("helper must not be dialed after a daemon stop failure")
		return nil, nil
	}
	t.Cleanup(func() { dialHelper = oldDial })

	fake := &fakeClient{}
	code, stdout, stderr := runCLIExit(t, fake, "uninstall", "--home", home, "--yes")
	if code == 0 {
		t.Fatal("want non-zero exit")
	}
	for _, want := range []string{"Uninstall incomplete.", "Daemon: stop failed", "sudo", "Checklist:", "Project files were left untouched", home} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, missing %q", stdout, want)
		}
	}
	if strings.Contains(stdout, "Uninstalled devstack integration.") {
		t.Fatalf("stdout = %q, must not report success", stdout)
	}
	if !strings.Contains(stderr, "daemon is wedged") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestCLISiteLogsDefaultFollowAndTail(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{
		sites: []model.Site{{Name: "blog", Domain: "blog.test"}},
		logsData: "data: {\"time\":\"2026-10-23T10:00:00Z\",\"source\":\"app\",\"text\":\"one\"}\n\n" +
			"data: {\"time\":\"2026-10-23T10:00:01Z\",\"source\":\"app\",\"text\":\"two\"}\n\n" +
			"data: {\"time\":\"2026-10-23T10:00:02Z\",\"source\":\"app\",\"text\":\"three\"}\n\n",
	}

	out := runCLI(t, fake, "site", "logs", "blog")
	if fake.logsFollow {
		t.Fatal("default Logs follow = true, want a finite snapshot")
	}
	for _, want := range []string{"one", "two", "three"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output = %q, missing %q", out, want)
		}
	}

	out = runCLI(t, fake, "site", "logs", "blog", "--follow")
	if !fake.logsFollow || fake.logsRef != "blog" {
		t.Fatalf("Logs(%q, follow=%v), want blog/true", fake.logsRef, fake.logsFollow)
	}
	for _, want := range []string{"one", "two", "three"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output = %q, missing %q", out, want)
		}
	}

	out = runCLI(t, fake, "site", "logs", "blog", "--tail", "2")
	if fake.logsFollow {
		t.Fatal("Logs follow = true, want a finite snapshot with --tail")
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

func TestCLISiteLogsJSON(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{
		sites: []model.Site{{Name: "blog", Domain: "blog.test"}},
		logsData: "data: {\"time\":\"2026-10-23T10:00:00Z\",\"source\":\"app\",\"text\":\"one\"}\n\n" +
			"data: {\"time\":\"2026-10-23T10:00:01Z\",\"source\":\"app\",\"text\":\"two\"}\n\n" +
			"data: {\"time\":\"2026-10-23T10:00:02Z\",\"source\":\"app\",\"text\":\"three\"}\n\n",
	}

	out := runCLI(t, fake, "site", "logs", "blog", "--json")
	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("JSONL lines = %d (%q), want 3", len(lines), out)
	}
	wantText := []string{"one", "two", "three"}
	for i, line := range lines {
		var record logLineJSON
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("line %d is not JSON: %q: %v", i, line, err)
		}
		if record.Text != wantText[i] || record.Source != "app" || record.Time == "" {
			t.Fatalf("record %d = %+v", i, record)
		}
	}

	out = runCLI(t, fake, "site", "logs", "blog", "--json", "--tail", "2")
	lines = nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("JSONL lines = %d (%q), want 2", len(lines), out)
	}
	for i, line := range lines {
		var record logLineJSON
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("line %d is not JSON: %q: %v", i, line, err)
		}
		if record.Text != wantText[i+1] {
			t.Fatalf("record %d = %+v, want text %q", i, record, wantText[i+1])
		}
	}
}

func nonEmptyLines(value string) []string {
	var lines []string
	for _, line := range strings.Split(value, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
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

func TestCLIDaemonStopRejectsPidOne(t *testing.T) {
	useTestSeams(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "run"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemon.PidPath(home), []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldStop := stopDaemon
	stopDaemon = daemonStop
	t.Cleanup(func() { stopDaemon = oldStop })

	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "daemon", "stop", "--home", home)
	if code == 0 {
		t.Fatal("want non-zero exit for pid 1")
	}
	for _, want := range []string{"DAEMON_STOP_REFUSED", "pid 1"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, missing %q", stderr, want)
		}
	}
}

func TestDaemonStopRejectsForeignPID(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "run"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemon.PidPath(home), []byte("4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldRead := readProcCmdline
	readProcCmdline = func(pid int) ([]byte, error) {
		if pid != 4242 {
			t.Fatalf("readProcCmdline(%d), want 4242", pid)
		}
		return []byte("/usr/bin/python3\x00script.py\x00"), nil
	}
	t.Cleanup(func() { readProcCmdline = oldRead })

	stopped, err := daemonStop(context.Background(), home)
	if err == nil || stopped {
		t.Fatalf("daemonStop = (%v, %v), want refusal", stopped, err)
	}
	converted := asError(err)
	for _, want := range []string{"DAEMON_STOP_REFUSED", "does not belong to devstack"} {
		if !strings.Contains(converted.Error(), want) {
			t.Fatalf("error = %q, missing %q", converted, want)
		}
	}
	if !strings.Contains(converted.Remediation, daemon.PidPath(home)) {
		t.Fatalf("remediation = %q, want pid path %q", converted.Remediation, daemon.PidPath(home))
	}
}

func TestIsDevstackCmdline(t *testing.T) {
	tests := []struct {
		name    string
		cmdline string
		want    bool
	}{
		{"daemon", "/usr/local/bin/devstack\x00daemon\x00run\x00", true},
		{"helper", "/usr/local/lib/devstack/devstack-helper\x00", true},
		{"foreign", "/usr/bin/python3\x00devstack.py\x00", false},
		{"empty", "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isDevstackCmdline([]byte(test.cmdline)); got != test.want {
				t.Fatalf("isDevstackCmdline(%q) = %v, want %v", test.cmdline, got, test.want)
			}
		})
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
	vhostStatus helperproto.VhostStatusResponse
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
	vhostStatus := f.vhostStatus
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
	case helperproto.PathVhostsStatus:
		if strings.TrimSpace(vhostStatus.Mode) == "" {
			vhostStatus.Mode = "builtin"
		}
		writeHelperJSON(w, http.StatusOK, vhostStatus)
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

func TestCLIVhostStatusAndUse(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{vhostStatus: helperproto.VhostStatusResponse{Mode: "builtin", ServerVersion: "1.26.1"}}
	out := runCLI(t, fake, "vhost", "status")
	if !strings.Contains(out, "builtin") || !strings.Contains(out, "1.26.1") {
		t.Fatalf("output = %q", out)
	}
	out = runCLI(t, fake, "vhost", "use", "nginx", "--yes")
	if !fake.servingModeSet || fake.servingMode != "nginx" {
		t.Fatal("mode not switched")
	}
	if !strings.Contains(out, "nginx") {
		t.Fatalf("output = %q, want the refreshed status", out)
	}
}

func TestCLIVhostStatusJSON(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{vhostStatus: helperproto.VhostStatusResponse{
		Mode:           "nginx",
		Server:         "nginx",
		ServerVersion:  "1.26.1",
		BridgePath:     "/etc/nginx/conf.d/devstack-vhosts.conf",
		IncludePresent: true,
		CurrentRelease: "/etc/devstack/vhosts/nginx/releases/20261022T214500Z",
		ConfigOK:       true,
		LastError:      "previous failure",
	}}
	out := runCLI(t, fake, "vhost", "status", "--json")
	var got helperproto.VhostStatusResponse
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v (%q)", err, out)
	}
	if got.Mode != "nginx" || got.ServerVersion != "1.26.1" || !got.IncludePresent || !got.ConfigOK {
		t.Fatalf("status = %+v", got)
	}
	if got.LastError != "previous failure" {
		t.Fatalf("last error = %q", got.LastError)
	}
}

func TestCLIVhostUseConfirmation(t *testing.T) {
	useTestSeams(t)

	t.Run("declined", func(t *testing.T) {
		fake := &fakeClient{}
		code, stdout, _ := runCLIExitInput(t, fake, "n\n", "vhost", "use", "nginx")
		if code == 0 {
			t.Fatal("want non-zero exit when the mode switch is declined")
		}
		if fake.servingModeSet {
			t.Fatal("mode was switched without confirmation")
		}
		for _, want := range []string{"80/443", "built-in proxy", "downtime"} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("confirmation output = %q, want it to mention %q", stdout, want)
			}
		}
	})

	t.Run("accepted", func(t *testing.T) {
		fake := &fakeClient{}
		code, _, _ := runCLIExitInput(t, fake, "y\n", "vhost", "use", "apache")
		if code != 0 {
			t.Fatalf("exit = %d", code)
		}
		if !fake.servingModeSet || fake.servingMode != "apache" {
			t.Fatalf("serving mode = %q (set %v)", fake.servingMode, fake.servingModeSet)
		}
	})

	t.Run("builtin warning", func(t *testing.T) {
		fake := &fakeClient{vhostStatus: helperproto.VhostStatusResponse{Mode: "nginx"}}
		code, stdout, _ := runCLIExitInput(t, fake, "n\n", "vhost", "use", "builtin")
		if code == 0 {
			t.Fatal("want non-zero exit when the mode switch is declined")
		}
		for _, want := range []string{"forwarders", "removes", "downtime"} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("confirmation output = %q, want it to mention %q", stdout, want)
			}
		}
	})

	t.Run("json requires yes", func(t *testing.T) {
		fake := &fakeClient{}
		code, stdout, stderr := runCLIExitInput(t, fake, "y\n", "vhost", "use", "nginx", "--json")
		if code == 0 {
			t.Fatal("want non-zero exit without --yes in JSON mode")
		}
		if fake.servingModeSet {
			t.Fatal("mode was switched without --yes")
		}
		if strings.TrimSpace(stdout) != "" {
			t.Fatalf("stdout = %q, want no JSON on failure", stdout)
		}
		if !strings.Contains(stderr, "CONFIRMATION_REQUIRED") || !strings.Contains(stderr, "--yes") {
			t.Fatalf("stderr = %q", stderr)
		}
	})

	t.Run("json with yes", func(t *testing.T) {
		fake := &fakeClient{}
		out := runCLI(t, fake, "vhost", "use", "apache", "--json", "--yes")
		var got helperproto.VhostStatusResponse
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("json: %v (%q)", err, out)
		}
		if got.Mode != "apache" {
			t.Fatalf("status = %+v", got)
		}
	})

	t.Run("invalid mode", func(t *testing.T) {
		fake := &fakeClient{}
		code, _, stderr := runCLIExit(t, fake, "vhost", "use", "caddy", "--yes")
		if code == 0 || fake.servingModeSet {
			t.Fatalf("code=%d set=%v", code, fake.servingModeSet)
		}
		if !strings.Contains(stderr, "INVALID_ARGUMENT") || !strings.Contains(stderr, "builtin, nginx, or apache") {
			t.Fatalf("stderr = %q", stderr)
		}
	})
}

func TestCLIVhostSync(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{vhostStatus: helperproto.VhostStatusResponse{
		Mode:           "nginx",
		Server:         "nginx",
		ServerVersion:  "1.26.1",
		IncludePresent: true,
		ConfigOK:       true,
	}}
	out := runCLI(t, fake, "vhost", "sync")
	if fake.vhostSyncs != 1 {
		t.Fatalf("sync calls = %d, want 1", fake.vhostSyncs)
	}
	if !strings.Contains(out, "nginx") || !strings.Contains(out, "1.26.1") {
		t.Fatalf("output = %q, want the refreshed status", out)
	}
}

func TestCLIVhostPreview(t *testing.T) {
	useTestSeams(t)
	const rendered = "server {\n    server_name blog.test;\n}\n"

	t.Run("defaults to the active external server", func(t *testing.T) {
		fake := &fakeClient{
			vhostStatus:    helperproto.VhostStatusResponse{Mode: "apache", Server: "apache"},
			previewContent: rendered,
		}
		out := runCLI(t, fake, "vhost", "preview", "blog")
		if fake.previewRef != "blog" || fake.previewServer != "apache" {
			t.Fatalf("preview = %q/%q", fake.previewRef, fake.previewServer)
		}
		if out != rendered {
			t.Fatalf("output = %q, want %q", out, rendered)
		}
	})

	t.Run("defaults to nginx in builtin mode", func(t *testing.T) {
		fake := &fakeClient{
			vhostStatus:    helperproto.VhostStatusResponse{Mode: "builtin"},
			previewContent: rendered,
		}
		_ = runCLI(t, fake, "vhost", "preview", "blog")
		if fake.previewServer != "nginx" {
			t.Fatalf("server = %q, want nginx", fake.previewServer)
		}
	})

	t.Run("flag overrides the active server", func(t *testing.T) {
		fake := &fakeClient{
			vhostStatus:    helperproto.VhostStatusResponse{Mode: "apache", Server: "apache"},
			previewContent: rendered,
		}
		_ = runCLI(t, fake, "vhost", "preview", "blog", "--server", "nginx")
		if fake.previewServer != "nginx" {
			t.Fatalf("server = %q, want nginx", fake.previewServer)
		}
	})

	t.Run("json", func(t *testing.T) {
		fake := &fakeClient{
			vhostStatus:    helperproto.VhostStatusResponse{Mode: "builtin"},
			previewContent: rendered,
		}
		out := runCLI(t, fake, "vhost", "preview", "blog", "--server", "apache", "--json")
		var got struct {
			Server  string `json:"server"`
			Site    string `json:"site"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("json: %v (%q)", err, out)
		}
		if got.Server != "apache" || got.Site != "blog" || got.Content != rendered {
			t.Fatalf("preview = %+v", got)
		}
	})

	t.Run("invalid server", func(t *testing.T) {
		fake := &fakeClient{}
		code, _, stderr := runCLIExit(t, fake, "vhost", "preview", "blog", "--server", "caddy")
		if code == 0 || fake.previewRef != "" {
			t.Fatalf("code=%d preview ref=%q", code, fake.previewRef)
		}
		if !strings.Contains(stderr, "INVALID_ARGUMENT") {
			t.Fatalf("stderr = %q", stderr)
		}
	})
}

func TestCLIVhostPath(t *testing.T) {
	useTestSeams(t)

	t.Run("builtin defaults", func(t *testing.T) {
		fake := &fakeClient{vhostStatus: helperproto.VhostStatusResponse{Mode: "builtin"}}
		out := runCLI(t, fake, "vhost", "path")
		for _, want := range []string{
			"/etc/devstack/vhosts/nginx/current",
			"/etc/devstack/vhosts/apache/current",
			"/etc/nginx/conf.d/devstack-vhosts.conf",
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("output = %q, want it to contain %q", out, want)
			}
		}
	})

	t.Run("external derives from the current release", func(t *testing.T) {
		fake := &fakeClient{vhostStatus: helperproto.VhostStatusResponse{
			Mode:           "nginx",
			Server:         "nginx",
			CurrentRelease: "/opt/devstack/vhosts/nginx/releases/20261022T214500Z",
			BridgePath:     "/opt/nginx/conf.d/devstack-vhosts.conf",
		}}
		out := runCLI(t, fake, "vhost", "path", "--json")
		var got struct {
			Mode          string `json:"mode"`
			Server        string `json:"server"`
			NginxCurrent  string `json:"nginx_current"`
			ApacheCurrent string `json:"apache_current"`
			Bridge        string `json:"bridge"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("json: %v (%q)", err, out)
		}
		if got.Mode != "nginx" || got.Server != "nginx" {
			t.Fatalf("paths = %+v", got)
		}
		if got.NginxCurrent != "/opt/devstack/vhosts/nginx/current" {
			t.Fatalf("nginx current = %q", got.NginxCurrent)
		}
		if got.ApacheCurrent != "/etc/devstack/vhosts/apache/current" {
			t.Fatalf("apache current = %q", got.ApacheCurrent)
		}
		if got.Bridge != "/opt/nginx/conf.d/devstack-vhosts.conf" {
			t.Fatalf("bridge = %q", got.Bridge)
		}
	})
}

func TestCLIVhostErrorRemediations(t *testing.T) {
	useTestSeams(t)
	tests := []struct {
		code string
		want string
	}{
		{"SERVING_MODE_UNAVAILABLE", "nginx or Apache"},
		{"VHOST_CONFIG_INVALID", "vhost sync"},
		{"VHOST_SYNC_FAILED", "vhost sync"},
		{"VHOST_INCLUDE_MISSING", "bridge"},
		{"SERVER_RELOAD_FAILED", "restart the web server"},
		{"VHOST_SYNC_BUSY", "Wait"},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			fake := &fakeClient{err: &client.Error{Code: tt.code, Message: "vhost failure"}}
			code, stdout, stderr := runCLIExit(t, fake, "vhost", "status")
			if code == 0 {
				t.Fatal("want non-zero exit")
			}
			if strings.TrimSpace(stdout) != "" {
				t.Fatalf("stdout = %q", stdout)
			}
			if !strings.Contains(stderr, tt.code) || !strings.Contains(stderr, "Remediation:") || !strings.Contains(stderr, tt.want) {
				t.Fatalf("stderr = %q, want code %s and %q", stderr, tt.code, tt.want)
			}
		})
	}
}
