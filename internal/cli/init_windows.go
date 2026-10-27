//go:build windows

package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/AramZahedi/dev-web-stack/internal/agent"
	"github.com/AramZahedi/dev-web-stack/internal/agentclient"
	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/securestore"
	"github.com/AramZahedi/dev-web-stack/internal/wsl"
)

var (
	wslRunnerFactory = func() wsl.Runner {
		return wsl.ExecRunner{}
	}
	newAgentClient = func(cfg agentclient.Config) (agentclient.API, error) {
		return agentclient.New(cfg)
	}
	generateTLSIdentity = agent.GenerateTLSIdentity
	securestoreSave     = securestore.Save
	securestoreLoad     = securestore.Load
)

type platformInitContext struct {
	agentClient agentclient.API
	runner      wsl.Runner
	distro      string
}

const transferScript = `# devstack-transfer
set -eu
mkdir -p /data/devstack/agent-tls
chmod 0700 /data/devstack/agent-tls
chown root:root /data/devstack/agent-tls
printf '%%s' '%s' | base64 -d > /data/devstack/agent.token
chmod 0600 /data/devstack/agent.token
chown root:root /data/devstack/agent.token
printf '%%s' '%s' | base64 -d > /data/devstack/agent-tls/agent.crt
chmod 0644 /data/devstack/agent-tls/agent.crt
chown root:root /data/devstack/agent-tls/agent.crt
printf '%%s' '%s' | base64 -d > /data/devstack/agent-tls/agent.key
chmod 0600 /data/devstack/agent-tls/agent.key
chown root:root /data/devstack/agent-tls/agent.key
printf '%%s' '%s' | base64 -d > /data/devstack/agent.yaml
chmod 0600 /data/devstack/agent.yaml
chown root:root /data/devstack/agent.yaml
`

