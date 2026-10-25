package servicecontrol

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/dockerweb"
	"github.com/AramZahedi/dev-web-stack/internal/services"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
)

// Compile-time integration contracts used by the daemon sequence.
var _ WebLifecycle = (*dockerweb.Provider)(nil)
var _ sites.DockerSwitch = (*WebSwitch)(nil)

// ErrWebServerActive reports that the active Docker web server cannot be
// deactivated or uninstalled until the serving provider changes.
var ErrWebServerActive = errors.New("the active docker web server cannot be deactivated or uninstalled")

// WebLifecycle is the dockerweb provider surface used by the service preparer.
type WebLifecycle interface {
	EnsureBase(server string) error
	Prepare(ctx context.Context, server string) error
	Commit(ctx context.Context, server string) error
	Rollback(ctx context.Context) error
}

// WebPreparer wires nginx/apache service lifecycle actions into the Docker
// serving provider. Install seeds base configuration; activation performs the
// transactional provider switch.
type WebPreparer struct {
	lifecycle WebLifecycle
	guard     DeactivationGuard
}

// NewWebPreparer returns a preparer for a Docker web-server provider. An
// optional guard refuses to stop or uninstall the active Docker web server.
func NewWebPreparer(lifecycle WebLifecycle, guards ...DeactivationGuard) *WebPreparer {
	preparer := &WebPreparer{lifecycle: lifecycle}
	if len(guards) > 0 {
		preparer.guard = guards[0]
	}
	return preparer
}

// NewWebPreparers returns the preparer map for the nginx and apache IDs.
func NewWebPreparers(lifecycle WebLifecycle, guards ...DeactivationGuard) map[services.ID]Preparer {
	preparer := NewWebPreparer(lifecycle, guards...)
	return map[services.ID]Preparer{
		services.IDNginx:  preparer,
		services.IDApache: preparer,
	}
}

// PrepareInstall seeds the service base config and directory tree.
func (p *WebPreparer) PrepareInstall(_ context.Context, svc services.Service) error {
	server, err := webServerName(svc.ID)
	if err != nil {
		return err
	}
	return p.lifecycle.EnsureBase(server)
}

// PrepareActivate stops the builtin runtime and renders the Docker config set.
func (p *WebPreparer) PrepareActivate(ctx context.Context, svc services.Service) error {
	server, err := webServerName(svc.ID)
	if err != nil {
		return err
	}
	return p.lifecycle.Prepare(ctx, server)
}

// CommitActivate validates, persists the docker serving state, and reloads.
func (p *WebPreparer) CommitActivate(ctx context.Context, svc services.Service) error {
	server, err := webServerName(svc.ID)
	if err != nil {
		return err
	}
	return p.lifecycle.Commit(ctx, server)
}

// CleanupActivate rolls back every side effect of a failed activation.
func (p *WebPreparer) CleanupActivate(ctx context.Context, _ services.Service, _ error) error {
	return p.lifecycle.Rollback(ctx)
}

// Credentials is unsupported for web servers.
func (p *WebPreparer) Credentials(context.Context, services.ID) (Credentials, error) {
	return Credentials{}, fmt.Errorf("%w: web servers have no credentials", ErrCredentialsUnsupported)
}

// RotateCredentials is unsupported for web servers.
func (p *WebPreparer) RotateCredentials(context.Context, services.ID) (Credentials, error) {
	return Credentials{}, fmt.Errorf("%w: web servers have no credentials", ErrCredentialsUnsupported)
}

// CanDeactivate delegates to the optional active-server guard.
func (p *WebPreparer) CanDeactivate(ctx context.Context, id services.ID) error {
	if p.guard == nil {
		return nil
	}
	return p.guard.CanDeactivate(ctx, id)
}

// CanUninstall delegates to the optional active-server guard.
func (p *WebPreparer) CanUninstall(ctx context.Context, id services.ID) error {
	if p.guard == nil {
		return nil
	}
	return p.guard.CanUninstall(ctx, id)
}

var _ Preparer = (*WebPreparer)(nil)
var _ DeactivationGuard = (*WebPreparer)(nil)

// WebServingState reads the persisted serving mode/provider for the guard.
type WebServingState interface {
	ServingMode() string
	ServingProvider() string
}

// WebGuard refuses deactivation or uninstall of the active Docker web server.
// It is an optional preparer capability; Manager can call it when the
// registered web preparer implements DeactivationGuard.
type WebGuard struct {
	State WebServingState
}

// DeactivationGuard is an optional capability checked by the service manager
// before deactivating or uninstalling a service.
type DeactivationGuard interface {
	CanDeactivate(ctx context.Context, id services.ID) error
	CanUninstall(ctx context.Context, id services.ID) error
}

// CanDeactivate reports whether the service may be stopped.
func (g WebGuard) CanDeactivate(_ context.Context, id services.ID) error {
	return g.check(id)
}

// CanUninstall reports whether the service may be removed.
func (g WebGuard) CanUninstall(_ context.Context, id services.ID) error {
	return g.check(id)
}

