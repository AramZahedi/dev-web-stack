//go:build windows

package daemon

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

type fakeBuildAgent struct {
	siteID string
	req    agentproto.BuildRequest
	resp   agentproto.BuildResponse
	err    error
}

func (f *fakeBuildAgent) BuildSite(_ context.Context, siteID string, req agentproto.BuildRequest) (agentproto.BuildResponse, error) {
	f.siteID = siteID
	f.req = req
	return f.resp, f.err
}

func TestWindowsBuildServiceRunsInAgentAndWritesLog(t *testing.T) {
	agent := &fakeBuildAgent{resp: agentproto.BuildResponse{ExitCode: 0, Stdout: "built in WSL\n"}}
	paths := config.Paths{Logs: t.TempDir()}
	svc := &windowsBuildService{
		ensure: func(context.Context) (buildAgent, error) { return agent, nil },
		paths:  paths,
	}
	site := model.Site{
		ID: "site-1", Name: "blog", Kind: model.SiteStatic, HostPath: t.TempDir(),
		Build: &model.BuildProfile{Command: "npm run build", Output: "dist", Env: map[string]string{"NODE_ENV": "production"}},
	}
	result, err := svc.Build(context.Background(), site)
	if err != nil {
		t.Fatalf("Build = %v", err)
	}
	if !result.Success || result.Command != "npm run build" {
		t.Fatalf("result = %+v", result)
	}
	if agent.siteID != site.ID || agent.req.Command != "npm run build" || agent.req.Env["NODE_ENV"] != "production" {
		t.Fatalf("agent request = %+v (site %q)", agent.req, agent.siteID)
	}
	if !strings.HasSuffix(result.OutputPath, "dist") {
		t.Fatalf("output path = %q", result.OutputPath)
	}
	if result.LogPath == "" || result.DurationMS < 0 {
		t.Fatalf("result log/duration = %+v", result)
	}
	logs, err := svc.Logs(context.Background(), site, 10)
	if err != nil {
		t.Fatalf("Logs = %v", err)
	}
	if !strings.Contains(logs.Content, "built in WSL") {
		t.Fatalf("logs content = %q", logs.Content)
	}
}

func TestWindowsBuildServiceReportsAgentFailureWithoutHidingLogs(t *testing.T) {
	agent := &fakeBuildAgent{resp: agentproto.BuildResponse{ExitCode: 2, Stdout: "stdout\n", Stderr: "build broke\n"}}
	paths := config.Paths{Logs: t.TempDir()}
	svc := &windowsBuildService{
		ensure: func(context.Context) (buildAgent, error) { return agent, nil },
		paths:  paths,
	}
	site := model.Site{
		ID: "site-2", Name: "broken", Kind: model.SiteStatic, HostPath: t.TempDir(),
		Build: &model.BuildProfile{Command: "npm run build", Output: "dist"},
	}
	result, err := svc.Build(context.Background(), site)
	if err != nil {
		t.Fatalf("Build = %v", err)
	}
	if result.Success || !strings.Contains(result.Error, "build broke") {
		t.Fatalf("result = %+v", result)
	}
	logs, err := svc.Logs(context.Background(), site, 10)
	if err != nil {
		t.Fatalf("Logs = %v", err)
	}
	if !strings.Contains(logs.Content, "stdout") || !strings.Contains(logs.Content, "build broke") {
		t.Fatalf("logs content = %q", logs.Content)
	}
}

func TestWindowsBuildServiceRejectsInvalidSetupBeforeCallingAgent(t *testing.T) {
	agent := &fakeBuildAgent{resp: agentproto.BuildResponse{ExitCode: 0}}
	svc := &windowsBuildService{
		ensure: func(context.Context) (buildAgent, error) { return agent, nil },
		paths:  config.Paths{Logs: t.TempDir()},
	}
	cases := []model.Site{
		{ID: "a", Name: "no-profile", Kind: model.SiteStatic, HostPath: t.TempDir()},
		{ID: "b", Name: "php", Kind: model.SitePHP, HostPath: t.TempDir(), Build: &model.BuildProfile{Command: "npm run build", Output: "dist"}},
		{ID: "c", Name: "no-command", Kind: model.SiteStatic, HostPath: t.TempDir(), Build: &model.BuildProfile{Output: "dist"}},
		{ID: "d", Name: "escape", Kind: model.SiteStatic, HostPath: t.TempDir(), Build: &model.BuildProfile{Command: "npm run build", Output: "../outside"}},
	}
	for _, site := range cases {
		if _, err := svc.Build(context.Background(), site); err == nil {
			t.Fatalf("Build(%s) accepted invalid setup", site.Name)
		}
	}
	if agent.siteID != "" {
		t.Fatal("agent was called for an invalid setup")
	}
}

