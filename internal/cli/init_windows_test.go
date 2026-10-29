//go:build windows

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentclient"
	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/daemon"
	"github.com/AramZahedi/dev-web-stack/internal/doctor"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/securestore"
	"github.com/AramZahedi/dev-web-stack/internal/wsl"
)

type mockWSLRunner struct {
	mu           sync.Mutex
	calls        [][]string
	distros      []string
	managedJSON  string
	managedErr   error
	initProc     string
	initProcErr  error
	testRErr     error
	transferErr  error
	systemctlErr error
	probeOut     string
	probeErr     error
}

func (m *mockWSLRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, append([]string(nil), args...))

	cmdStr := strings.Join(args, " ")
	switch {
	case cmdStr == "--list --quiet":
		return []byte(strings.Join(m.distros, "\n")), nil
	case len(args) >= 5 && args[0] == "-d" && args[2] == "--" && args[3] == "cat" && args[4] == "/data/devstack/managed.json":
		if m.managedErr != nil {
			return nil, m.managedErr
		}
		return []byte(m.managedJSON), nil
	case len(args) >= 5 && args[0] == "-d" && args[2] == "--" && args[3] == "cat" && args[4] == "/proc/1/comm":
		if m.initProcErr != nil {
			return nil, m.initProcErr
		}
		return []byte(m.initProc), nil
	case strings.Contains(cmdStr, "test -s") || strings.Contains(cmdStr, "test -r"):
		if m.testRErr != nil {
			return nil, m.testRErr
		}
		return []byte("ok"), nil
	case strings.Contains(cmdStr, "devstack-transfer"):
		if m.transferErr != nil {
			return nil, m.transferErr
		}
		return []byte("transferred"), nil
	case strings.Contains(cmdStr, "systemctl restart devstack-agent.service"):
		if m.systemctlErr != nil {
			return nil, m.systemctlErr
		}
		return []byte("restarted"), nil
	case strings.Contains(cmdStr, "devstack-probe"):
		if m.probeErr != nil {
			return nil, m.probeErr
		}
		return []byte(m.probeOut), nil
	}
	return []byte("ok"), nil
}

func (m *mockWSLRunner) getCalls() [][]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := make([][]string, len(m.calls))
	for i, c := range m.calls {
		copied[i] = append([]string(nil), c...)
	}
	return copied
}

type mockAgentClient struct {
	mu           sync.Mutex
	healthCalls  int
	healthErr    error
	caPEM        []byte
	installCAErr error
	hostsCalls   []agentproto.HostsRequest
	syncHostsErr error
}

func (m *mockAgentClient) Health(_ context.Context) (agentproto.HealthResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.healthCalls++
	if m.healthErr != nil {
		return agentproto.HealthResponse{}, m.healthErr
	}
	return agentproto.HealthResponse{Distro: "devstack", Version: "dev"}, nil
}

func (m *mockAgentClient) InstallCA(_ context.Context, pem []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.caPEM = append([]byte(nil), pem...)
	return m.installCAErr
}

func (m *mockAgentClient) SyncHosts(_ context.Context, req agentproto.HostsRequest) (agentproto.HostsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hostsCalls = append(m.hostsCalls, req)
	if m.syncHostsErr != nil {
		return agentproto.HostsResponse{}, m.syncHostsErr
	}
	return agentproto.HostsResponse{Changed: true}, nil
}

