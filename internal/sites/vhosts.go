package sites

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/AramZahedi/dev-web-stack/internal/certs"
	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/proxy"
	"github.com/AramZahedi/dev-web-stack/internal/vhost"
)

// servingMode returns the configured serving mode, treating an unset value as
// builtin. Config.Default always sets it; the fallback keeps zero-value Config
// values usable in tests.
func (s *Service) servingMode() string {
	mode := strings.TrimSpace(s.deps.Config.ServingMode)
	if mode == "" {
		return config.ServingModeBuiltin
	}
	return mode
}

func (s *Service) externalServing() bool {
	return s.servingMode() != config.ServingModeBuiltin
}

// servingProvider returns the configured provider, treating an unset value as
// native for compatibility with pre-provider installations.
func (s *Service) servingProvider() string {
	provider := strings.TrimSpace(s.deps.Config.ServingProvider)
	if provider == "" {
		return config.ServingProviderNative
	}
	return provider
}

// dockerServing reports whether the external mode is owned by a managed
// service container rather than the native vhost helper.
func (s *Service) dockerServing() bool {
	return s.servingProvider() == config.ServingProviderDocker
}

// ServingProvider returns the effective serving provider.
func (s *Service) ServingProvider() string {
	if s == nil {
		return config.ServingProviderNative
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.servingProvider()
}

// siteNeedsBackend reports whether a site kind is served by a supervised
// process in the current mode. External static sites have no process.
func (s *Service) siteNeedsBackend(site model.Site) bool {
	return site.Kind != model.SiteStatic || !s.externalServing()
}

// ServingMode returns the currently effective serving mode. It reflects a
// runtime switch saved by SetServingMode.
func (s *Service) ServingMode() string {
	if s == nil {
		return config.ServingModeBuiltin
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.servingMode()
}

func builtinVhostStatus() helperproto.VhostStatusResponse {
	return helperproto.VhostStatusResponse{Mode: config.ServingModeBuiltin, ConfigOK: true}
}

// VhostStatus reports the helper's persisted vhost state. A daemon without a
// vhost-capable helper reports builtin state while the configured mode is
// builtin; an external mode surfaces the helper error.
func (s *Service) VhostStatus(ctx context.Context) (helperproto.VhostStatusResponse, error) {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.vhostStatusLocked(ctx)
}

func (s *Service) vhostStatusLocked(ctx context.Context) (helperproto.VhostStatusResponse, error) {
	if s.dockerServing() {
		return helperproto.VhostStatusResponse{
			Mode:     s.servingMode(),
			Server:   s.servingMode(),
			ConfigOK: true,
		}, nil
	}
	if s.deps.Vhosts == nil {
		return builtinVhostStatus(), nil
	}
	status, err := s.deps.Vhosts.VhostStatus(ctx)
	if err != nil {
		if errors.Is(err, helperproto.ErrNotImplemented) && !s.externalServing() {
			return builtinVhostStatus(), nil
		}
		return helperproto.VhostStatusResponse{}, err
	}
	if strings.TrimSpace(status.Mode) == "" {
		status.Mode = config.ServingModeBuiltin
	}
	return status, nil
}

// SyncVhosts installs a complete vhost set when an external mode is active.
// Builtin mode is a no-op.
func (s *Service) SyncVhosts(ctx context.Context) error {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.syncVhostsLocked(ctx, "")
}

func (s *Service) syncVhostsLocked(ctx context.Context, excludeID string) error {
	if s.dockerServing() {
		if s.deps.DockerSyncer == nil {
			return errors.New("sites: docker web config syncer is not configured")
		}
		mode := s.servingMode()
		if mode == config.ServingModeBuiltin {
			return errors.New("sites: docker serving provider requires serving_mode nginx or apache")
		}
		if err := s.deps.DockerSyncer.Sync(ctx, mode); err != nil {
			return fmt.Errorf("sites: sync docker %s vhosts: %w", mode, err)
		}
		return nil
	}
	if !s.externalServing() {
		return nil
	}
	if s.deps.Vhosts == nil {
		return errors.New("sites: vhost helper is not configured")
	}
	mode := s.servingMode()
	req, err := s.buildVhostRequestLocked(ctx, mode, excludeID)
	if err != nil {
		return err
	}
	// A plain sync only installs configs; it does not stop forwarders. If the
	// helper is still builtin (for example after a failed startup reconcile),
	// use SetServingMode so 80/443 ownership is handled atomically.
	helperMode, err := s.helperModeLocked(ctx)
	if err != nil {
		return err
	}
	if helperMode != mode {
		if _, err := s.deps.Vhosts.SetServingMode(ctx, mode, req); err != nil {
			return fmt.Errorf("sites: switch external vhosts to %q: %w", mode, err)
		}
		return nil
	}
	if err := s.deps.Vhosts.SyncVhosts(ctx, req); err != nil {
		return fmt.Errorf("sites: sync external vhosts: %w", err)
	}
	return nil
}

// ReconcileServingMode makes the helper agree with config.yaml. Configuration
// wins: the helper is switched without saving config, and a matching external
// mode receives a full sync. It never starts or stops the proxy or static
// backends; daemon startup owns that ordering.
func (s *Service) ReconcileServingMode(ctx context.Context) error {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	mode := s.servingMode()
	if s.dockerServing() {
		if s.deps.DockerSyncer == nil {
			return errors.New("sites: docker web config syncer is not configured")
		}
		if err := s.deps.DockerSyncer.Sync(ctx, mode); err != nil {
			return fmt.Errorf("sites: reconcile docker %s vhosts: %w", mode, err)
		}
		return nil
	}
	if s.deps.Vhosts == nil {
		if s.externalServing() {
			return errors.New("sites: vhost helper is not configured")
		}
		return nil
	}

	status, err := s.deps.Vhosts.VhostStatus(ctx)
	if err != nil {
		if errors.Is(err, helperproto.ErrNotImplemented) && mode == config.ServingModeBuiltin {
			return nil
		}
		return fmt.Errorf("sites: read vhost status: %w", err)
	}
	helperMode := strings.TrimSpace(status.Mode)
	if helperMode == "" {
		helperMode = config.ServingModeBuiltin
	}
	if helperMode == mode {
		if mode == config.ServingModeBuiltin {
			return nil
		}
		return s.syncVhostsLocked(ctx, "")
	}

	req := helperproto.VHostSyncRequest{}
	if mode != config.ServingModeBuiltin {
		req, err = s.buildVhostRequestLocked(ctx, mode, "")
		if err != nil {
			return err
		}
	}
	if _, err := s.deps.Vhosts.SetServingMode(ctx, mode, req); err != nil {
		return fmt.Errorf("sites: reconcile serving mode %q: %w", mode, err)
	}
	return nil
}

// SetServingMode performs a runtime switch and persists it only after the
// helper succeeds. Every failure restores the helper, proxy, static backends,
// and in-memory configuration to their previous state.
func (s *Service) SetServingMode(ctx context.Context, mode string) error {
	ctx = nonNilContext(ctx)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if !config.ValidServingMode(mode) {
		return fmt.Errorf("sites: unsupported serving mode %q", mode)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.dockerServing() {
		return errors.New("sites: serving_provider is docker; activate or roll back the web-server service instead of changing serving_mode directly")
	}

	current := s.servingMode()
	if s.deps.Vhosts == nil {
		if mode == current && mode == config.ServingModeBuiltin {
			return nil
		}
		return errors.New("sites: vhost helper is not configured")
	}
	if strings.TrimSpace(s.deps.Config.Home) == "" {
		return errors.New("sites: config home is not set")
	}

	// config.yaml is not trustworthy on its own: the helper may have been
	// switched out of band, or a previous save may have failed after the helper
	// changed. Read the live helper mode under the lock and repair a mismatch
	// with SetServingMode even when the requested mode equals the config.
	helperMode, err := s.helperModeLocked(ctx)
	if err != nil {
		return err
	}

	var target helperproto.VHostSyncRequest
	if mode != config.ServingModeBuiltin {
		target, err = s.buildVhostRequestLocked(ctx, mode, "")
		if err != nil {
			return err
		}
	}

	if mode == current {
		if helperMode == mode {
			if current == config.ServingModeBuiltin {
				return nil
			}
			return s.syncVhostsLocked(ctx, "")
		}
		// The config already names the target but the helper disagrees.
		if mode == config.ServingModeBuiltin {
			return s.switchExternalToBuiltin(ctx, helperMode)
		}
		return s.switchExternal(ctx, helperMode, mode, target)
	}

	switch {
	case current == config.ServingModeBuiltin:
		return s.switchBuiltinToExternal(ctx, mode, target)
	case mode == config.ServingModeBuiltin:
		return s.switchExternalToBuiltin(ctx, current)
	default:
		return s.switchExternal(ctx, current, mode, target)
	}
}

// helperModeLocked returns the helper's persisted mode, treating an old helper
// without vhost support as builtin. The caller must hold s.mu.
func (s *Service) helperModeLocked(ctx context.Context) (string, error) {
	status, err := s.deps.Vhosts.VhostStatus(ctx)
	if err != nil {
		if errors.Is(err, helperproto.ErrNotImplemented) {
			return config.ServingModeBuiltin, nil
		}
		return "", fmt.Errorf("sites: read vhost status: %w", err)
	}
	mode := strings.TrimSpace(status.Mode)
	if mode == "" {
		return config.ServingModeBuiltin, nil
	}
	return mode, nil
}

func (s *Service) switchBuiltinToExternal(ctx context.Context, mode string, req helperproto.VHostSyncRequest) error {
	if err := s.stopStaticBackendsLocked(ctx); err != nil {
		return err
	}

	if s.deps.Proxy != nil {
		if err := s.deps.Proxy.Stop(ctx); err != nil {
			restore := s.restoreBuiltinRuntimeLocked(ctx)
			return errors.Join(fmt.Errorf("sites: stop built-in proxy: %w", err), restore)
		}
	}

	if _, err := s.deps.Vhosts.SetServingMode(ctx, mode, req); err != nil {
		restore := s.restoreBuiltinRuntimeLocked(ctx)
		return errors.Join(fmt.Errorf("sites: switch helper to %q: %w", mode, err), restore)
	}

	if err := s.saveServingModeLocked(ctx, mode); err != nil {
		var errs []error
		errs = append(errs, fmt.Errorf("sites: save serving mode: %w", err))
		// Start the built-in proxy before restoring helper builtin. The helper
		// enables the 80/443 forwarders immediately, so reversing that order
		// would leave privileged ports bound with no loopback proxy behind them.
		if proxyErr := s.startBuiltinProxyLocked(ctx); proxyErr != nil {
			errs = append(errs, fmt.Errorf("rollback: start built-in proxy: %w", proxyErr))
			return errors.Join(errs...)
		}
		if _, herr := s.deps.Vhosts.SetServingMode(ctx, config.ServingModeBuiltin, helperproto.VHostSyncRequest{}); herr != nil {
			errs = append(errs, fmt.Errorf("rollback: restore helper builtin mode: %w", herr))
		}
		if restoreErr := s.startStaticBackendsLocked(ctx); restoreErr != nil {
			errs = append(errs, fmt.Errorf("rollback: restart static backends: %w", restoreErr))
		}
		return errors.Join(errs...)
	}
	return nil
}

func (s *Service) switchExternalToBuiltin(ctx context.Context, previous string) error {
	if err := s.startBuiltinProxyLocked(ctx); err != nil {
		return fmt.Errorf("sites: start built-in proxy: %w", err)
	}
	if err := s.rebuildRoutesLocked(ctx); err != nil {
		var errs []error
		errs = append(errs, fmt.Errorf("sites: publish routes before builtin switch: %w", err))
		if s.deps.Proxy != nil {
			if stopErr := s.deps.Proxy.Stop(ctx); stopErr != nil {
				errs = append(errs, fmt.Errorf("rollback: stop built-in proxy: %w", stopErr))
			}
		}
		return errors.Join(errs...)
	}

	if _, err := s.deps.Vhosts.SetServingMode(ctx, config.ServingModeBuiltin, helperproto.VHostSyncRequest{}); err != nil {
		var errs []error
		errs = append(errs, fmt.Errorf("sites: switch helper to builtin: %w", err))
		if s.deps.Proxy != nil {
			if stopErr := s.deps.Proxy.Stop(ctx); stopErr != nil {
				errs = append(errs, fmt.Errorf("rollback: stop built-in proxy: %w", stopErr))
			}
		}
		return errors.Join(errs...)
	}

	if err := s.saveServingModeLocked(ctx, config.ServingModeBuiltin); err != nil {
		return errors.Join(
			fmt.Errorf("sites: save serving mode: %w", err),
			s.restoreExternalModeLocked(ctx, previous),
		)
	}

	if err := s.startStaticBackendsLocked(ctx); err != nil {
		return errors.Join(
			fmt.Errorf("sites: start static backends: %w", err),
			s.restoreExternalModeLocked(ctx, previous),
			s.stopStaticBackendsLocked(ctx),
		)
	}
	if err := s.rebuildRoutesLocked(ctx); err != nil {
		return errors.Join(
			fmt.Errorf("sites: publish routes after builtin switch: %w", err),
			s.restoreExternalModeLocked(ctx, previous),
			s.stopStaticBackendsLocked(ctx),
		)
	}
	return nil
}

func (s *Service) switchExternal(ctx context.Context, previous, mode string, req helperproto.VHostSyncRequest) error {
	if _, err := s.deps.Vhosts.SetServingMode(ctx, mode, req); err != nil {
		return fmt.Errorf("sites: switch helper to %q: %w", mode, err)
	}
	if err := s.saveServingModeLocked(ctx, mode); err != nil {
		errs := []error{fmt.Errorf("sites: save serving mode: %w", err)}
		// Mismatch repair starts with config already naming the target while
		// the helper is still builtin. Restoring the helper to builtin here
		// would enable 80/443 forwarders without the built-in proxy (the daemon
		// is configured external), so leave the successfully switched external
		// helper in place when config already matches the target.
		if s.servingMode() == mode {
			return errors.Join(errs...)
		}
		oldReq, buildErr := s.buildVhostRequestLocked(ctx, previous, "")
		if buildErr != nil {
			errs = append(errs, fmt.Errorf("rollback: build %q request: %w", previous, buildErr))
		} else if _, herr := s.deps.Vhosts.SetServingMode(ctx, previous, oldReq); herr != nil {
			errs = append(errs, fmt.Errorf("rollback: restore helper %q mode: %w", previous, herr))
		}
		return errors.Join(errs...)
	}
	return nil
}

// restoreExternalModeLocked switches the helper back to previous and stops the
// built-in proxy. Configuration is restored when possible.
func (s *Service) restoreExternalModeLocked(ctx context.Context, previous string) error {
	var errs []error
	req, err := s.buildVhostRequestLocked(ctx, previous, "")
	if err != nil {
		errs = append(errs, fmt.Errorf("rollback: build %q request: %w", previous, err))
	} else if _, err := s.deps.Vhosts.SetServingMode(ctx, previous, req); err != nil {
		errs = append(errs, fmt.Errorf("rollback: restore helper %q mode: %w", previous, err))
	}
	if s.deps.Proxy != nil {
		if err := s.deps.Proxy.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("rollback: stop built-in proxy: %w", err))
		}
	}
	if err := s.saveServingModeLocked(ctx, previous); err != nil {
		errs = append(errs, fmt.Errorf("rollback: restore saved serving mode: %w", err))
	}
	return errors.Join(errs...)
}

func (s *Service) saveServingModeLocked(ctx context.Context, mode string) error {
	return s.saveServingStateLocked(ctx, mode, s.servingProvider())
}

// SaveServingState persists serving_mode and serving_provider together. It is
// used by the Docker provider's commit and rollback paths.
func (s *Service) SaveServingState(ctx context.Context, mode, provider string) error {
	ctx = nonNilContext(ctx)
	mode = strings.ToLower(strings.TrimSpace(mode))
	provider = strings.ToLower(strings.TrimSpace(provider))
	if mode == "" {
		mode = config.ServingModeBuiltin
	}
	if provider == "" {
		provider = config.ServingProviderNative
	}
	if !config.ValidServingMode(mode) {
		return fmt.Errorf("sites: unsupported serving mode %q", mode)
	}
	if !config.ValidServingProvider(provider) {
		return fmt.Errorf("sites: unsupported serving provider %q", provider)
	}
	if provider == config.ServingProviderDocker && mode == config.ServingModeBuiltin {
		return errors.New("sites: serving_provider docker requires serving_mode nginx or apache")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveServingStateLocked(ctx, mode, provider)
}

func (s *Service) saveServingStateLocked(ctx context.Context, mode, provider string) error {
	cfg := s.deps.Config
	previousMode := cfg.ServingMode
	previousProvider := cfg.ServingProvider
	cfg.ServingMode = mode
	cfg.ServingProvider = provider
	if strings.TrimSpace(cfg.Home) == "" {
		return errors.New("config home is not set")
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	s.deps.Config.ServingMode = mode
	s.deps.Config.ServingProvider = provider
	if s.deps.Store != nil {
		if err := s.deps.Store.SetSetting(ctx, "serving_mode", mode); err != nil {
			// Keep config.yaml and the in-memory config consistent with the
			// last successfully persisted setting.
			s.deps.Config.ServingMode = previousMode
			s.deps.Config.ServingProvider = previousProvider
			restore := s.deps.Config
			restore.ServingMode = previousMode
			restore.ServingProvider = previousProvider
			restoreErr := restore.Save()
			return errors.Join(
				fmt.Errorf("sites: persist serving_mode setting: %w", err),
				restoreErr,
			)
		}
		if err := s.deps.Store.SetSetting(ctx, "serving_provider", provider); err != nil {
			s.deps.Config.ServingMode = previousMode
			s.deps.Config.ServingProvider = previousProvider
			restore := s.deps.Config
			restore.ServingMode = previousMode
			restore.ServingProvider = previousProvider
			restoreErr := restore.Save()
			// Best effort: put the already-written mode setting back too.
			var modeErr error
			if err := s.deps.Store.SetSetting(ctx, "serving_mode", previousMode); err != nil {
				modeErr = err
			}
			return errors.Join(
				fmt.Errorf("sites: persist serving_provider setting: %w", err),
				restoreErr,
				modeErr,
			)
		}
	}
	return nil
}

// StopForDocker stops the builtin runtime before a Docker web server takes
// ownership of 80/443: static backends, the loopback proxy, then the helper
// forwarders.
func (s *Service) StopForDocker(ctx context.Context) error {
	ctx = nonNilContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()

	var errs []error
	if err := s.stopStaticBackendsLocked(ctx); err != nil {
		errs = append(errs, fmt.Errorf("sites: stop static backends for docker: %w", err))
	}
	if s.deps.Proxy != nil {
		if err := s.deps.Proxy.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("sites: stop built-in proxy for docker: %w", err))
		}
	}
	if s.deps.Forwarders != nil {
		if err := s.deps.Forwarders.SetForwarders(ctx, s.deps.Config.HTTPPort, s.deps.Config.HTTPSPort, false); err != nil {
			errs = append(errs, fmt.Errorf("sites: disable builtin forwarders for docker: %w", err))
		}
	}
	return errors.Join(errs...)
}

// StartAfterDocker restores the builtin proxy, routes, forwarders, and static
// backends after the Docker web server has been stopped or a switch failed.
func (s *Service) StartAfterDocker(ctx context.Context) error {
	ctx = nonNilContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()

	restoreProxy := func() {
		if s.deps.Proxy != nil {
			_ = s.deps.Proxy.Stop(ctx)
		}
	}
	if err := s.startBuiltinProxyLocked(ctx); err != nil {
		return fmt.Errorf("sites: start built-in proxy after docker: %w", err)
	}
	if err := s.rebuildRoutesLocked(ctx); err != nil {
		restoreProxy()
		return fmt.Errorf("sites: publish routes after docker: %w", err)
	}
	if s.deps.Forwarders != nil {
		if err := s.deps.Forwarders.SetForwarders(ctx, s.deps.Config.HTTPPort, s.deps.Config.HTTPSPort, true); err != nil {
			restoreProxy()
			return fmt.Errorf("sites: enable builtin forwarders after docker: %w", err)
		}
	}
	if err := s.startStaticBackendsLocked(ctx); err != nil {
		restoreProxy()
		return fmt.Errorf("sites: start static backends after docker: %w", err)
	}
	return nil
}

// restoreBuiltinRuntimeLocked starts the built-in proxy and any static sites
// whose desired state is running. The configured mode is already builtin.
func (s *Service) restoreBuiltinRuntimeLocked(ctx context.Context) error {
	var errs []error
	if err := s.startBuiltinProxyLocked(ctx); err != nil {
		errs = append(errs, fmt.Errorf("rollback: start built-in proxy: %w", err))
	}
	if err := s.startStaticBackendsLocked(ctx); err != nil {
		errs = append(errs, fmt.Errorf("rollback: restart static backends: %w", err))
	}
	return errors.Join(errs...)
}

// startBuiltinProxyLocked starts the proxy unless it is already running. The
// proxy must outlive the request that triggered the mode switch;
// proxy.Server.Start watches its context and stops the listeners when it is
// canceled.
func (s *Service) startBuiltinProxyLocked(ctx context.Context) error {
	if s.deps.Proxy == nil {
		return nil
	}
	err := s.deps.Proxy.Start(context.WithoutCancel(ctx))
	if err != nil && !errors.Is(err, proxy.ErrAlreadyStarted) {
		return err
	}
	return nil
}

// stopStaticBackendsLocked stops every static site process without changing
// desired state. Static sites have no process in external mode, so Stop is
// idempotent and already-stopped sites are unaffected.
func (s *Service) stopStaticBackendsLocked(ctx context.Context) error {
	if s.deps.Backend == nil || s.deps.Store == nil {
		return nil
	}
	stored, err := s.deps.Store.ListSites(ctx)
	if err != nil {
		return fmt.Errorf("sites: list sites for mode switch: %w", err)
	}
	var stopped []model.Site
	var errs []error
	for _, site := range stored {
		if site.Kind != model.SiteStatic || site.DesiredState != model.DesiredRunning {
			continue
		}
		stopped = append(stopped, site)
		if err := s.deps.Backend.Stop(ctx, site.ID, true); err != nil {
			errs = append(errs, fmt.Errorf("sites: stop static site %q: %w", site.Name, err))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	// A partial stop must not leave some running sites stopped. Start every
	// site that was targeted, including one whose Stop reported an error.
	for _, site := range stopped {
		if _, err := s.startLocked(ctx, site); err != nil {
			errs = append(errs, fmt.Errorf("rollback: restart static site %q: %w", site.Name, err))
		}
	}
	return errors.Join(errs...)
}

// startStaticBackendsLocked starts every static site whose desired state is
// running, allocating a port when one is not already reserved.
func (s *Service) startStaticBackendsLocked(ctx context.Context) error {
	if s.deps.Store == nil {
		return nil
	}
	stored, err := s.deps.Store.ListSites(ctx)
	if err != nil {
		return fmt.Errorf("sites: list sites for mode switch: %w", err)
	}
	var errs []error
	for _, site := range stored {
		if site.Kind != model.SiteStatic || site.DesiredState != model.DesiredRunning {
			continue
		}
		if _, err := s.startLocked(ctx, site); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// buildVhostRequestLocked renders the complete helper request from the store.
// Certificates are ensured before their PEM is read.
func (s *Service) buildVhostRequestLocked(ctx context.Context, mode, excludeID string) (helperproto.VHostSyncRequest, error) {
	if s.deps.Store == nil || s.deps.Certs == nil {
		return helperproto.VHostSyncRequest{}, errors.New("sites: store and certificate manager are required")
	}
	dashboardRecord, err := s.deps.Certs.Ensure(ctx, certs.DashboardOwner, []string{dashboardHost})
	if err != nil {
		return helperproto.VHostSyncRequest{}, fmt.Errorf("sites: ensure dashboard certificate: %w", err)
	}
	dashboardCert, dashboardKey, err := s.deps.Certs.PEM(dashboardRecord)
	if err != nil {
		return helperproto.VHostSyncRequest{}, fmt.Errorf("sites: read dashboard certificate: %w", err)
	}

	stored, err := s.deps.Store.ListSites(ctx)
	if err != nil {
		return helperproto.VHostSyncRequest{}, fmt.Errorf("sites: list sites for vhosts: %w", err)
	}
	sort.Slice(stored, func(i, j int) bool {
		if stored[i].Name == stored[j].Name {
			return stored[i].ID < stored[j].ID
		}
		return stored[i].Name < stored[j].Name
	})

	sites := make([]helperproto.VHostSite, 0, len(stored))
	for _, site := range stored {
		if site.ID == excludeID {
			continue
		}
		spec, err := s.vhostSiteSpecLocked(ctx, site)
		if err == nil {
			err = validateVhostSiteForSync(site)
		}
		if err != nil {
			// One broken site must not poison the whole set. Publish it as a
			// stopped 503 vhost so every other site still syncs, and record
			// the reason as a site event.
			spec = s.degradedVhostSite(ctx, site)
			s.emitEvent(ctx, site.ID, "site.error",
				fmt.Sprintf("site %s is served as a stopped 503 vhost: %v", site.Name, err))
		}
		sites = append(sites, spec)
	}
	return helperproto.VHostSyncRequest{
		Mode:         mode,
		MaxBodyBytes: int64(s.deps.Config.MaxRequestBodyMB) << 20,
		Sites:        sites,
		Dashboard: helperproto.VHostDashboard{
			Domain:      dashboardHost,
			BackendPort: dashboardPort(s.deps.Config),
			CertPEM:     dashboardCert,
			KeyPEM:      dashboardKey,
		},
		RequestedAt: s.currentTime(),
	}, nil
}

func (s *Service) vhostSiteSpecLocked(ctx context.Context, site model.Site) (helperproto.VHostSite, error) {
	documentRoot := absoluteDocumentRoot(site)
	spec := helperproto.VHostSite{
		ID:              site.ID,
		Name:            site.Name,
		Domain:          site.Domain,
		BackendPath:     site.BackendPath,
		WWW:             site.WWW,
		Kind:            string(site.Kind),
		DocumentRoot:    documentRoot,
		TLS:             site.TLS,
		ForceHTTPS:      site.ForceHTTPS,
		SPAFallback:     site.SPAFallback,
		FrontController: site.FrontController,
		DesiredState:    string(site.DesiredState),
	}
	switch site.Kind {
	case model.SiteStatic:
		spec.BackendPort = 0
		// A running static site whose root disappeared must not break the
		// whole vhost set. Serve a branded 503 while the stored desired state
		// stays running so Get/List can report the error.
		if site.DesiredState == model.DesiredRunning {
			if err := staticRootError(site); err != nil {
				spec.DesiredState = string(model.DesiredStopped)
			}
		}
	case model.SiteNode:
		spec.BackendPort = site.HTTPPort
	case model.SitePHP:
		spec.BackendPort = site.FastCGIPort
	default:
		spec.BackendPort = site.HTTPPort
	}
	if s.deps.Certs != nil {
		rec, err := s.deps.Certs.Ensure(ctx, site.ID, siteDomains(site))
		if err != nil {
			if site.TLS {
				return helperproto.VHostSite{}, fmt.Errorf("sites: ensure certificate for %q: %w", site.Name, err)
			}
		} else if certPEM, keyPEM, err := s.deps.Certs.PEM(rec); err == nil {
			spec.CertPEM = certPEM
			spec.KeyPEM = keyPEM
		} else if site.TLS {
			return helperproto.VHostSite{}, fmt.Errorf("sites: read certificate for %q: %w", site.Name, err)
		}
	}
	return spec, nil
}

// degradedVhostSite returns a valid stopped vhost for a site whose paths are
// unusable. It keeps the host name so the branded 503 is shown instead of the
// default 404. TLS is retained when the certificate itself is still usable;
// dropping it would make https://site.test fall through to the dashboard
// certificate and show a hostname mismatch.
func (s *Service) degradedVhostSite(ctx context.Context, site model.Site) helperproto.VHostSite {
	kind := site.Kind
	if kind != model.SiteStatic && kind != model.SiteNode {
		// A stopped 503 vhost has no backend behavior, so an unsupported kind
		// (for example a legacy PHP row) can still be represented as static.
		kind = model.SiteStatic
	}
	spec := helperproto.VHostSite{
		ID:           site.ID,
		Name:         site.Name,
		Domain:       site.Domain,
		WWW:          site.WWW,
		Kind:         string(kind),
		TLS:          site.TLS,
		DesiredState: string(model.DesiredStopped),
	}
	if site.TLS && s.deps.Certs != nil {
		rec, err := s.deps.Certs.Ensure(ctx, site.ID, siteDomains(site))
		if err == nil {
			certPEM, keyPEM, err := s.deps.Certs.PEM(rec)
			if err == nil {
				spec.CertPEM = certPEM
				spec.KeyPEM = keyPEM
				return spec
			}
		}
	}
	// No usable certificate: fall back to a plain-HTTP 503 rather than
	// presenting the wrong certificate.
	spec.TLS = false
	return spec
}

// validateVhostSiteForSync checks the filesystem conditions that the helper
// enforces for a complete sync. Building the request must detect these before
// the helper rejects the whole set, so a single bad site can be degraded.
func validateVhostSiteForSync(site model.Site) error {
	if site.DesiredState != model.DesiredRunning {
		return nil
	}
	backend := strings.TrimSpace(site.BackendPath)
	if backend == "" {
		return errors.New("backend path is empty")
	}
	resolvedBackend, err := resolveVhostDirectory("backend path", backend)
	if err != nil {
		return err
	}
	if err := validateVhostBackendOwnership(resolvedBackend); err != nil {
		return err
	}
	// Node sites never use a document root; ignore it rather than degrading a
	// healthy Node vhost because of an unused field.
	if site.Kind == model.SiteNode {
		return nil
	}
	root := absoluteDocumentRoot(site)
	if root == "" {
		if site.Kind == model.SiteStatic {
			return errors.New("document root is empty")
		}
		return nil
	}
	resolvedRoot, err := resolveVhostDirectory("document root", root)
	if err != nil {
		return err
	}
	if !vhostPathContained(resolvedRoot, resolvedBackend) {
		return fmt.Errorf("document root %q resolves outside backend path %q", root, backend)
	}
	if site.Kind == model.SiteStatic {
		return staticRootError(site)
	}
	if site.Kind == model.SitePHP && (site.FastCGIPort < 43000 || site.FastCGIPort > 43999) {
		return fmt.Errorf("FastCGI port %d is outside 43000-43999", site.FastCGIPort)
	}
	return nil
}

// validateVhostBackendOwnership mirrors the helper's ownership boundary so one
// root-owned or foreign backend degrades to a 503 instead of poisoning the
// entire external vhost sync.
func validateVhostBackendOwnership(path string) error {
	if os.Geteuid() == 0 {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat backend path %q: %w", path, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("backend path %q has no ownership metadata", path)
	}
	if int(stat.Uid) == os.Geteuid() {
		return nil
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && vhostPathContained(path, home) {
		return nil
	}
	return fmt.Errorf("backend path %q is not owned by the devstack user", path)
}

func resolveVhostDirectory(kind, value string) (string, error) {
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("%s %q must be absolute", kind, value)
	}
	resolved, err := filepath.EvalSymlinks(value)
	if err != nil {
		return "", fmt.Errorf("resolve %s %q: %w", kind, value, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat %s %q: %w", kind, value, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s %q is not a directory", kind, value)
	}
	return resolved, nil
}

func vhostPathContained(path, parent string) bool {
	rel, err := filepath.Rel(parent, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// PreviewVhost renders one site's vhost without installing it or calling the
// helper. Certificate material is ensured so the rendered TLS paths are valid.
func (s *Service) PreviewVhost(ctx context.Context, ref, server string) (string, error) {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkDeps(false, false); err != nil {
		return "", err
	}
	site, err := s.resolveLocked(ctx, ref)
	if err != nil {
		return "", err
	}
	server = strings.ToLower(strings.TrimSpace(server))
	if server != string(vhost.ServerNginx) && server != string(vhost.ServerApache) {
		return "", fmt.Errorf("sites: unsupported preview server %q", server)
	}

	spec, err := s.vhostSiteSpecLocked(ctx, site)
	if err != nil {
		return "", err
	}
	dashboardRecord, err := s.deps.Certs.Ensure(ctx, certs.DashboardOwner, []string{dashboardHost})
	if err != nil {
		return "", fmt.Errorf("sites: ensure dashboard certificate: %w", err)
	}
	dashboardCert, dashboardKey, err := s.deps.Certs.PEM(dashboardRecord)
	if err != nil {
		return "", fmt.Errorf("sites: read dashboard certificate: %w", err)
	}
	files, err := vhost.Render(vhost.Set{
		Server: vhost.Server(server),
		Sites: []vhost.SiteSpec{{
			ID:              spec.ID,
			Name:            spec.Name,
			Domain:          spec.Domain,
			WWW:             spec.WWW,
			Kind:            site.Kind,
			DocumentRoot:    spec.DocumentRoot,
			BackendPort:     spec.BackendPort,
			TLS:             spec.TLS,
			ForceHTTPS:      spec.ForceHTTPS,
			SPAFallback:     spec.SPAFallback,
			FrontController: spec.FrontController,
			DesiredState:    model.DesiredState(spec.DesiredState),
			CertPEM:         spec.CertPEM,
			KeyPEM:          spec.KeyPEM,
		}},
		Dashboard: vhost.DashboardSpec{
			Domain:  dashboardHost,
			UIPort:  dashboardPort(s.deps.Config),
			CertPEM: dashboardCert,
			KeyPEM:  dashboardKey,
		},
		MaxBodyBytes: int64(s.deps.Config.MaxRequestBodyMB) << 20,
	})
	if err != nil {
		return "", fmt.Errorf("sites: render %s preview for %q: %w", server, site.Name, err)
	}
	content, ok := files[vhostFilename(site)]
	if !ok {
		return "", fmt.Errorf("sites: rendered preview for %q is missing", site.Name)
	}
	return string(content), nil
}

// absoluteDocumentRoot resolves a site document root against its backend path.
// Static and PHP sites with an empty or "." root serve the backend path itself;
// an empty Node document root stays empty.
func absoluteDocumentRoot(site model.Site) string {
	root := strings.TrimSpace(site.DocumentRoot)
	if root == "" || root == "." {
		if site.Kind == model.SiteStatic || site.Kind == model.SitePHP {
			return filepath.Clean(site.BackendPath)
		}
		return ""
	}
	if filepath.IsAbs(root) {
		return filepath.Clean(root)
	}
	return filepath.Clean(filepath.Join(site.BackendPath, root))
}

// vhostFilename mirrors vhost.siteFilename without exporting the renderer's
// internal naming helper.
func vhostFilename(site model.Site) string {
	id := site.ID
	if len(id) > 8 {
		id = id[:8]
	}
	return site.Name + "-" + id + ".conf"
}

// staticRootError verifies that an external static site's document root exists,
// is a directory, and can be opened for reading.
func staticRootError(site model.Site) error {
	path := absoluteDocumentRoot(site)
	if path == "" {
		return errors.New("document root is empty")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	if _, err := dir.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// deleteExternalLocked removes an external site: stop any process, publish the
// remaining hosts and vhost set, and only then delete its certificate and row.
// Failures restore the row, hosts, certificate, and process where possible.
func (s *Service) deleteExternalLocked(ctx context.Context, site model.Site) error {
	oldHosts, err := s.hostsFor(ctx, "", nil)
	if err != nil {
		return err
	}
	if s.siteNeedsBackend(site) {
		switch {
		case site.Kind == model.SitePHP:
			if s.deps.PHPBackend == nil {
				return fmt.Errorf("sites: stop %q: PHP backend is not configured", site.Name)
			}
			if err := s.stopPHPProcessLocked(ctx, site.ID); err != nil {
				return fmt.Errorf("sites: stop %q: %w", site.Name, err)
			}
		default:
			if s.deps.Backend != nil {
				if err := s.deps.Backend.Stop(ctx, site.ID, true); err != nil {
					return fmt.Errorf("sites: stop %q: %w", site.Name, err)
				}
			}
		}
	}

	stopped := site
	stopped.DesiredState = model.DesiredStopped
	stopped.Status = model.StatusStopped
	stopped.UpdatedAt = s.currentTime()
	if err := s.deps.Store.UpdateSite(ctx, stopped); err != nil {
		return fmt.Errorf("sites: record stopped state for %q: %w", site.Name, err)
	}

	remaining, err := s.hostsFor(ctx, site.ID, nil)
	if err != nil {
		return s.restoreExternalDelete(ctx, site, oldHosts, err)
	}
	if err := s.syncHosts(ctx, remaining); err != nil {
		return s.restoreExternalDelete(ctx, site, oldHosts, fmt.Errorf("sites: sync hosts before deleting %q: %w", site.Name, err))
	}
	if err := s.syncVhostsLocked(ctx, site.ID); err != nil {
		return s.restoreExternalDelete(ctx, site, oldHosts, fmt.Errorf("sites: sync vhosts before deleting %q: %w", site.Name, err))
	}

	if err := s.deps.Certs.Delete(ctx, site.ID); err != nil {
		return s.restoreExternalDelete(ctx, site, oldHosts, fmt.Errorf("sites: delete certificate for %q: %w", site.Name, err))
	}
	if err := s.deps.Store.DeleteSite(ctx, site.ID); err != nil {
		return s.restoreExternalDelete(ctx, site, oldHosts, fmt.Errorf("sites: delete %q: %w", site.Name, err))
	}

	s.emitEvent(ctx, site.ID, "site.deleted", fmt.Sprintf("site %s deleted", site.Name))
	// Only release retained and row ports after the row is gone: a later
	// failure restores the row and expects its FastCGI port to stay reserved.
	if s.deps.Backend != nil {
		s.releaseRetainedPHPGenerationsLocked(site.ID)
	}
	s.deps.Ports.Release(site.HTTPPort)
	if site.FastCGIPort != 0 {
		s.deps.Ports.Release(site.FastCGIPort)
	}
	var removeErr error
	if site.Kind == model.SitePHP {
		removeErr = s.removePHPConfigLocked(site.ID)
	}
	if err := s.rebuildRoutesLocked(ctx); err != nil {
		return errors.Join(fmt.Errorf("sites: publish routes after deleting %q: %w", site.Name, err), removeErr)
	}
	if removeErr != nil {
		return fmt.Errorf("sites: remove PHP config for %q: %w", site.Name, removeErr)
	}
	return nil
}

// restoreExternalDelete reverses the reversible parts of a failed external
// deletion. The vhost set is re-synced last so it matches the restored rows.
func (s *Service) restoreExternalDelete(ctx context.Context, site model.Site, oldHosts []string, cause error) error {
	errs := []error{cause}
	if _, err := s.deps.Certs.Ensure(ctx, site.ID, siteDomains(site)); err != nil {
		errs = append(errs, fmt.Errorf("rollback: restore certificate for %q: %w", site.Name, err))
	}
	if err := s.deps.Store.UpdateSite(ctx, site); err != nil {
		errs = append(errs, fmt.Errorf("rollback: restore row %q: %w", site.Name, err))
	}
	if err := s.syncHosts(ctx, oldHosts); err != nil {
		errs = append(errs, fmt.Errorf("rollback: restore hosts for %q: %w", site.Name, err))
	}
	if site.DesiredState == model.DesiredRunning {
		switch site.Kind {
		case model.SiteNode:
			if s.deps.Backend != nil {
				if _, err := s.deps.Backend.Prepare(ctx, backendSpec(site)); err == nil {
					if _, err := s.deps.Backend.Start(ctx, site.ID); err != nil {
						errs = append(errs, fmt.Errorf("rollback: restart %q: %w", site.Name, err))
					}
				} else {
					errs = append(errs, fmt.Errorf("rollback: prepare %q: %w", site.Name, err))
				}
			}
		case model.SitePHP:
			if s.deps.PHPBackend == nil {
				errs = append(errs, fmt.Errorf("rollback: restart %q: PHP backend is not configured", site.Name))
				break
			}
			spec, err := s.phpSiteSpecLocked(ctx, site)
			if err != nil {
				errs = append(errs, fmt.Errorf("rollback: prepare %q: %w", site.Name, err))
				break
			}
			if _, err := s.deps.PHPBackend.PreparePHP(ctx, site.ID, phpDefaultGeneration, spec); err != nil {
				errs = append(errs, fmt.Errorf("rollback: prepare %q: %w", site.Name, err))
			} else if _, err := s.deps.PHPBackend.StartPHP(ctx, site.ID, phpDefaultGeneration); err != nil {
				errs = append(errs, fmt.Errorf("rollback: restart %q: %w", site.Name, err))
			}
		}
	}
	if s.externalServing() {
		if err := s.syncVhostsLocked(ctx, ""); err != nil {
			errs = append(errs, fmt.Errorf("rollback: restore vhosts for %q: %w", site.Name, err))
		}
	}
	if err := s.rebuildRoutesLocked(ctx); err != nil {
		errs = append(errs, fmt.Errorf("rollback: restore routes for %q: %w", site.Name, err))
	}
	return errors.Join(errs...)
}
