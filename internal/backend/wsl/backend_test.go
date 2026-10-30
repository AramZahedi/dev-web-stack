package wslbackend

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentclient"
	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/backend"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

type fakeAgent struct {
	agentclient.API
	prepareRequest agentproto.SitePrepareRequest
	prepareResult  agentproto.SitePrepareResponse
	prepareError   error
	caInstall      []byte
	started        []string
	stopped        []string
	status         agentproto.SiteStatusResponse
	statusErr      error
	logs           io.ReadCloser
	execRequest    agentproto.SiteExecRequest
	execResult     agentproto.SiteExecResponse
}

func (f *fakeAgent) PrepareSite(_ context.Context, req agentproto.SitePrepareRequest) (agentproto.SitePrepareResponse, error) {
	f.prepareRequest = req
	return f.prepareResult, f.prepareError
}
func (f *fakeAgent) StartSite(_ context.Context, id string, _ agentproto.SiteActionRequest) (agentproto.SiteActionResponse, error) {
	f.started = append(f.started, id)
	return agentproto.SiteActionResponse{Status: "running", PID: 41}, nil
}
func (f *fakeAgent) StopSite(_ context.Context, id string, _ agentproto.SiteActionRequest) (agentproto.SiteActionResponse, error) {
	f.stopped = append(f.stopped, id)
	return agentproto.SiteActionResponse{Status: "stopped"}, nil
}
func (f *fakeAgent) SiteStatus(context.Context, string) (agentproto.SiteStatusResponse, error) {
	return f.status, f.statusErr
}
func (f *fakeAgent) SiteLogs(context.Context, string, agentproto.SiteLogsRequest) (io.ReadCloser, error) {
	return f.logs, nil
}
func (f *fakeAgent) ExecSite(_ context.Context, _ string, req agentproto.SiteExecRequest) (agentproto.SiteExecResponse, error) {
	f.execRequest = req
	return f.execResult, nil
}
func (f *fakeAgent) InstallCA(_ context.Context, certPEM []byte) error {
	f.caInstall = append([]byte(nil), certPEM...)
	return nil
}