// Satisfy agentclient.API
func (m *mockAgentClient) Version(context.Context) (agentproto.VersionResponse, error) {
	return agentproto.VersionResponse{}, nil
}
func (m *mockAgentClient) Capabilities(context.Context) (agentproto.CapabilitiesResponse, error) {
	return agentproto.CapabilitiesResponse{}, nil
}
func (m *mockAgentClient) AllocatePorts(context.Context, agentproto.PortRequest) (agentproto.PortResponse, error) {
	return agentproto.PortResponse{}, nil
}
func (m *mockAgentClient) ReleasePorts(context.Context, agentproto.PortRequest) error {
	return nil
}
func (m *mockAgentClient) ValidatePorts(context.Context, agentproto.PortRequest) (agentproto.PortValidateResponse, error) {
	return agentproto.PortValidateResponse{}, nil
}
func (m *mockAgentClient) PrepareSite(context.Context, agentproto.SitePrepareRequest) (agentproto.SitePrepareResponse, error) {
	return agentproto.SitePrepareResponse{}, nil
}
func (m *mockAgentClient) StartSite(context.Context, string, agentproto.SiteActionRequest) (agentproto.SiteActionResponse, error) {
	return agentproto.SiteActionResponse{}, nil
}
func (m *mockAgentClient) StopSite(context.Context, string, agentproto.SiteActionRequest) (agentproto.SiteActionResponse, error) {
	return agentproto.SiteActionResponse{}, nil
}
func (m *mockAgentClient) SiteStatus(context.Context, string) (agentproto.SiteStatusResponse, error) {
	return agentproto.SiteStatusResponse{}, nil
}
func (m *mockAgentClient) SiteLogs(context.Context, string, agentproto.SiteLogsRequest) (io.ReadCloser, error) {
	return nil, nil
}
func (m *mockAgentClient) ExecSite(context.Context, string, agentproto.SiteExecRequest) (agentproto.SiteExecResponse, error) {
	return agentproto.SiteExecResponse{}, nil
}
func (m *mockAgentClient) PreparePHP(context.Context, string, agentproto.PHPPrepareRequest) (agentproto.SitePrepareResponse, error) {
	return agentproto.SitePrepareResponse{}, nil
}
func (m *mockAgentClient) StartPHP(context.Context, string, agentproto.PHPActionRequest) (agentproto.SiteActionResponse, error) {
	return agentproto.SiteActionResponse{}, nil
}
func (m *mockAgentClient) StopPHP(context.Context, string, agentproto.PHPActionRequest) (agentproto.SiteActionResponse, error) {
	return agentproto.SiteActionResponse{}, nil
}
func (m *mockAgentClient) BuildSite(context.Context, string, agentproto.BuildRequest) (agentproto.BuildResponse, error) {
	return agentproto.BuildResponse{}, nil
}
func (m *mockAgentClient) TerminalTicket(context.Context, string, agentproto.TerminalTicketRequest) (agentproto.TerminalTicketResponse, error) {
	return agentproto.TerminalTicketResponse{}, nil
}
func (m *mockAgentClient) InstallRuntime(context.Context, agentproto.RuntimeRequest) (agentproto.RuntimeResponse, error) {
	return agentproto.RuntimeResponse{}, nil
}
func (m *mockAgentClient) RemoveRuntime(context.Context, agentproto.RuntimeRequest) error {
	return nil
}
func (m *mockAgentClient) ListRuntimes(context.Context) (agentproto.RuntimeListResponse, error) {
	return agentproto.RuntimeListResponse{}, nil
}
func (m *mockAgentClient) ServiceAction(context.Context, agentproto.ServiceRequest) (agentproto.ServiceResponse, error) {
	return agentproto.ServiceResponse{}, nil
}
func (m *mockAgentClient) ServiceStatus(context.Context, string) (agentproto.ServiceResponse, error) {
	return agentproto.ServiceResponse{}, nil
}
func (m *mockAgentClient) ServiceLogs(context.Context, string, agentproto.ServiceLogsRequest) (agentproto.ServiceLogsResponse, error) {
	return agentproto.ServiceLogsResponse{}, nil
}
func (m *mockAgentClient) ServiceConfig(context.Context, string) (agentproto.ServiceConfigResponse, error) {
	return agentproto.ServiceConfigResponse{}, nil
}
func (m *mockAgentClient) ServiceContent(context.Context, agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error) {
	return agentproto.ServiceContentResponse{}, nil
}
func (m *mockAgentClient) ServiceApply(context.Context, agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error) {
	return agentproto.ServiceContentResponse{}, nil
}
func (m *mockAgentClient) ServiceReset(context.Context, agentproto.ServiceConfigRequest) error {
	return nil
}
func (m *mockAgentClient) ServiceCredential(context.Context, agentproto.ServiceCredentialRequest) (agentproto.ServiceCredentialResponse, error) {
	return agentproto.ServiceCredentialResponse{}, nil
}
func (m *mockAgentClient) WebTargets(context.Context, agentproto.WebTargetsRequest) error {
	return nil
}
func (m *mockAgentClient) SyncWeb(context.Context, agentproto.WebTargetsRequest) (agentproto.WebSyncResponse, error) {
	return agentproto.WebSyncResponse{}, nil
}
func (m *mockAgentClient) EnsureCert(context.Context, agentproto.CertRequest) (agentproto.CertResponse, error) {
	return agentproto.CertResponse{}, nil
}
func (m *mockAgentClient) CA(context.Context) (agentproto.CAResponse, error) {
	return agentproto.CAResponse{}, nil
}
func (m *mockAgentClient) ValidatePath(context.Context, agentproto.PathValidateRequest) (agentproto.PathValidateResponse, error) {
	return agentproto.PathValidateResponse{}, nil
}
func (m *mockAgentClient) CopySource(context.Context, agentproto.SourceCopyRequest) (agentproto.SourceCopyResponse, error) {
	return agentproto.SourceCopyResponse{}, nil
}

