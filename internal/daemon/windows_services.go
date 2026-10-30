//go:build windows

package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/build"
	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/terminal"
)

// buildAgent is the narrow agent surface used by the Windows build adapter.
type buildAgent interface {
	BuildSite(context.Context, string, agentproto.BuildRequest) (agentproto.BuildResponse, error)
}

// windowsBuildService runs the build in the WSL agent, where /bin/sh and the
// Linux Node runtime actually exist. The daemon still owns the output path
// validation and the build log.
type windowsBuildService struct {
	ensure func(context.Context) (buildAgent, error)
	paths  config.Paths
}

func newWindowsBuildService(adapter *windowsAgentServiceAdapter, paths config.Paths) *windowsBuildService {
	return &windowsBuildService{
		ensure: func(ctx context.Context) (buildAgent, error) {
			return adapter.ensureAwake(ctx)
		},
		paths: paths,
	}
}

func (s *windowsBuildService) Build(ctx context.Context, site model.Site) (build.Result, error) {
	result := build.Result{Site: site.Name}
	if site.Build == nil {
		return result, fmt.Errorf("%w: site %q has no build profile", build.ErrInvalidSetup, site.Name)
	}
	if site.Kind != model.SiteStatic {
		return result, fmt.Errorf("%w: site %q is not a static site", build.ErrInvalidSetup, site.Name)
	}
	command := strings.TrimSpace(site.Build.Command)
	if command == "" {
		return result, fmt.Errorf("%w: site %q has no build command", build.ErrInvalidSetup, site.Name)
	}
	output, err := build.OutputPath(site)
	if err != nil {
		return result, fmt.Errorf("%w: %v", build.ErrInvalidSetup, err)
	}
	agent, err := s.ensure(ctx)
	if err != nil {
		return result, fmt.Errorf("build: WSL agent unavailable: %w", err)
	}
	result.Command = command
	result.OutputPath = output
	started := time.Now()
	resp, err := agent.BuildSite(ctx, site.ID, agentproto.BuildRequest{
		Command:        command,
		Env:            site.Build.Env,
		TimeoutMS:      int((5 * time.Minute) / time.Millisecond),
		BackendPath:    site.BackendPath,
		RuntimeVersion: strings.TrimSpace(site.Build.NodeVersion),
	})
	result.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		return result, fmt.Errorf("build: agent build: %w", err)
	}
	content := resp.Stdout
	if resp.Stderr != "" {
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += resp.Stderr
	}
	result.LogPath, err = s.writeLog(site, content)
	if err != nil {
		return result, err
	}
	result.Success = resp.ExitCode == 0
	if !result.Success {
		result.Error = strings.TrimSpace(resp.Stderr)
		if result.Error == "" {
			result.Error = fmt.Sprintf("build exited with code %d", resp.ExitCode)
		}
	}
	return result, nil
}

func (s *windowsBuildService) Logs(ctx context.Context, site model.Site, tail int) (build.Logs, error) {
	return build.NewService(build.Deps{Paths: s.paths}).Logs(ctx, site, tail)
}

func (s *windowsBuildService) writeLog(site model.Site, content string) (string, error) {
	dir := filepath.Join(s.paths.Logs, "builds", build.SafeComponent(site.Name))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("build: create log directory: %w", err)
	}
	base := time.Now().UTC().Format("20060102T150405.000000000Z")
	path := filepath.Join(dir, base+".log")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", fmt.Errorf("build: write log: %w", err)
	}
	return path, nil
}

// terminalAgent is the narrow agent surface used by the Windows terminal
// adapter.
type terminalAgent interface {
	TerminalTicket(context.Context, string, agentproto.TerminalTicketRequest) (agentproto.TerminalTicketResponse, error)
	TerminalWebSocket(context.Context, string, string) (*websocket.Conn, error)
}

// windowsTerminalService proxies the dashboard WebSocket to the WSL agent's
// PTY. The agent owns the shell, environment, scaffolding, and ticket lifetime.
type windowsTerminalService struct {
	ensure func(context.Context) (terminalAgent, error)
	logger *slog.Logger
}

func newWindowsTerminalService(adapter *windowsAgentServiceAdapter, logger *slog.Logger) *windowsTerminalService {
	return &windowsTerminalService{
		ensure: func(ctx context.Context) (terminalAgent, error) {
			return adapter.ensureAwake(ctx)
		},
		logger: logger,
	}
}

func (s *windowsTerminalService) IssueTicket(ctx context.Context, site model.Site) (terminal.Ticket, error) {
	agent, err := s.ensure(ctx)
	if err != nil {
		return terminal.Ticket{}, fmt.Errorf("terminal: WSL agent unavailable: %w", err)
	}
	resp, err := agent.TerminalTicket(ctx, site.ID, agentproto.TerminalTicketRequest{Cols: 100, Rows: 30, SiteName: site.Name})
	if err != nil {
		return terminal.Ticket{}, fmt.Errorf("terminal: agent ticket: %w", err)
	}
	return terminal.Ticket{Token: resp.Ticket, ExpiresAt: resp.ExpiresAt, SiteID: site.ID}, nil
}

func (s *windowsTerminalService) Serve(ctx context.Context, w http.ResponseWriter, r *http.Request, site model.Site, ticket string) error {
	agent, err := s.ensure(ctx)
	if err != nil {
		return fmt.Errorf("terminal: WSL agent unavailable: %w", err)
	}
	agentConn, err := agent.TerminalWebSocket(ctx, site.ID, strings.TrimSpace(ticket))
	if err != nil {
		return fmt.Errorf("terminal: agent WebSocket: %w", err)
	}
	defer agentConn.CloseNow()

	browserConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// The dashboard is authenticated by the install token, session cookie,
		// CSRF check, and origin allowlist before this point.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return fmt.Errorf("%w: %v", terminal.ErrUpgrade, err)
	}
	defer browserConn.CloseNow()
	return bridgeTerminal(ctx, browserConn, agentConn)
}

// bridgeTerminal relays frames in both directions until either side closes.
func bridgeTerminal(ctx context.Context, browser, agent *websocket.Conn) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errs := make(chan error, 2)
	copyFrames := func(dst, src *websocket.Conn) {
		for {
			typ, data, err := src.Read(ctx)
			if err != nil {
				errs <- err
				return
			}
			if err := dst.Write(ctx, typ, data); err != nil {
				errs <- err
				return
			}
		}
	}
	go copyFrames(agent, browser)
	go copyFrames(browser, agent)

	err := <-errs
	cancel()
	_ = browser.Close(websocket.StatusNormalClosure, "")
	_ = agent.Close(websocket.StatusNormalClosure, "")
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	// A peer disconnect is a normal terminal close, not an API failure.
	return nil
}