func TestPrepareMapsBackendAndWindowsProxyDocumentRoots(t *testing.T) {
	agent := &fakeAgent{prepareResult: agentproto.SitePrepareResponse{
		BackendHost: "127.0.0.1", HTTPPort: 41812, FastCGIPort: 43101,
	}}
	backendClient := New(agent)
	prepared, err := backendClient.Prepare(context.Background(), backend.SiteSpec{
		ID: "site-id", Kind: "php", Domain: "shop.test", HostPath: `C:\devstack\projects\shop`,
		BackendPath: "/mnt/c/devstack/projects/shop", DocumentRoot: "public",
		HTTPPort: 41812, FastCGIPort: 43101, FrontController: true,
		PHPVersion: "8.3", PHPBuild: "php-8.3-test", PHPBuildPath: "/data/devstack/runtimes/php/test",
		PHPIniPath: "/data/devstack/sites/site-id/php.ini", FPMConfigPath: "/data/devstack/sites/site-id/fpm.conf",
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if prepared.BackendHost != "127.0.0.1" || prepared.HTTPPort != 41812 || prepared.FastCGIPort != 43101 {
		t.Fatalf("PreparedSite = %+v", prepared)
	}
	site := agent.prepareRequest.Site
	if site.BackendPath != "/mnt/c/devstack/projects/shop" || site.DocumentRoot != "/mnt/c/devstack/projects/shop/public" {
		t.Fatalf("agent backend paths = backend %q document %q", site.BackendPath, site.DocumentRoot)
	}
	if site.ProxyDocumentRoot != `C:\devstack\projects\shop\public` {
		t.Fatalf("ProxyDocumentRoot = %q", site.ProxyDocumentRoot)
	}
	if site.FastCGIPort != 43101 || site.PHPBuildPath == "" || !site.FrontController {
		t.Fatalf("agent site spec lost PHP configuration: %+v", site)
	}
	if site.Domain != "shop.test" {
		t.Fatalf("agent site domain = %q, want shop.test", site.Domain)
	}
}

func TestInstallCAForwardsDaemonCertificateToAgent(t *testing.T) {
	pem := []byte("-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----\n")
	agent := &fakeAgent{}
	if err := New(agent).InstallCA(context.Background(), pem); err != nil {
		t.Fatalf("InstallCA: %v", err)
	}
	if string(agent.caInstall) != string(pem) {
		t.Fatalf("installed CA = %q, want %q", agent.caInstall, pem)
	}
}

func TestPrepareSurfacesTypedAgentAuthenticationFailure(t *testing.T) {
	want := &agentproto.Error{Code: agentproto.CodeUnauthenticated, Message: "bad token", Status: 401}
	_, err := New(&fakeAgent{prepareError: want}).Prepare(context.Background(), backend.SiteSpec{ID: "site"})
	if !errors.Is(err, agentproto.ErrUnauthenticated) {
		t.Fatalf("Prepare error = %v, want typed unauthenticated error", err)
	}
}

func TestLifecycleStatusLogsAndExecUseAgent(t *testing.T) {
	logReader := io.NopCloser(strings.NewReader("agent log"))
	agent := &fakeAgent{
		status: agentproto.SiteStatusResponse{Status: "running", PID: 77},
		logs:   logReader, execResult: agentproto.SiteExecResponse{ExitCode: 3, Stdout: "out", Stderr: "err"},
	}
	b := New(agent)
	if info, err := b.Start(context.Background(), "site"); err != nil || info.PID != 41 || len(agent.started) != 1 {
		t.Fatalf("Start = %+v, %v; calls=%v", info, err, agent.started)
	}
	if err := b.Stop(context.Background(), "site", true); err != nil || len(agent.stopped) != 1 {
		t.Fatalf("Stop = %v; calls=%v", err, agent.stopped)
	}
	status, err := b.Status(context.Background(), "site")
	if err != nil || status.State != "running" || status.PID != 77 {
		t.Fatalf("Status = %+v, %v", status, err)
	}
	logs, err := b.Logs(context.Background(), "site", true)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	data, err := io.ReadAll(logs)
	if err != nil || string(data) != "agent log" {
		t.Fatalf("Logs content = %q, %v", data, err)
	}
	result, err := b.Exec(context.Background(), "site", []string{"php", "-v"}, map[string]string{"A": "B"})
	if err != nil || result.ExitCode != 3 || result.Stdout != "out" || result.Stderr != "err" || agent.execRequest.Env["A"] != "B" {
		t.Fatalf("Exec = %+v, %v; request=%+v", result, err, agent.execRequest)
	}
	_, err = b.Exec(context.Background(), "site", []string{`C:\Users\Aram\.devstack\sites\s\php.ini`, "-c"}, nil)
	if err != nil {
		t.Fatalf("Exec windows path: %v", err)
	}
	if got, want := agent.execRequest.Argv[0], "/mnt/c/Users/Aram/.devstack/sites/s/php.ini"; got != want {
		t.Fatalf("Exec translated argv[0] = %q, want %q", got, want)
	}
	if agent.execRequest.Argv[1] != "-c" {
		t.Fatalf("Exec changed a flag: %+v", agent.execRequest.Argv)
	}
}

func TestStatusTreatsAnUnknownAgentSiteAsStopped(t *testing.T) {
	agent := &fakeAgent{statusErr: &agentproto.Error{Code: agentproto.CodeNotFound, Message: "agent: site not found", Status: 404}}
	status, err := New(agent).Status(context.Background(), "unprepared")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != model.StatusStopped {
		t.Fatalf("status = %+v, want stopped", status)
	}
	agent.statusErr = errors.New("agent offline")
	if _, err := New(agent).Status(context.Background(), "unprepared"); err == nil {
		t.Fatal("Status swallowed a non-not-found error")
	}
}

func TestToWSLPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"/var/www/site", "/var/www/site"},
		{`C:\Projects\app`, "/mnt/c/Projects/app"},
		{`c:/Projects/app`, "/mnt/c/Projects/app"},
		{`D:\Data\site`, "/mnt/d/Data/site"},
		{`\\wsl.localhost\devstack\data\devstack\projects\app`, "/data/devstack/projects/app"},
		{`\\wsl$\devstack\home\user\site`, "/home/user/site"},
		{`//wsl.localhost/devstack/var/www`, "/var/www"},
		{`//wsl$/devstack/srv/data`, "/srv/data"},
		{`\\wsl.localhost\devstack`, "/"},
		{`//wsl.localhost/devstack/`, "/"},
	}

	for _, tc := range tests {
		got := toWSLPath(tc.input)
		if got != tc.want {
			t.Errorf("toWSLPath(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestToAgentSiteSourceMode(t *testing.T) {
	tests := []struct {
		hostPath string
		wantMode string
	}{
		{`C:\Projects\app`, "windows"},
		{`c:/Projects/app`, "windows"},
		{`\\wsl.localhost\devstack\data\app`, "wsl"},
		{`//wsl.localhost/devstack/data/app`, "wsl"},
		{`\\wsl$\devstack\home\user\app`, "wsl"},
		{`//wsl$/devstack/home/user/app`, "wsl"},
		{`/data/app`, ""},
	}

	for _, tc := range tests {
		spec, err := toAgentSite(backend.SiteSpec{HostPath: tc.hostPath})
		if err != nil {
			t.Fatal(err)
		}
		if spec.SourceMode != tc.wantMode {
			t.Errorf("toAgentSite(%q).SourceMode = %q, want %q", tc.hostPath, spec.SourceMode, tc.wantMode)
		}
	}
}

func TestDatabaseLinksPayloadMatchesAgentWireShape(t *testing.T) {
	links := []backend.DatabaseLinkSpec{{
		ServiceID: "mysql", Engine: "mysql", Database: "app", UserID: "usr_app",
		Username: "app", Host: "127.0.0.1", Port: 3306, Primary: true, PasswordManaged: true,
	}}
	raw, err := databaseLinksPayload(links)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"service_id"`, `"user_id"`, `"password_managed"`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("payload missing %s: %s", key, raw)
		}
	}
	var decoded struct {
		DatabaseLinks []struct {
			ServiceID       string `json:"service_id"`
			UserID          string `json:"user_id"`
			PasswordManaged bool   `json:"password_managed"`
		} `json:"database_links"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.DatabaseLinks) != 1 || decoded.DatabaseLinks[0].UserID != "usr_app" || !decoded.DatabaseLinks[0].PasswordManaged {
		t.Fatalf("decoded = %+v", decoded.DatabaseLinks)
	}
}