func setupWindowsInitTestEnv(t *testing.T) (*mockWSLRunner, *mockAgentClient, *fakeHelper, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("DEVSTACK_HOME", home)

	fake, socket := startFakeHelper(t)

	runner := &mockWSLRunner{
		distros:     []string{"devstack", "docker-desktop"},
		managedJSON: `{"managed_by":"devstack","install_id":"test-install-123"}`,
		initProc:    "systemd\n",
		testRErr:    errors.New("not found"), // trigger credentials generation by default
		probeOut:    "ok\n",
	}

	agentClient := &mockAgentClient{}

	prevRunner := wslRunnerFactory
	prevAgentClient := newAgentClient
	prevEnsureRunning := ensureRunning

	t.Cleanup(func() {
		wslRunnerFactory = prevRunner
		newAgentClient = prevAgentClient
		ensureRunning = prevEnsureRunning
	})

	wslRunnerFactory = func() wsl.Runner {
		return runner
	}
	newAgentClient = func(cfg agentclient.Config) (agentclient.API, error) {
		return agentClient, nil
	}
	ensureRunning = func(_ context.Context, _ daemon.Options) error {
		return nil
	}

	return runner, agentClient, fake, home, socket
}

func TestEnsureAgentCredentialsUsesResolvedHomeNotEnvironment(t *testing.T) {
	home := t.TempDir()
	other := t.TempDir()
	t.Setenv("DEVSTACK_HOME", other)
	runner := &mockWSLRunner{testRErr: errors.New("not found")}
	token, pin, err := ensureAgentCredentials(context.Background(), runner, "devstack", home, config.Config{})
	if err != nil {
		t.Fatalf("ensureAgentCredentials: %v", err)
	}
	if len(token) != 64 || len(pin) != 64 {
		t.Fatalf("token/pin lengths = %d/%d", len(token), len(pin))
	}
	storedToken, err := securestore.LoadAt(home, "agent-token")
	if err != nil {
		t.Fatalf("agent-token was not stored under the resolved home: %v", err)
	}
	if string(storedToken) != token {
		t.Fatal("stored token does not match the generated token")
	}
	if _, err := securestore.LoadAt(other, "agent-token"); !os.IsNotExist(err) {
		t.Fatalf("agent-token leaked into DEVSTACK_HOME instead of --home: %v", err)
	}
}