func (g WebGuard) check(id services.ID) error {
	server, err := webServerName(id)
	if err != nil {
		return nil
	}
	if g.State == nil {
		return nil
	}
	if g.State.ServingProvider() != "docker" {
		return nil
	}
	if strings.TrimSpace(g.State.ServingMode()) != server {
		return nil
	}
	return fmt.Errorf("%w: %s is serving 80/443; switch serving_mode to builtin or another web server first", ErrWebServerActive, id)
}

var _ DeactivationGuard = WebGuard{}

// WebServiceManager is the P3 service manager subset needed by the provider.
type WebServiceManager interface {
	Activate(ctx context.Context, id services.ID) error
	Deactivate(ctx context.Context, id services.ID) error
	Validate(ctx context.Context, id services.ID) error
	Reload(ctx context.Context, id services.ID) error
}

// WebServiceControl adapts the P3 manager's services.ID methods to the
// dockerweb ServiceControl interface.
type WebServiceControl struct {
	manager WebServiceManager
}

// NewWebServiceControl wraps the service manager for the Docker web provider.
// The manager may be bound later with Bind when the provider is constructed
// before the P3 manager (the manager needs the provider as a preparer).
func NewWebServiceControl(manager WebServiceManager) *WebServiceControl {
	return &WebServiceControl{manager: manager}
}

// Bind attaches the P3 manager after construction.
func (c *WebServiceControl) Bind(manager WebServiceManager) {
	c.manager = manager
}

// Activate starts a web-server container through the P3 manager.
func (c *WebServiceControl) Activate(ctx context.Context, server string) error {
	id, err := webServiceID(server)
	if err != nil {
		return err
	}
	if c.manager == nil {
		return errors.New("servicecontrol: web service manager is not bound")
	}
	return c.manager.Activate(ctx, id)
}

// Deactivate stops a web-server container through the P3 manager.
func (c *WebServiceControl) Deactivate(ctx context.Context, server string) error {
	id, err := webServiceID(server)
	if err != nil {
		return err
	}
	if c.manager == nil {
		return errors.New("servicecontrol: web service manager is not bound")
	}
	return c.manager.Deactivate(ctx, id)
}

// Validate runs the catalog validation command in the container.
func (c *WebServiceControl) Validate(ctx context.Context, server string) error {
	id, err := webServiceID(server)
	if err != nil {
		return err
	}
	if c.manager == nil {
		return errors.New("servicecontrol: web service manager is not bound")
	}
	return c.manager.Validate(ctx, id)
}

// Reload runs the catalog reload command in the container.
func (c *WebServiceControl) Reload(ctx context.Context, server string) error {
	id, err := webServiceID(server)
	if err != nil {
		return err
	}
	if c.manager == nil {
		return errors.New("servicecontrol: web service manager is not bound")
	}
	return c.manager.Reload(ctx, id)
}

func webServiceID(server string) (services.ID, error) {
	switch strings.ToLower(strings.TrimSpace(server)) {
	case "nginx":
		return services.IDNginx, nil
	case "apache":
		return services.IDApache, nil
	default:
		return "", fmt.Errorf("%w: unsupported web server %q", services.ErrNotFound, server)
	}
}

func webServerName(id services.ID) (string, error) {
	switch id {
	case services.IDNginx:
		return "nginx", nil
	case services.IDApache:
		return "apache", nil
	default:
		return "", fmt.Errorf("%w: %q is not a web server", services.ErrNotFound, id)
	}
}

// WebSwitchManager is the explicit provider-switch subset that bypasses the
// active-server deactivation guard.
type WebSwitchManager interface {
	StopForProviderSwitch(ctx context.Context, id services.ID) error
	StartForProviderSwitch(ctx context.Context, id services.ID) error
}

// WebSwitch adapts the P3 manager for the documented Docker -> builtin
// transition. It is the only path allowed to stop the active Docker web
// server without tripping WebGuard.
type WebSwitch struct {
	manager WebSwitchManager
}

// NewWebSwitch returns an unbound provider-switch adapter. Bind it after the
// P3 manager is constructed.
func NewWebSwitch(manager WebSwitchManager) *WebSwitch {
	return &WebSwitch{manager: manager}
}

// Bind attaches the P3 manager after construction.
func (w *WebSwitch) Bind(manager WebSwitchManager) {
	w.manager = manager
}

// StopActiveServer stops the active web container through the explicit switch
// path.
func (w *WebSwitch) StopActiveServer(ctx context.Context, server string) error {
	id, err := webServiceID(server)
	if err != nil {
		return err
	}
	if w.manager == nil {
		return errors.New("servicecontrol: web service manager is not bound")
	}
	return w.manager.StopForProviderSwitch(ctx, id)
}

// StartActiveServer restarts a previously active web container when the
// builtin restore fails.
func (w *WebSwitch) StartActiveServer(ctx context.Context, server string) error {
	id, err := webServiceID(server)
	if err != nil {
		return err
	}
	if w.manager == nil {
		return errors.New("servicecontrol: web service manager is not bound")
	}
	return w.manager.StartForProviderSwitch(ctx, id)
}