func initPlatformPreDaemon(ctx context.Context, home string, cfg config.Config, ca helperproto.EnsureCAResponse) (*platformInitContext, error) {
	distroName := strings.TrimSpace(cfg.WSLDistro)
	if distroName == "" {
		distroName = "devstack"
	}
	runner := wslRunnerFactory()

	// 1. Verify managed WSL distro exists and is running
	listOut, err := runner.Run(ctx, "--list", "--quiet")
	if err != nil {
		return nil, wrapError(fmt.Errorf("query WSL distros: %w", err), "WSL_DISTRO_QUERY_FAILED", "Ensure WSL is installed and working, then rerun `devstack init`.")
	}
	found := false
	for _, name := range wsl.ParseDistroList(listOut) {
		if strings.EqualFold(name, distroName) {
			found = true
			break
		}
	}
	if !found {
		return nil, &Error{
			Code:        "WSL_DISTRO_MISSING",
			Message:     fmt.Sprintf("managed WSL distribution %q not found", distroName),
			Remediation: "Run install.ps1 to provision the devstack WSL distribution.",
		}
	}

	// Verify managed.json exists and has valid devstack marker
	markerOut, err := runner.Run(ctx, "-d", distroName, "--", "cat", "/data/devstack/managed.json")
	if err != nil {
		return nil, &Error{
			Code:        "WSL_DISTRO_UNMANAGED",
			Message:     fmt.Sprintf("WSL distribution %q is not marked as managed by devstack", distroName),
			Remediation: "Ensure the devstack WSL distro was imported by install.ps1 with /data/devstack/managed.json.",
		}
	}
	var marker struct {
		ManagedBy string `json:"managed_by"`
		InstallID string `json:"install_id"`
	}
	if err := json.Unmarshal(markerOut, &marker); err != nil || marker.ManagedBy != "devstack" {
		return nil, &Error{
			Code:        "WSL_DISTRO_UNMANAGED",
			Message:     fmt.Sprintf("WSL distribution %q has invalid managed.json marker", distroName),
			Remediation: "Ensure /data/devstack/managed.json contains managed_by: devstack.",
		}
	}

	// Verify systemd as PID 1
	initProcOut, err := runner.Run(ctx, "-d", distroName, "--", "cat", "/proc/1/comm")
	if err != nil || !strings.Contains(strings.TrimSpace(string(initProcOut)), "systemd") {
		return nil, &Error{
			Code:        "WSL_SYSTEMD_REQUIRED",
			Message:     fmt.Sprintf("WSL distribution %q must run systemd as PID 1", distroName),
			Remediation: "Ensure /etc/wsl.conf has [boot] systemd=true and restart the distro.",
		}
	}

	// 2. Check / initialize agent credentials
	token, pin, err := ensureAgentCredentials(ctx, runner, distroName, home, cfg)
	if err != nil {
		return nil, err
	}

	// 3. Connect agent client
	agentPort := cfg.AgentPort
	if agentPort == 0 {
		agentPort = 7443
	}
	baseURL := fmt.Sprintf("https://127.0.0.1:%d", agentPort)
	client, err := newAgentClient(agentclient.Config{
		BaseURL:   baseURL,
		Token:     token,
		PinSHA256: pin,
		Timeout:   5 * time.Second,
	})
	if err != nil {
		return nil, wrapError(fmt.Errorf("configure WSL agent client: %w", err), "AGENT_CONFIG_INVALID", "Review agent port configuration in config.yaml.")
	}

	// Ensure agent is reachable
	var lastHealthErr error
	for attempt := 0; attempt < 20; attempt++ {
		healthCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, lastHealthErr = client.Health(healthCtx)
		cancel()
		if lastHealthErr == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if lastHealthErr != nil {
		return nil, wrapError(fmt.Errorf("connect to WSL agent at %s: %w", baseURL, lastHealthErr), "AGENT_UNAVAILABLE", "Check `devstack-agent.service` status in WSL with `wsl -d devstack systemctl status devstack-agent`.")
	}

	// 4. Push helper root CA into WSL agent
	if len(ca.RootPEM) > 0 {
		if err := client.InstallCA(ctx, ca.RootPEM); err != nil {
			return nil, wrapError(fmt.Errorf("install root CA into WSL agent: %w", err), "AGENT_CA_INSTALL_FAILED", "Inspect agent logs for CA installation errors.")
		}
	}

	return &platformInitContext{
		agentClient: client,
		runner:      runner,
		distro:      distroName,
	}, nil
}

func ensureAgentCredentials(ctx context.Context, runner wsl.Runner, distroName, home string, cfg config.Config) (string, string, error) {
	// Sync DEVSTACK_HOME so securestore looks in the correct home directory
	if os.Getenv("DEVSTACK_HOME") == "" {
		_ = os.Setenv("DEVSTACK_HOME", home)
	}

	tokenBytes, errToken := securestoreLoad("agent-token")
	pinBytes, errPin := securestoreLoad("agent-pin")

	hasSecurestore := errToken == nil && errPin == nil && len(tokenBytes) > 0 && len(pinBytes) > 0

	hasWSLFiles := false
	if hasSecurestore {
		// Check if required files exist in WSL and are non-empty
		_, errT := runner.Run(ctx, "-d", distroName, "--user", "root", "--", "test", "-s", "/data/devstack/agent.token")
		_, errK := runner.Run(ctx, "-d", distroName, "--user", "root", "--", "test", "-s", "/data/devstack/agent-tls/agent.key")
		_, errY := runner.Run(ctx, "-d", distroName, "--user", "root", "--", "test", "-s", "/data/devstack/agent.yaml")
		if errT == nil && errK == nil && errY == nil {
			hasWSLFiles = true
		}
	}

	if hasSecurestore && hasWSLFiles {
		return string(tokenBytes), string(pinBytes), nil
	}

	// Generate agent bearer token (32 random bytes = 64 hex characters)
	randBytes := make([]byte, 32)
	if _, err := rand.Read(randBytes); err != nil {
		return "", "", fmt.Errorf("generate agent bearer token: %w", err)
	}
	token := hex.EncodeToString(randBytes)

	// Generate agent TLS keypair
	tempDir, err := os.MkdirTemp("", "devstack-agent-tls-*")
	if err != nil {
		return "", "", fmt.Errorf("create temporary TLS directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	identity, err := generateTLSIdentity(tempDir)
	if err != nil {
		return "", "", fmt.Errorf("generate agent TLS identity: %w", err)
	}

	// Save to Windows DPAPI securestore
	if err := securestoreSave("agent-token", []byte(token)); err != nil {
		return "", "", fmt.Errorf("save agent-token to securestore: %w", err)
	}
	if err := securestoreSave("agent-pin", []byte(identity.Pin)); err != nil {
		return "", "", fmt.Errorf("save agent-pin to securestore: %w", err)
	}

	// Prepare agent.yaml
	agentPort := cfg.AgentPort
	if agentPort == 0 {
		agentPort = 7443
	}
	agentCfg := agent.Config{
		Root:        "/data/devstack",
		Distro:      distroName,
		Version:     "dev",
		Port:        agentPort,
		TokenFile:   "/data/devstack/agent.token",
		TLSDir:      "/data/devstack/agent-tls",
		TLSCertFile: "/data/devstack/agent-tls/agent.crt",
		TLSKeyFile:  "/data/devstack/agent-tls/agent.key",
	}
	agentYAML, err := yaml.Marshal(agentCfg)
	if err != nil {
		return "", "", fmt.Errorf("encode agent.yaml: %w", err)
	}

	// Transfer into WSL /data/devstack/
	b64Token := base64.StdEncoding.EncodeToString([]byte(token))
	b64Cert := base64.StdEncoding.EncodeToString(identity.CertPEM)
	b64Key := base64.StdEncoding.EncodeToString(identity.KeyPEM)
	b64YAML := base64.StdEncoding.EncodeToString(agentYAML)

	transferCmd := fmt.Sprintf(transferScript, b64Token, b64Cert, b64Key, b64YAML)
	if _, err := runner.Run(ctx, "-d", distroName, "--user", "root", "--", "sh", "-c", transferCmd); err != nil {
		return "", "", fmt.Errorf("transfer agent credentials to WSL: %w", err)
	}

	// Restart devstack-agent.service
	if _, err := runner.Run(ctx, "-d", distroName, "--user", "root", "--", "systemctl", "restart", "devstack-agent.service"); err != nil {
		return "", "", fmt.Errorf("restart devstack-agent.service: %w", err)
	}

	return token, identity.Pin, nil
}

func initPlatformSyncHosts(ctx context.Context, pctx *platformInitContext, domains []string) error {
	if pctx == nil || pctx.agentClient == nil {
		return nil
	}
	if _, err := pctx.agentClient.SyncHosts(ctx, agentproto.HostsRequest{Domains: domains}); err != nil {
		return wrapError(fmt.Errorf("sync WSL hosts: %w", err), "WSL_HOSTS_SYNC_FAILED", "Ensure devstack-agent is running inside WSL.")
	}
	return nil
}

func initPlatformPostDaemon(ctx context.Context, pctx *platformInitContext, cfg config.Config) error {
	if pctx == nil || pctx.runner == nil {
		return nil
	}
	port := cfg.HTTPSPort
	if port == 0 {
		port = 8443
	}
	if err := wsl.ProbeLocalhostForwarding(ctx, pctx.runner, pctx.distro, port); err != nil {
		return wrapError(err, "WSL_LOCALHOST_FORWARDING_UNAVAILABLE", fmt.Sprintf("Verify that Windows Firewall allows inbound connections from WSL on port %d.", port))
	}
	return nil
}

type fakeWSLRunnerCLITest struct {
	mu sync.Mutex
}

func (f *fakeWSLRunnerCLITest) Run(_ context.Context, args ...string) ([]byte, error) {
	cmdStr := strings.Join(args, " ")
	switch {
	case cmdStr == "--list --quiet":
		return []byte("devstack\n"), nil
	case len(args) >= 5 && args[0] == "-d" && args[2] == "--" && args[3] == "cat" && args[4] == "/data/devstack/managed.json":
		return []byte(`{"managed_by":"devstack","install_id":"test-install-id"}`), nil
	case len(args) >= 5 && args[0] == "-d" && args[2] == "--" && args[3] == "cat" && args[4] == "/proc/1/comm":
		return []byte("systemd\n"), nil
	case strings.Contains(cmdStr, "devstack-probe"):
		return []byte("ok\n"), nil
	}
	return []byte("ok\n"), nil
}

type fakeAgentClientCLITest struct{}

func (fakeAgentClientCLITest) Health(_ context.Context) (agentproto.HealthResponse, error) {
	return agentproto.HealthResponse{Distro: "devstack", Version: "dev"}, nil
}
func (fakeAgentClientCLITest) InstallCA(_ context.Context, _ []byte) error { return nil }
func (fakeAgentClientCLITest) SyncHosts(_ context.Context, _ agentproto.HostsRequest) (agentproto.HostsResponse, error) {
	return agentproto.HostsResponse{Changed: true}, nil
}
func (fakeAgentClientCLITest) Version(context.Context) (agentproto.VersionResponse, error) {
	return agentproto.VersionResponse{}, nil
}
func (fakeAgentClientCLITest) Capabilities(context.Context) (agentproto.CapabilitiesResponse, error) {
	return agentproto.CapabilitiesResponse{}, nil
}
func (fakeAgentClientCLITest) AllocatePorts(context.Context, agentproto.PortRequest) (agentproto.PortResponse, error) {
	return agentproto.PortResponse{}, nil
}
func (fakeAgentClientCLITest) ReleasePorts(context.Context, agentproto.PortRequest) error { return nil }
func (fakeAgentClientCLITest) ValidatePorts(context.Context, agentproto.PortRequest) (agentproto.PortValidateResponse, error) {
	return agentproto.PortValidateResponse{}, nil
}
func (fakeAgentClientCLITest) PrepareSite(context.Context, agentproto.SitePrepareRequest) (agentproto.SitePrepareResponse, error) {
	return agentproto.SitePrepareResponse{}, nil
}
func (fakeAgentClientCLITest) StartSite(context.Context, string, agentproto.SiteActionRequest) (agentproto.SiteActionResponse, error) {
	return agentproto.SiteActionResponse{}, nil
}
func (fakeAgentClientCLITest) StopSite(context.Context, string, agentproto.SiteActionRequest) (agentproto.SiteActionResponse, error) {
	return agentproto.SiteActionResponse{}, nil
}
func (fakeAgentClientCLITest) SiteStatus(context.Context, string) (agentproto.SiteStatusResponse, error) {
	return agentproto.SiteStatusResponse{}, nil
}
func (fakeAgentClientCLITest) SiteLogs(context.Context, string, agentproto.SiteLogsRequest) (io.ReadCloser, error) {
	return nil, nil
}
func (fakeAgentClientCLITest) ExecSite(context.Context, string, agentproto.SiteExecRequest) (agentproto.SiteExecResponse, error) {
	return agentproto.SiteExecResponse{}, nil
}
func (fakeAgentClientCLITest) PreparePHP(context.Context, string, agentproto.PHPPrepareRequest) (agentproto.SitePrepareResponse, error) {
	return agentproto.SitePrepareResponse{}, nil
}
func (fakeAgentClientCLITest) StartPHP(context.Context, string, agentproto.PHPActionRequest) (agentproto.SiteActionResponse, error) {
	return agentproto.SiteActionResponse{}, nil
}
func (fakeAgentClientCLITest) StopPHP(context.Context, string, agentproto.PHPActionRequest) (agentproto.SiteActionResponse, error) {
	return agentproto.SiteActionResponse{}, nil
}
func (fakeAgentClientCLITest) BuildSite(context.Context, string, agentproto.BuildRequest) (agentproto.BuildResponse, error) {
	return agentproto.BuildResponse{}, nil
}
func (fakeAgentClientCLITest) TerminalTicket(context.Context, string, agentproto.TerminalTicketRequest) (agentproto.TerminalTicketResponse, error) {
	return agentproto.TerminalTicketResponse{}, nil
}
func (fakeAgentClientCLITest) InstallRuntime(context.Context, agentproto.RuntimeRequest) (agentproto.RuntimeResponse, error) {
	return agentproto.RuntimeResponse{}, nil
}
func (fakeAgentClientCLITest) RemoveRuntime(context.Context, agentproto.RuntimeRequest) error {
	return nil
}
func (fakeAgentClientCLITest) ListRuntimes(context.Context) (agentproto.RuntimeListResponse, error) {
	return agentproto.RuntimeListResponse{}, nil
}
func (fakeAgentClientCLITest) ServiceAction(context.Context, agentproto.ServiceRequest) (agentproto.ServiceResponse, error) {
	return agentproto.ServiceResponse{}, nil
}
func (fakeAgentClientCLITest) ServiceStatus(context.Context, string) (agentproto.ServiceResponse, error) {
	return agentproto.ServiceResponse{}, nil
}
func (fakeAgentClientCLITest) ServiceLogs(context.Context, string, agentproto.ServiceLogsRequest) (agentproto.ServiceLogsResponse, error) {
	return agentproto.ServiceLogsResponse{}, nil
}
func (fakeAgentClientCLITest) ServiceConfig(context.Context, string) (agentproto.ServiceConfigResponse, error) {
	return agentproto.ServiceConfigResponse{}, nil
}
func (fakeAgentClientCLITest) ServiceContent(context.Context, agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error) {
	return agentproto.ServiceContentResponse{}, nil
}
func (fakeAgentClientCLITest) ServiceApply(context.Context, agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error) {
	return agentproto.ServiceContentResponse{}, nil
}
func (fakeAgentClientCLITest) ServiceReset(context.Context, agentproto.ServiceConfigRequest) error {
	return nil
}
func (fakeAgentClientCLITest) ServiceCredential(context.Context, agentproto.ServiceCredentialRequest) (agentproto.ServiceCredentialResponse, error) {
	return agentproto.ServiceCredentialResponse{}, nil
}
func (fakeAgentClientCLITest) WebTargets(context.Context, agentproto.WebTargetsRequest) error {
	return nil
}
func (fakeAgentClientCLITest) SyncWeb(context.Context, agentproto.WebTargetsRequest) (agentproto.WebSyncResponse, error) {
	return agentproto.WebSyncResponse{}, nil
}
func (fakeAgentClientCLITest) EnsureCert(context.Context, agentproto.CertRequest) (agentproto.CertResponse, error) {
	return agentproto.CertResponse{}, nil
}
func (fakeAgentClientCLITest) CA(context.Context) (agentproto.CAResponse, error) {
	return agentproto.CAResponse{}, nil
}
func (fakeAgentClientCLITest) ValidatePath(context.Context, agentproto.PathValidateRequest) (agentproto.PathValidateResponse, error) {
	return agentproto.PathValidateResponse{}, nil
}
func (fakeAgentClientCLITest) CopySource(context.Context, agentproto.SourceCopyRequest) (agentproto.SourceCopyResponse, error) {
	return agentproto.SourceCopyResponse{}, nil
}

func setupFakeWSLForCLITests(t testing.TB) {
	prevRunner := wslRunnerFactory
	prevAgent := newAgentClient
	t.Cleanup(func() {
		wslRunnerFactory = prevRunner
		newAgentClient = prevAgent
	})
	wslRunnerFactory = func() wsl.Runner {
		return &fakeWSLRunnerCLITest{}
	}
	newAgentClient = func(_ agentclient.Config) (agentclient.API, error) {
		return fakeAgentClientCLITest{}, nil
	}
}