func TestWindowsInitSuccess(t *testing.T) {
	runner, agentClient, helper, home, socket := setupWindowsInitTestEnv(t)

	fake := &fakeClient{
		sites: []model.Site{{Name: "shop", Domain: "shop.test", WWW: true}},
		doctorReport: doctor.Report{OK: true, Checks: []doctor.Check{{
			ID: "helper", Title: "Helper", Status: doctor.StatusOK, Detail: "ready",
		}}},
	}

	out := runCLI(t, fake, "init", "--home", home, "--helper-socket", socket, "--json")

	var result initResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal init result: %v\nOutput: %s", err, out)
	}

	if result.CAFingerprint != "test-fingerprint" {
		t.Fatalf("fingerprint = %q, want test-fingerprint", result.CAFingerprint)
	}

	// 1. Verify DPAPI securestore saved token and pin
	tok, err := securestore.Load("agent-token")
	if err != nil {
		t.Fatalf("load agent-token from securestore: %v", err)
	}
	if len(tok) != 64 { // 32 bytes hex = 64 hex chars
		t.Fatalf("token length = %d, want 64", len(tok))
	}
	pin, err := securestore.Load("agent-pin")
	if err != nil {
		t.Fatalf("load agent-pin from securestore: %v", err)
	}
	if len(pin) != 64 { // SHA-256 hex = 64 hex chars
		t.Fatalf("pin length = %d, want 64", len(pin))
	}

	// 2. Verify WSL runner commands
	calls := runner.getCalls()
	foundTransfer := false
	foundRestart := false
	foundProbe := false
	for _, call := range calls {
		cmd := strings.Join(call, " ")
		if strings.Contains(cmd, "devstack-transfer") {
			foundTransfer = true
		}
		if strings.Contains(cmd, "systemctl restart devstack-agent.service") {
			foundRestart = true
		}
		if strings.Contains(cmd, "devstack-probe") {
			foundProbe = true
		}
	}
	if !foundTransfer {
		t.Fatalf("wsl transfer was not called: calls=%#v", calls)
	}
	if !foundRestart {
		t.Fatalf("systemctl restart was not called: calls=%#v", calls)
	}
	if !foundProbe {
		t.Fatalf("probe was not called: calls=%#v", calls)
	}

	// 3. Verify Agent client CA installation
	if !reflect.DeepEqual(agentClient.caPEM, []byte("test-root-pem")) {
		t.Fatalf("agent CA PEM = %q, want 'test-root-pem'", string(agentClient.caPEM))
	}

	// 4. Verify Dual-hosts synchronization on both helper and agent client
	wantHosts := []string{"devstack.test", "shop.test", "www.shop.test"}
	for _, domain := range wantHosts {
		if !contains(result.Hosts, domain) {
			t.Fatalf("result.Hosts missing %s: got %v", domain, result.Hosts)
		}
	}

	// Check agent client SyncHosts calls
	if len(agentClient.hostsCalls) < 2 {
		t.Fatalf("agentClient.hostsCalls len = %d, want >= 2", len(agentClient.hostsCalls))
	}
	lastCall := agentClient.hostsCalls[len(agentClient.hostsCalls)-1]
	for _, domain := range wantHosts {
		if !contains(lastCall.Domains, domain) {
			t.Fatalf("agent hosts call missing %s: got %v", domain, lastCall.Domains)
		}
	}

	// Helper calls
	helperCalls := helper.snapshot()
	syncHostsCount := 0
	for _, c := range helperCalls {
		if c.Path == helperproto.PathHostsSync {
			syncHostsCount++
		}
	}
	if syncHostsCount < 2 {
		t.Fatalf("helper hosts sync count = %d, want >= 2", syncHostsCount)
	}
}

func TestWindowsInitDistroMissing(t *testing.T) {
	runner, _, _, home, socket := setupWindowsInitTestEnv(t)
	runner.distros = []string{"Ubuntu", "docker-desktop"} // devstack missing

	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "init", "--home", home, "--helper-socket", socket)
	if code == 0 {
		t.Fatal("expected init to fail when distro is missing, got exit 0")
	}
	if !strings.Contains(stderr, "WSL_DISTRO_MISSING") && !strings.Contains(stderr, "devstack") {
		t.Fatalf("expected error mentioning WSL_DISTRO_MISSING or devstack, got: %s", stderr)
	}
}

func TestWindowsInitDistroUnmanaged(t *testing.T) {
	runner, _, _, home, socket := setupWindowsInitTestEnv(t)
	runner.managedErr = errors.New("cat: /data/devstack/managed.json: No such file or directory")

	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "init", "--home", home, "--helper-socket", socket)
	if code == 0 {
		t.Fatal("expected init to fail when distro is unmanaged, got exit 0")
	}
	if !strings.Contains(stderr, "WSL_DISTRO_UNMANAGED") && !strings.Contains(stderr, "managed") {
		t.Fatalf("expected error mentioning WSL_DISTRO_UNMANAGED, got: %s", stderr)
	}
}

