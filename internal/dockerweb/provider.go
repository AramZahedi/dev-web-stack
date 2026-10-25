package dockerweb

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// PortProbe checks whether a loopback TCP port is already accepting
// connections. The Docker provider uses it after stopping the builtin
// forwarders to detect a native nginx/Apache server that still owns 80/443.
type PortProbe interface {
	InUse(ctx context.Context, port int) (bool, error)
}

// LoopbackPortProbe is the production port probe. A successful loopback
// connection means another process owns the port; connection failures mean it
// is free.
type LoopbackPortProbe struct {
	Timeout time.Duration
}

// InUse implements PortProbe.
func (p LoopbackPortProbe) InUse(ctx context.Context, port int) (bool, error) {
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 250 * time.Millisecond
	}
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false, nil
	}
	_ = conn.Close()
	return true, nil
}

// BuiltinControl stops and restores the native builtin proxy and privileged
// 80/443 forwarders. sites.Service implements it.
type BuiltinControl interface {
	StopForDocker(ctx context.Context) error
	StartAfterDocker(ctx context.Context) error
}

// ServiceControl activates, stops, validates, and reloads a web-server service
// container. servicecontrol.WebServiceControl adapts the P3 service manager.
type ServiceControl interface {
	Activate(ctx context.Context, server string) error
	Deactivate(ctx context.Context, server string) error
	Validate(ctx context.Context, server string) error
	Reload(ctx context.Context, server string) error
}

// Syncer writes the container base and generated configuration.
type Syncer interface {
	EnsureBase(server Server) error
	Sync(ctx context.Context, server Server) error
}

// ServingState reads and persists serving_mode/serving_provider. sites.Service
// implements it so config.yaml and SQLite stay authoritative.
type ServingState interface {
	ServingMode() string
	ServingProvider() string
	SaveServingState(ctx context.Context, mode, provider string) error
}

// ProviderConfig wires the Docker serving switch to its collaborators.
type ProviderConfig struct {
	State    ServingState
	Builtin  BuiltinControl
	Syncer   Syncer
	Services ServiceControl
	Probe    PortProbe
}

// Provider performs the transactional serving-provider switch:
// prepare (stop builtin, render config), commit (validate, persist, reload), or
// rollback (stop the container, restore the previous mode).
type Provider struct {
	state    ServingState
	builtin  BuiltinControl
	syncer   Syncer
	services ServiceControl
	probe    PortProbe

	mu           sync.Mutex
	prepared     bool
	server       string
	prevMode     string
	prevProvider string
}

// NewProvider validates its collaborators.
func NewProvider(cfg ProviderConfig) (*Provider, error) {
	if cfg.State == nil {
		return nil, errors.New("dockerweb: serving state is required")
	}
	if cfg.Builtin == nil {
		return nil, errors.New("dockerweb: builtin control is required")
	}
	if cfg.Syncer == nil {
		return nil, errors.New("dockerweb: config syncer is required")
	}
	if cfg.Services == nil {
		return nil, errors.New("dockerweb: service control is required")
	}
	return &Provider{
		state:    cfg.State,
		builtin:  cfg.Builtin,
		syncer:   cfg.Syncer,
		services: cfg.Services,
		probe:    cfg.Probe,
	}, nil
}

// EnsureBase scaffolds the base configuration for a web service. It is called
// by the install preparer before a container is pulled.
func (p *Provider) EnsureBase(server string) error {
	s, err := parseServer(server)
	if err != nil {
		return err
	}
	return p.syncer.EnsureBase(s)
}

// Prepare stops the builtin proxy/forwarders and renders the complete Docker
// config set. It refuses to displace a native external server and restores the
// builtin runtime if any step fails.
func (p *Provider) Prepare(ctx context.Context, serverName string) error {
	server, err := parseServer(serverName)
	if err != nil {
		return err
	}
	p.mu.Lock()
	if p.prepared {
		p.mu.Unlock()
		return errors.New("dockerweb: a provider transition is already prepared")
	}
	mode := p.state.ServingMode()
	provider := p.state.ServingProvider()
	if strings.TrimSpace(mode) == "" {
		mode = "builtin"
	}
	if strings.TrimSpace(provider) == "" {
		provider = "native"
	}
	p.mu.Unlock()

	if provider == "native" && mode != "builtin" {
		return fmt.Errorf("dockerweb: native %s is active on 80/443; stop it before activating the Docker %s container", mode, server)
	}

	if err := p.builtin.StopForDocker(ctx); err != nil {
		restoreErr := p.builtin.StartAfterDocker(ctx)
		if restoreErr != nil {
			return errors.Join(fmt.Errorf("dockerweb: stop builtin serving: %w", err), fmt.Errorf("dockerweb: restore builtin serving: %w", restoreErr))
		}
		return fmt.Errorf("dockerweb: stop builtin serving: %w", err)
	}
	fail := func(cause error) error {
		if restoreErr := p.builtin.StartAfterDocker(ctx); restoreErr != nil {
			return errors.Join(cause, fmt.Errorf("dockerweb: restore builtin serving: %w", restoreErr))
		}
		return cause
	}
	if provider != "docker" {
		if err := p.checkPorts(ctx, server); err != nil {
			return fail(err)
		}
	}
	if err := p.syncer.EnsureBase(server); err != nil {
		return fail(fmt.Errorf("dockerweb: ensure %s base configuration: %w", server, err))
	}
	if err := p.syncer.Sync(ctx, server); err != nil {
		return fail(fmt.Errorf("dockerweb: sync %s configuration: %w", server, err))
	}

	p.mu.Lock()
	p.prepared = true
	p.server = string(server)
	p.prevMode = mode
	p.prevProvider = provider
	p.mu.Unlock()
	return nil
}

