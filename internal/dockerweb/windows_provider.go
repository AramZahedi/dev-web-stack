package dockerweb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
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

// WindowsTargetSource builds the typed WSL backend target set for one active
// Docker web server. Site data and certificates are still agent-owned.
type WindowsTargetSource interface {
	PrivateWebTargets(ctx context.Context, server Server) ([]agentproto.WebTarget, error)
}

// AgentWindowsPrivateSyncer bridges the Windows provider to the typed agent
// web endpoint without reading or writing service files over NTFS/UNC.
type AgentWindowsPrivateSyncer struct {
	agent   privateWebAgent
	targets WindowsTargetSource
}

// NewAgentWindowsPrivateSyncer constructs the authenticated-agent web syncer.
func NewAgentWindowsPrivateSyncer(agent privateWebAgent, targets WindowsTargetSource) (*AgentWindowsPrivateSyncer, error) {
	if agent == nil || targets == nil {
		return nil, errors.New("dockerweb: Windows agent sync requires an agent client and target source")
	}
	return &AgentWindowsPrivateSyncer{agent: agent, targets: targets}, nil
}

// EnsurePrivateBase validates the fixed server; the agent seeds the private
// base atomically during the next SyncPrivate call.
func (*AgentWindowsPrivateSyncer) EnsurePrivateBase(server Server) error { return server.Validate() }

// SyncPrivate sends only typed targets. The agent owns all WSL config files.
func (s *AgentWindowsPrivateSyncer) SyncPrivate(ctx context.Context, server Server) error {
	if err := server.Validate(); err != nil {
		return err
	}
	targets, err := s.targets.PrivateWebTargets(ctx, server)
	if err != nil {
		return fmt.Errorf("dockerweb: build private %s targets: %w", server, err)
	}
	if _, err := s.agent.SyncWeb(ctx, agentproto.WebTargetsRequest{Targets: targets}); err != nil {
		return fmt.Errorf("dockerweb: sync private %s targets through agent: %w", server, err)
	}
	return nil
}

// WindowsProviderConfig wires the Windows/WSL private-HTTP serving adapter.
type WindowsProviderConfig struct {
	State    ServingState
	Syncer   WindowsPrivateSyncer
	Services ServiceControl
}

// WindowsProvider switches only the private WSL web service. The daemon stays
// the owner of public routing, public TLS, and devstack.test.
type WindowsProvider struct {
	state    ServingState
	syncer   WindowsPrivateSyncer
	services ServiceControl

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
	return &WindowsProvider{state: cfg.State, syncer: cfg.Syncer, services: cfg.Services}, nil
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
	if err := p.state.SaveServingState(ctx, mode, provider); err != nil {
		errs = append(errs, fmt.Errorf("dockerweb: restore Windows serving state: %w", err))
	}
	return errors.Join(errs...)
}

// PrivateBackend describes the HTTP-only endpoint that the Windows daemon
// proxies to after terminating public TLS.
func (*WindowsProvider) PrivateBackend() (host string, port int, tls bool) {
	return "127.0.0.1", windowsPrivateHTTPPort, false
}