func TestWindowsInitSystemdRequired(t *testing.T) {
	runner, _, _, home, socket := setupWindowsInitTestEnv(t)
	runner.initProc = "init\n" // Not systemd

	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "init", "--home", home, "--helper-socket", socket)
	if code == 0 {
		t.Fatal("expected init to fail when systemd is not PID 1, got exit 0")
	}
	if !strings.Contains(stderr, "WSL_SYSTEMD_REQUIRED") && !strings.Contains(stderr, "systemd") {
		t.Fatalf("expected error mentioning WSL_SYSTEMD_REQUIRED, got: %s", stderr)
	}
}

func TestWindowsInitCredentialsReusedWhenPresent(t *testing.T) {
	runner, _, _, home, socket := setupWindowsInitTestEnv(t)

	// Pre-populate securestore
	existingToken := strings.Repeat("a", 64)
	existingPin := strings.Repeat("b", 64)
	if err := securestore.Save("agent-token", []byte(existingToken)); err != nil {
		t.Fatalf("save token: %v", err)
	}
	if err := securestore.Save("agent-pin", []byte(existingPin)); err != nil {
		t.Fatalf("save pin: %v", err)
	}

	// Runner returns ok for test -r (files exist in WSL)
	runner.testRErr = nil

	fake := &fakeClient{
		doctorReport: doctor.Report{OK: true},
	}

	out := runCLI(t, fake, "init", "--home", home, "--helper-socket", socket, "--json")
	var result initResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Verify transfer was skipped
	for _, call := range runner.getCalls() {
		cmd := strings.Join(call, " ")
		if strings.Contains(cmd, "devstack-transfer") {
			t.Fatal("expected file transfer to be skipped when credentials already present in securestore and WSL")
		}
	}
}

func TestWindowsInitTransferError(t *testing.T) {
	runner, _, _, home, socket := setupWindowsInitTestEnv(t)
	runner.transferErr = errors.New("disk full")

	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "init", "--home", home, "--helper-socket", socket)
	if code == 0 {
		t.Fatal("expected init to fail on transfer error, got exit 0")
	}
	if !strings.Contains(stderr, "disk full") && !strings.Contains(stderr, "transfer") {
		t.Fatalf("expected transfer error in stderr, got: %s", stderr)
	}
}

func TestWindowsInitLocalhostForwardingError(t *testing.T) {
	runner, _, _, home, socket := setupWindowsInitTestEnv(t)
	runner.probeErr = errors.New("timeout connecting to gateway")

	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "init", "--home", home, "--helper-socket", socket)
	if code == 0 {
		t.Fatal("expected init to fail when localhost forwarding probe fails, got exit 0")
	}
	if !strings.Contains(stderr, "WSL_LOCALHOST_FORWARDING_UNAVAILABLE") && !strings.Contains(stderr, "forwarding") {
		t.Fatalf("expected WSL_LOCALHOST_FORWARDING_UNAVAILABLE in stderr, got: %s", stderr)
	}
}

func TestWindowsInitDistroQueryFailed(t *testing.T) {
	runner, _, _, home, socket := setupWindowsInitTestEnv(t)
	runner.distros = nil
	runner.managedErr = errors.New("wsl.exe execution failed")
	// Make Run fail on --list --quiet
	prevRunner := wslRunnerFactory
	t.Cleanup(func() { wslRunnerFactory = prevRunner })
	wslRunnerFactory = func() wsl.Runner {
		return &mockWSLRunner{
			calls:    [][]string{},
			distros:  []string{},
			probeErr: errors.New("cannot list"),
		}
	}
	// Better yet, mock runner that fails on --list --quiet:
	failingRunner := &failingWSLRunner{err: errors.New("wsl service not running")}
	wslRunnerFactory = func() wsl.Runner { return failingRunner }

	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "init", "--home", home, "--helper-socket", socket)
	if code == 0 {
		t.Fatal("expected init to fail on query error, got exit 0")
	}
	if !strings.Contains(stderr, "WSL_DISTRO_QUERY_FAILED") {
		t.Fatalf("expected WSL_DISTRO_QUERY_FAILED in stderr, got: %s", stderr)
	}
}

