package dockerweb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

const windowsPrivateHTTPPort = 18080

// WindowsPrivateSyncer prepares the WSL container configuration without
// stopping the daemon's builtin proxy or touching public host ports.
type WindowsPrivateSyncer interface {
	EnsurePrivateBase(server Server) error
	SyncPrivate(ctx context.Context, server Server) error
}

type privateWebAgent interface {
	SyncWeb(context.Context, agentproto.WebTargetsRequest) (agentproto.WebSyncResponse, error)
}

// PrivateWebReloader applies synced files to the running web container.
type PrivateWebReloader func(ctx context.Context, server string) error

// WindowsTargetSource builds the typed WSL backend target set for one active
// Docker web server. Site data and certificates are still agent-owned.
type WindowsTargetSource interface {
	PrivateWebTargets(ctx context.Context, server string) ([]agentproto.WebTarget, error)
}

// AgentWindowsPrivateSyncer bridges the Windows provider to the typed agent
// web endpoint without reading or writing service files over NTFS/UNC.
type AgentWindowsPrivateSyncer struct {
	agent   privateWebAgent
	targets WindowsTargetSource
	reload  PrivateWebReloader

	mu        sync.Mutex
	lastSync  time.Time
	configOK  bool
	lastError string
}

// NewAgentWindowsPrivateSyncer constructs the authenticated-agent web syncer.
// An optional reloader applies site-driven config syncs to the live container;
// provider activations pass no reloader because Commit owns that transition.
func NewAgentWindowsPrivateSyncer(agent privateWebAgent, targets WindowsTargetSource, reload ...PrivateWebReloader) (*AgentWindowsPrivateSyncer, error) {
	if agent == nil || targets == nil {
		return nil, errors.New("dockerweb: Windows agent sync requires an agent client and target source")
	}
	syncer := &AgentWindowsPrivateSyncer{agent: agent, targets: targets}
	if len(reload) > 0 {
		syncer.reload = reload[0]
	}
	return syncer, nil
}

// DockerReload applies site-driven vhost syncs to the running container.
func (s *AgentWindowsPrivateSyncer) DockerReload(ctx context.Context, server string) error {
	if s.reload == nil {
		return nil
	}
	parsed := Server(strings.ToLower(strings.TrimSpace(server)))
	if err := parsed.Validate(); err != nil {
		return err
	}
	if err := s.reload(ctx, string(parsed)); err != nil {
		s.record(fmt.Errorf("dockerweb: reload private %s: %w", parsed, err))
		return err
	}
	return nil
}

// EnsurePrivateBase validates the fixed server; the agent seeds the private
// base atomically during the next SyncPrivate call.
func (*AgentWindowsPrivateSyncer) EnsurePrivateBase(server Server) error { return server.Validate() }

// SyncPrivate sends only typed targets. The agent owns all WSL config files.
func (s *AgentWindowsPrivateSyncer) SyncPrivate(ctx context.Context, server Server) error {
	return s.Sync(ctx, string(server))
}

// Sync implements sites.DockerSyncer for the Windows Docker provider.
func (s *AgentWindowsPrivateSyncer) Sync(ctx context.Context, server string) error {
	if strings.TrimSpace(server) == "" {
		s.record(errors.New("dockerweb: private web server is required"))
		return errors.New("dockerweb: private web server is required")
	}
	parsed := Server(strings.ToLower(strings.TrimSpace(server)))
	if err := parsed.Validate(); err != nil {
		s.record(err)
		return err
	}
	targets, err := s.targets.PrivateWebTargets(ctx, string(parsed))
	if err != nil {
		err = fmt.Errorf("dockerweb: build private %s targets: %w", parsed, err)
		s.record(err)
		return err
	}
	if s.agent == nil {
		err = errors.New("dockerweb: WSL agent is not connected")
		s.record(err)
		return err
	}
	if _, err := s.agent.SyncWeb(ctx, agentproto.WebTargetsRequest{Server: string(parsed), Targets: targets}); err != nil {
		err = fmt.Errorf("dockerweb: sync private %s targets through agent: %w", parsed, err)
		s.record(err)
		return err
	}
	s.mu.Lock()
	s.lastSync = time.Now().UTC()
	s.configOK = true
	s.lastError = ""
	s.mu.Unlock()
	return nil
}

// DockerStatus reports the last successful private sync.
func (s *AgentWindowsPrivateSyncer) DockerStatus() (time.Time, bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSync, s.configOK, s.lastError
}

// DockerRoutable delegates to the target source when it exposes the source-mode
// policy. Unknown sources default to routable.
func (s *AgentWindowsPrivateSyncer) DockerRoutable(site model.Site) bool {
	if source, ok := s.targets.(interface{ DockerRoutable(model.Site) bool }); ok {
		return source.DockerRoutable(site)
	}
	return true
}