func (p *Provider) checkPorts(ctx context.Context, server Server) error {
	if p.probe == nil {
		return nil
	}
	for _, port := range []int{80, 443} {
		busy, err := p.probe.InUse(ctx, port)
		if err != nil {
			return fmt.Errorf("dockerweb: probe port %d: %w", port, err)
		}
		if busy {
			return fmt.Errorf("dockerweb: port %d is still in use after stopping the builtin forwarders; stop any native nginx/Apache server before activating the Docker %s container", port, server)
		}
	}
	return nil
}

// Commit validates the started container, persists serving_mode/serving_provider,
// and reloads. Any failure rolls the whole transition back.
func (p *Provider) Commit(ctx context.Context, serverName string) error {
	server, err := parseServer(serverName)
	if err != nil {
		return err
	}
	p.mu.Lock()
	prepared := p.prepared
	preparedServer := p.server
	p.mu.Unlock()
	if !prepared || preparedServer != string(server) {
		return fmt.Errorf("dockerweb: commit %s without a matching prepare", server)
	}

	if err := p.services.Validate(ctx, string(server)); err != nil {
		rollbackErr := p.Rollback(ctx)
		return errors.Join(fmt.Errorf("dockerweb: validate %s configuration: %w", server, err), rollbackErr)
	}
	if err := p.services.Reload(ctx, string(server)); err != nil {
		rollbackErr := p.Rollback(ctx)
		return errors.Join(fmt.Errorf("dockerweb: reload %s: %w", server, err), rollbackErr)
	}
	// Persist only after validation and reload succeed. A failed save is
	// recovered by Rollback while the previous serving state still identifies
	// the previous owner of 80/443.
	if err := p.state.SaveServingState(ctx, string(server), "docker"); err != nil {
		rollbackErr := p.Rollback(ctx)
		return errors.Join(fmt.Errorf("dockerweb: save docker serving state: %w", err), rollbackErr)
	}

	p.mu.Lock()
	p.prepared = false
	p.server = ""
	p.mu.Unlock()
	return nil
}

// Rollback stops the target container if it was started, restores the builtin
// runtime or the previous Docker web server, and restores the saved serving
// state. Rollback is safe to call after a failed Prepare or Commit.
func (p *Provider) Rollback(ctx context.Context) error {
	p.mu.Lock()
	prepared := p.prepared
	server := p.server
	prevMode := p.prevMode
	prevProvider := p.prevProvider
	p.prepared = false
	p.server = ""
	p.mu.Unlock()
	if !prepared {
		return nil
	}

	var errs []error
	if server != "" {
		if err := p.services.Deactivate(ctx, string(server)); err != nil {
			errs = append(errs, fmt.Errorf("dockerweb: stop failed %s container: %w", server, err))
		}
	}
	switch {
	case prevProvider == "docker" && prevMode != "" && prevMode != "builtin":
		if err := p.services.Activate(ctx, prevMode); err != nil {
			errs = append(errs, fmt.Errorf("dockerweb: restore previous docker %s: %w", prevMode, err))
		}
	case prevMode == "builtin" || prevMode == "":
		if err := p.builtin.StartAfterDocker(ctx); err != nil {
			errs = append(errs, fmt.Errorf("dockerweb: restore builtin serving: %w", err))
		}
	default:
		// A native external server was refused during Prepare, so this branch
		// is only reachable for a corrupted saved state. Leave the previous
		// config intact rather than stopping a server devstack does not manage.
	}
	if err := p.state.SaveServingState(ctx, prevMode, prevProvider); err != nil {
		errs = append(errs, fmt.Errorf("dockerweb: restore serving state: %w", err))
	}
	return errors.Join(errs...)
}

func parseServer(server string) (Server, error) {
	value := Server(strings.ToLower(strings.TrimSpace(server)))
	if err := value.Validate(); err != nil {
		return "", err
	}
	return value, nil
}