type failingWSLRunner struct {
	err error
}

func (f *failingWSLRunner) Run(_ context.Context, _ ...string) ([]byte, error) {
	return nil, f.err
}

func TestWindowsInitServiceRestartError(t *testing.T) {
	runner, _, _, home, socket := setupWindowsInitTestEnv(t)
	runner.systemctlErr = errors.New("systemctl unit failed")

	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "init", "--home", home, "--helper-socket", socket)
	if code == 0 {
		t.Fatal("expected init to fail when systemctl restart fails, got exit 0")
	}
	if !strings.Contains(stderr, "restart devstack-agent.service") {
		t.Fatalf("expected restart error in stderr, got: %s", stderr)
	}
}

func TestWindowsInitAgentHealthTimeout(t *testing.T) {
	_, agentClient, _, home, socket := setupWindowsInitTestEnv(t)
	agentClient.healthErr = errors.New("connection refused")

	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "init", "--home", home, "--helper-socket", socket)
	if code == 0 {
		t.Fatal("expected init to fail when agent client is unavailable, got exit 0")
	}
	if !strings.Contains(stderr, "AGENT_UNAVAILABLE") && !strings.Contains(stderr, "connection refused") {
		t.Fatalf("expected AGENT_UNAVAILABLE in stderr, got: %s", stderr)
	}
}

func TestWindowsInitAgentInstallCAError(t *testing.T) {
	_, agentClient, _, home, socket := setupWindowsInitTestEnv(t)
	agentClient.installCAErr = errors.New("failed to write CA")

	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "init", "--home", home, "--helper-socket", socket)
	if code == 0 {
		t.Fatal("expected init to fail on InstallCA error, got exit 0")
	}
	if !strings.Contains(stderr, "AGENT_CA_INSTALL_FAILED") && !strings.Contains(stderr, "failed to write CA") {
		t.Fatalf("expected AGENT_CA_INSTALL_FAILED in stderr, got: %s", stderr)
	}
}

func TestWindowsInitAgentSyncHostsError(t *testing.T) {
	_, agentClient, _, home, socket := setupWindowsInitTestEnv(t)
	agentClient.syncHostsErr = errors.New("failed to update /etc/hosts")

	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "init", "--home", home, "--helper-socket", socket)
	if code == 0 {
		t.Fatal("expected init to fail on SyncHosts error, got exit 0")
	}
	if !strings.Contains(stderr, "WSL_HOSTS_SYNC_FAILED") && !strings.Contains(stderr, "failed to update /etc/hosts") {
		t.Fatalf("expected WSL_HOSTS_SYNC_FAILED in stderr, got: %s", stderr)
	}
}

func TestWindowsInitCredentialsRegeneratedIfWSLFilesMissing(t *testing.T) {
	runner, _, _, home, socket := setupWindowsInitTestEnv(t)

	// Pre-populate securestore with an old token
	oldToken := strings.Repeat("c", 64)
	oldPin := strings.Repeat("d", 64)
	if err := securestore.Save("agent-token", []byte(oldToken)); err != nil {
		t.Fatalf("save token: %v", err)
	}
	if err := securestore.Save("agent-pin", []byte(oldPin)); err != nil {
		t.Fatalf("save pin: %v", err)
	}

	// But WSL file test -r returns error (missing)
	runner.testRErr = errors.New("missing in WSL")

	fake := &fakeClient{
		doctorReport: doctor.Report{OK: true},
	}

	out := runCLI(t, fake, "init", "--home", home, "--helper-socket", socket, "--json")
	var result initResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Transfer should have happened
	foundTransfer := false
	for _, call := range runner.getCalls() {
		cmd := strings.Join(call, " ")
		if strings.Contains(cmd, "devstack-transfer") {
			foundTransfer = true
			break
		}
	}
	if !foundTransfer {
		t.Fatal("expected file transfer when WSL files were missing despite securestore credentials")
	}

	// Securestore should have been refreshed
	newTok, err := securestore.Load("agent-token")
	if err != nil {
		t.Fatalf("load token: %v", err)
	}
	if string(newTok) == oldToken {
		t.Fatal("expected agent-token to be regenerated, but old token remains")
	}
}