func (s *AgentWindowsPrivateSyncer) record(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configOK = false
	if err != nil {
		s.lastError = err.Error()
	}
}

// ProxyRouterTargetUpdater updates proxy router targets when Docker web services start or stop.
type ProxyRouterTargetUpdater interface {
	UpdateDockerWebRoutes(ctx context.Context, active bool, host string, port int) error
}

// ProviderSwitch stops or starts a web container through the explicit
// provider-switch path, bypassing the active-server deactivation guard.
type ProviderSwitch interface {
	StopActiveServer(ctx context.Context, server string) error
	StartActiveServer(ctx context.Context, server string) error
}

// WindowsProviderConfig wires the Windows/WSL private-HTTP serving adapter.
type WindowsProviderConfig struct {
	State    ServingState
	Syncer   WindowsPrivateSyncer
	Services ServiceControl
	Switch   ProviderSwitch
	Router   ProxyRouterTargetUpdater
}

// WindowsProvider switches only the private WSL web service. The daemon stays
// the owner of public routing, public TLS, and devstack.test.
type WindowsProvider struct {
	state    ServingState
	syncer   WindowsPrivateSyncer
	services ServiceControl
	switchTo ProviderSwitch
	router   ProxyRouterTargetUpdater

	mu           sync.Mutex
	prepared     bool
	server       Server
	previousMode string
	previousFrom string
}

// NewWindowsProvider validates and constructs the Windows private web provider.
func NewWindowsProvider(cfg WindowsProviderConfig) (*WindowsProvider, error) {
	if cfg.State == nil || cfg.Syncer == nil || cfg.Services == nil {
		return nil, errors.New("dockerweb: Windows provider requires state, private syncer, and service control")
	}
	return &WindowsProvider{
		state:    cfg.State,
		syncer:   cfg.Syncer,
		services: cfg.Services,
		switchTo: cfg.Switch,
		router:   cfg.Router,
	}, nil
}

// EnsureBase scaffolds private configuration in WSL.
func (p *WindowsProvider) EnsureBase(serverName string) error {
	server, err := parseServer(serverName)
	if err != nil {
		return err
	}
	return p.syncer.EnsurePrivateBase(server)
}

// Prepare syncs HTTP-only WSL vhosts without changing public daemon listeners.
func (p *WindowsProvider) Prepare(ctx context.Context, serverName string) error {
	server, err := parseServer(serverName)
	if err != nil {
		return err
	}
	p.mu.Lock()
	if p.prepared {
		p.mu.Unlock()
		return errors.New("dockerweb: a Windows provider transition is already prepared")
	}
	previousMode := strings.TrimSpace(p.state.ServingMode())
	previousProvider := strings.TrimSpace(p.state.ServingProvider())
	if previousMode == "" {
		previousMode = "builtin"
	}
	if previousProvider == "" {
		previousProvider = "native"
	}
	p.mu.Unlock()
	if previousProvider == "docker" && previousMode != "" && previousMode != "builtin" && previousMode != string(server) {
		return fmt.Errorf("dockerweb: %s is active; switch to builtin before activating %s", previousMode, server)
	}
	if err := p.syncer.EnsurePrivateBase(server); err != nil {
		return fmt.Errorf("dockerweb: ensure WSL private %s config: %w", server, err)
	}
	if err := p.syncer.SyncPrivate(ctx, server); err != nil {
		return fmt.Errorf("dockerweb: sync WSL private %s config: %w", server, err)
	}
	p.mu.Lock()
	p.prepared = true
	p.server = server
	p.previousMode = previousMode
	p.previousFrom = previousProvider
	p.mu.Unlock()
	return nil
}

// Commit validates and reloads the WSL web server before publishing the Docker
// provider state. The daemon's public TLS route is unaffected.
func (p *WindowsProvider) Commit(ctx context.Context, serverName string) error {
	server, err := parseServer(serverName)
	if err != nil {
		return err
	}
	p.mu.Lock()
	prepared, preparedServer := p.prepared, p.server
	p.mu.Unlock()
	if !prepared || preparedServer != server {
		return fmt.Errorf("dockerweb: commit %s without a matching Windows prepare", server)
	}
	if err := p.services.Validate(ctx, string(server)); err != nil {
		return errors.Join(fmt.Errorf("dockerweb: validate WSL %s: %w", server, err), p.Rollback(ctx))
	}
	if err := p.services.Reload(ctx, string(server)); err != nil {
		return errors.Join(fmt.Errorf("dockerweb: reload WSL %s: %w", server, err), p.Rollback(ctx))
	}
	if p.router != nil {
		if err := p.router.UpdateDockerWebRoutes(ctx, true, "127.0.0.1", windowsPrivateHTTPPort); err != nil {
			return errors.Join(fmt.Errorf("dockerweb: update router targets for %s: %w", server, err), p.Rollback(ctx))
		}
	}
	if err := p.state.SaveServingState(ctx, string(server), "docker"); err != nil {
		return errors.Join(fmt.Errorf("dockerweb: save Windows Docker serving state: %w", err), p.Rollback(ctx))
	}
	p.mu.Lock()
	p.prepared = false
	p.server = ""
	p.mu.Unlock()
	return nil
}