func TestWindowsBuildServiceSurfacesAgentConnectionError(t *testing.T) {
	svc := &windowsBuildService{
		ensure: func(context.Context) (buildAgent, error) { return nil, errors.New("agent offline") },
		paths:  config.Paths{Logs: t.TempDir()},
	}
	site := model.Site{
		ID: "site-3", Name: "offline", Kind: model.SiteStatic, HostPath: t.TempDir(),
		Build: &model.BuildProfile{Command: "npm run build", Output: "dist"},
	}
	result, err := svc.Build(context.Background(), site)
	if err == nil || !strings.Contains(err.Error(), "agent offline") {
		t.Fatalf("Build = %+v, %v", result, err)
	}
	if result.Success {
		t.Fatalf("failed build result = %+v", result)
	}
}

type fakeTerminalAgent struct {
	ticket    agentproto.TerminalTicketResponse
	ticketErr error
	dial      func(context.Context, string, string) (*websocket.Conn, error)
}

func (f *fakeTerminalAgent) TerminalTicket(context.Context, string, agentproto.TerminalTicketRequest) (agentproto.TerminalTicketResponse, error) {
	return f.ticket, f.ticketErr
}

func (f *fakeTerminalAgent) TerminalWebSocket(ctx context.Context, id, ticket string) (*websocket.Conn, error) {
	return f.dial(ctx, id, ticket)
}

func TestWindowsTerminalServiceIssuesAgentTicket(t *testing.T) {
	expires := time.Now().Add(30 * time.Second)
	agent := &fakeTerminalAgent{ticket: agentproto.TerminalTicketResponse{Ticket: "agent-ticket", ExpiresAt: expires}}
	svc := &windowsTerminalService{ensure: func(context.Context) (terminalAgent, error) { return agent, nil }}
	ticket, err := svc.IssueTicket(context.Background(), model.Site{ID: "site-1", Name: "blog"})
	if err != nil {
		t.Fatalf("IssueTicket = %v", err)
	}
	if ticket.Token != "agent-ticket" || ticket.SiteID != "site-1" || !ticket.ExpiresAt.Equal(expires) {
		t.Fatalf("ticket = %+v", ticket)
	}
}

func TestWindowsTerminalServiceBridgesFrames(t *testing.T) {
	agentLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	agentSrv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		typ, data, err := conn.Read(r.Context())
		if err != nil || string(data) != "hello" {
			return
		}
		_ = conn.Write(r.Context(), typ, []byte("world"))
	}))
	agentSrv.Listener = agentLn
	agentSrv.Start()
	defer agentSrv.Close()

	agent := &fakeTerminalAgent{
		dial: func(ctx context.Context, id, ticket string) (*websocket.Conn, error) {
			if id != "site-1" || ticket != "ticket-1" {
				t.Fatalf("dial id=%q ticket=%q", id, ticket)
			}
			conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(agentSrv.URL, "http"), nil)
			return conn, err
		},
	}
	svc := &windowsTerminalService{ensure: func(context.Context) (terminalAgent, error) { return agent, nil }}

	daemonSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = svc.Serve(r.Context(), w, r, model.Site{ID: "site-1", Name: "blog"}, "ticket-1")
	}))
	defer daemonSrv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	browser, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(daemonSrv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer browser.CloseNow()
	if err := browser.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	typ, data, err := browser.Read(ctx)
	if err != nil {
		t.Fatalf("browser read: %v", err)
	}
	if typ != websocket.MessageText || string(data) != "world" {
		t.Fatalf("browser frame = %v %q", typ, data)
	}
}