// Rollback stops a failed private WSL server transition and restores only the
// saved control-plane serving state. It never changes daemon listeners.
func (p *WindowsProvider) Rollback(ctx context.Context) error {
	p.mu.Lock()
	if !p.prepared {
		p.mu.Unlock()
		return nil
	}
	server := p.server
	mode, provider := p.previousMode, p.previousFrom
	p.prepared = false
	p.server = ""
	p.mu.Unlock()
	var errs []error
	if err := p.services.Deactivate(ctx, string(server)); err != nil {
		errs = append(errs, fmt.Errorf("dockerweb: stop failed WSL %s: %w", server, err))
	}
	if p.router != nil {
		if err := p.router.UpdateDockerWebRoutes(ctx, false, "", 0); err != nil {
			errs = append(errs, fmt.Errorf("dockerweb: restore router targets: %w", err))
		}
	}
	if provider == "docker" && mode != "" && mode != "builtin" && mode != string(server) {
		if p.switchTo != nil {
			if err := p.switchTo.StartActiveServer(ctx, mode); err != nil {
				errs = append(errs, fmt.Errorf("dockerweb: restore previous %s: %w", mode, err))
			} else if p.router != nil {
				if err := p.router.UpdateDockerWebRoutes(ctx, true, "127.0.0.1", windowsPrivateHTTPPort); err != nil {
					errs = append(errs, fmt.Errorf("dockerweb: restore previous %s route: %w", mode, err))
				}
			}
		} else if err := p.services.Activate(ctx, mode); err != nil {
			errs = append(errs, fmt.Errorf("dockerweb: restore previous %s: %w", mode, err))
		}
	}
	if err := p.state.SaveServingState(ctx, mode, provider); err != nil {
		errs = append(errs, fmt.Errorf("dockerweb: restore Windows serving state: %w", err))
	}
	return errors.Join(errs...)
}

// StopActiveServer stops the active Docker web server and restores the router targets.
func (p *WindowsProvider) StopActiveServer(ctx context.Context, serverName string) error {
	server, err := parseServer(serverName)
	if err != nil {
		return err
	}
	var errs []error
	if p.switchTo != nil {
		if err := p.switchTo.StopActiveServer(ctx, string(server)); err != nil {
			errs = append(errs, fmt.Errorf("dockerweb: stop %s: %w", server, err))
		}
	} else if err := p.services.Deactivate(ctx, string(server)); err != nil {
		errs = append(errs, fmt.Errorf("dockerweb: stop %s: %w", server, err))
	}
	if p.router != nil {
		if err := p.router.UpdateDockerWebRoutes(ctx, false, "", 0); err != nil {
			errs = append(errs, fmt.Errorf("dockerweb: restore router targets after stop: %w", err))
		}
	}
	return errors.Join(errs...)
}

// StartActiveServer starts the Docker web server and updates the router targets.
func (p *WindowsProvider) StartActiveServer(ctx context.Context, serverName string) error {
	server, err := parseServer(serverName)
	if err != nil {
		return err
	}
	if p.switchTo != nil {
		if err := p.switchTo.StartActiveServer(ctx, string(server)); err != nil {
			return fmt.Errorf("dockerweb: start %s: %w", server, err)
		}
	} else if err := p.services.Activate(ctx, string(server)); err != nil {
		return fmt.Errorf("dockerweb: start %s: %w", server, err)
	}
	if p.router != nil {
		if err := p.router.UpdateDockerWebRoutes(ctx, true, "127.0.0.1", windowsPrivateHTTPPort); err != nil {
			return fmt.Errorf("dockerweb: update router targets after start: %w", err)
		}
	}
	return nil
}

// PrivateBackend describes the HTTP-only endpoint that the Windows daemon
// proxies to after terminating public TLS.
func (*WindowsProvider) PrivateBackend() (host string, port int, tls bool) {
	return "127.0.0.1", windowsPrivateHTTPPort, false
}
