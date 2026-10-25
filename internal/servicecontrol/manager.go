// Package servicecontrol implements the daemon-side lifecycle manager for
// infrastructure services. It merges the immutable catalog, the privileged
// helper's container status, and persisted state, and it runs lifecycle actions
// through a per-service Preparer before delegating to the helper.
package servicecontrol

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

// Helper is the privileged-helper service RPC surface.
type Helper interface {
	Status(ctx context.Context) ([]helperproto.ServiceStatus, error)
	Install(ctx context.Context, id string) error
	Activate(ctx context.Context, id string) error
	Deactivate(ctx context.Context, id string) error
	Restart(ctx context.Context, id string) error
	Uninstall(ctx context.Context, id string, deleteData bool) error
	Logs(ctx context.Context, id string, tail int) (string, error)
}

// Store is the persistent service-state surface.
type Store interface {
	GetServiceState(ctx context.Context, id services.ID) (services.Record, error)
	ListServiceStates(ctx context.Context) ([]services.Record, error)
	SaveServiceState(ctx context.Context, rec services.Record) error
	DeleteServiceState(ctx context.Context, id services.ID) error
}

// UninstallResult reports the optional backup written before data deletion.
// The helper owns the backup flow; the path is empty when no backup exists.
type UninstallResult struct {
	BackupPath string `json:"backup_path,omitempty"`
}

// Manager owns service lifecycle actions and the merged service view.
type Manager struct {
	root      string
	store     Store
	helper    Helper
	catalog   services.Catalog
	preparers map[services.ID]Preparer
	now       func() time.Time
}

// NewManager returns a Manager for root and catalog. A nil or empty preparer
// map falls back to NoopPreparer for every service.
func NewManager(
	root string,
	st Store,
	helper Helper,
	catalog services.Catalog,
	preparers map[services.ID]Preparer,
) *Manager {
	registry := make(map[services.ID]Preparer, len(preparers))
	for id, prep := range preparers {
		if prep != nil {
			registry[id] = prep
		}
	}
	return &Manager{
		root:      root,
		store:     st,
		helper:    helper,
		catalog:   catalog,
		preparers: registry,
		now:       time.Now,
	}
}

// List returns every catalog service merged with helper status and persisted
// state. A helper RPC failure is returned rather than shown as not installed.
func (m *Manager) List(ctx context.Context) ([]services.View, error) {
	statuses, err := m.helper.Status(ctx)
	if err != nil {
		return nil, helperFailure("list service status", err)
	}

	records := map[services.ID]services.Record{}
	recs, err := m.store.ListServiceStates(ctx)
	if err != nil {
		return nil, err
	}
	for _, rec := range recs {
		records[rec.ID] = rec
	}

	byID := make(map[services.ID]helperproto.ServiceStatus, len(statuses))
	for _, status := range statuses {
		byID[services.ID(status.ID)] = status
	}

	views := make([]services.View, 0, len(m.catalog.Services))
	for _, svc := range m.catalog.Services {
		rec, hasRec := records[svc.ID]
		status, hasStatus := byID[svc.ID]
		views = append(views, m.buildView(svc, rec, hasRec, status, hasStatus))
	}
	return views, nil
}

// Get returns one merged service view.
func (m *Manager) Get(ctx context.Context, id services.ID) (services.View, error) {
	svc, err := m.service(id)
	if err != nil {
		return services.View{}, err
	}

	statuses, err := m.helper.Status(ctx)
	if err != nil {
		return services.View{}, helperFailure("get service status", err)
	}
	var status helperproto.ServiceStatus
	hasStatus := false
	for _, candidate := range statuses {
		if services.ID(candidate.ID) == id {
			status = candidate
			hasStatus = true
			break
		}
	}

	rec, err := m.store.GetServiceState(ctx, id)
	hasRec := err == nil
	if err != nil && !errors.Is(err, services.ErrNotFound) {
		return services.View{}, err
	}
	return m.buildView(svc, rec, hasRec, status, hasStatus), nil
}

// Install prepares the service, pulls its image through the helper, and
// persists the installed state.
func (m *Manager) Install(ctx context.Context, id services.ID) error {
	svc, err := m.service(id)
	if err != nil {
		return err
	}
	prep := m.preparer(id)

	if err := prep.PrepareInstall(ctx, svc); err != nil {
		return m.recordFailure(ctx, svc, "install", false, err)
	}
	if err := m.helper.Install(ctx, string(id)); err != nil {
		return m.recordFailure(ctx, svc, "install", false, helperFailure("install service", err))
	}
	return m.persist(ctx, svc, services.StateInstalled, false, "install", "")
}

// Activate prepares the service, starts it through the helper, commits the
// preparer's work, and persists the active state. Any failure runs
// CleanupActivate and records the error.
func (m *Manager) Activate(ctx context.Context, id services.ID) error {
	svc, err := m.service(id)
	if err != nil {
		return err
	}
	prep := m.preparer(id)

	cause := prep.PrepareActivate(ctx, svc)
	if cause == nil {
		if helperErr := m.helper.Activate(ctx, string(id)); helperErr != nil {
			cause = helperFailure("activate service", helperErr)
		}
	}
	if cause == nil {
		cause = prep.CommitActivate(ctx, svc)
	}
	if cause != nil {
		return m.activationFailure(ctx, svc, prep, cause)
	}
	return m.persist(ctx, svc, services.StateActive, true, "activate", "")
}

// Deactivate stops the service and records the stopped state.
func (m *Manager) Deactivate(ctx context.Context, id services.ID) error {
	svc, err := m.service(id)
	if err != nil {
		return err
	}
	if err := m.checkDeactivationGuard(ctx, id, false); err != nil {
		return err
	}
	if err := m.helper.Deactivate(ctx, string(id)); err != nil {
		return m.recordFailure(ctx, svc, "deactivate", m.currentActive(ctx, id), helperFailure("deactivate service", err))
	}
	return m.persist(ctx, svc, services.StateInstalledButStopped, false, "deactivate", "")
}

// Restart restarts the service and records the active state.
func (m *Manager) Restart(ctx context.Context, id services.ID) error {
	svc, err := m.service(id)
	if err != nil {
		return err
	}
	if err := m.helper.Restart(ctx, string(id)); err != nil {
		return m.recordFailure(ctx, svc, "restart", m.currentActive(ctx, id), helperFailure("restart service", err))
	}
	return m.persist(ctx, svc, services.StateActive, true, "restart", "")
}

// Uninstall removes the service container and its persisted state. Data is
// preserved unless deleteData is set; the helper owns the backup and deletion
// flow.
func (m *Manager) Uninstall(ctx context.Context, id services.ID, deleteData bool) (UninstallResult, error) {
	svc, err := m.service(id)
	if err != nil {
		return UninstallResult{}, err
	}
	if err := m.checkDeactivationGuard(ctx, id, true); err != nil {
		return UninstallResult{}, err
	}
	if err := m.helper.Uninstall(ctx, string(id), deleteData); err != nil {
		return UninstallResult{}, m.recordFailure(ctx, svc, "uninstall", m.currentActive(ctx, id), helperFailure("uninstall service", err))
	}
	if err := m.store.DeleteServiceState(ctx, id); err != nil && !errors.Is(err, services.ErrNotFound) {
		return UninstallResult{}, err
	}
	return UninstallResult{}, nil
}

// Logs returns bounded container logs from the helper.
func (m *Manager) Logs(ctx context.Context, id services.ID, tail int) (string, error) {
	if _, err := m.service(id); err != nil {
		return "", err
	}
	logs, err := m.helper.Logs(ctx, string(id), tail)
	if err != nil {
		return "", helperFailure("read service logs", err)
	}
	return logs, nil
}

// Credentials reveals a service's credentials through its preparer.
func (m *Manager) Credentials(ctx context.Context, id services.ID) (Credentials, error) {
	if _, err := m.service(id); err != nil {
		return Credentials{}, err
	}
	return m.preparer(id).Credentials(ctx, id)
}

// RotateCredentials regenerates a service's credentials through its preparer.
func (m *Manager) RotateCredentials(ctx context.Context, id services.ID) (Credentials, error) {
	if _, err := m.service(id); err != nil {
		return Credentials{}, err
	}
	return m.preparer(id).RotateCredentials(ctx, id)
}

func (m *Manager) service(id services.ID) (services.Service, error) {
	svc, ok := m.catalog.Get(id)
	if !ok {
		return services.Service{}, fmt.Errorf("%w: %q", services.ErrNotFound, id)
	}
	return svc, nil
}

// Deactivate stops the target container through the provider-switch path,
// bypassing the active-server guard because sites.Service has already chosen
// the explicit Docker -> builtin transition.
func (m *Manager) StopForProviderSwitch(ctx context.Context, id services.ID) error {
	svc, err := m.service(id)
	if err != nil {
		return err
	}
	if err := m.helper.Deactivate(ctx, string(id)); err != nil {
		return m.recordFailure(ctx, svc, "provider-switch-stop", m.currentActive(ctx, id), helperFailure("deactivate service", err))
	}
	return m.persist(ctx, svc, services.StateInstalledButStopped, false, "provider-switch-stop", "")
}

// StartForProviderSwitch restarts a previously active container when a
// Docker -> builtin transition fails after the container was stopped.
func (m *Manager) StartForProviderSwitch(ctx context.Context, id services.ID) error {
	svc, err := m.service(id)
	if err != nil {
		return err
	}
	if err := m.helper.Activate(ctx, string(id)); err != nil {
		return m.recordFailure(ctx, svc, "provider-switch-start", m.currentActive(ctx, id), helperFailure("activate service", err))
	}
	return m.persist(ctx, svc, services.StateActive, true, "provider-switch-start", "")
}

// ServiceValidator is the optional helper capability used by web-server
// config checks. The P2 helper client exposes Validate and Reload.
type ServiceValidator interface {
	Validate(ctx context.Context, id string) error
	Reload(ctx context.Context, id string) error
}

// Validate runs the catalog validation command inside a service container.
func (m *Manager) Validate(ctx context.Context, id services.ID) error {
	if _, err := m.service(id); err != nil {
		return err
	}
	validator, ok := m.helper.(ServiceValidator)
	if !ok {
		return fmt.Errorf("%w: validate is not supported by the service helper", ErrHelperUnavailable)
	}
	if err := validator.Validate(ctx, string(id)); err != nil {
		return helperFailure("validate service", err)
	}
	return nil
}

// Reload gracefully reloads a service container.
func (m *Manager) Reload(ctx context.Context, id services.ID) error {
	if _, err := m.service(id); err != nil {
		return err
	}
	validator, ok := m.helper.(ServiceValidator)
	if !ok {
		return fmt.Errorf("%w: reload is not supported by the service helper", ErrHelperUnavailable)
	}
	if err := validator.Reload(ctx, string(id)); err != nil {
		return helperFailure("reload service", err)
	}
	return nil
}

func (m *Manager) preparer(id services.ID) Preparer {
	if prep, ok := m.preparers[id]; ok && prep != nil {
		return prep
	}
	return NoopPreparer{}
}

// checkDeactivationGuard lets a web-server preparer refuse to stop or remove
// the container that currently owns 80/443.
func (m *Manager) checkDeactivationGuard(ctx context.Context, id services.ID, uninstall bool) error {
	guard, ok := m.preparer(id).(DeactivationGuard)
	if !ok {
		return nil
	}
	if uninstall {
		return guard.CanUninstall(ctx, id)
	}
	return guard.CanDeactivate(ctx, id)
}

func (m *Manager) activationFailure(ctx context.Context, svc services.Service, prep Preparer, cause error) error {
	if cleanupErr := prep.CleanupActivate(ctx, svc, cause); cleanupErr != nil {
		cause = fmt.Errorf("%w; cleanup activation: %v", cause, cleanupErr)
	}
	return m.recordFailure(ctx, svc, "activate", false, cause)
}

func (m *Manager) recordFailure(ctx context.Context, svc services.Service, operation string, active bool, cause error) error {
	if err := m.persist(ctx, svc, services.StateError, active, operation, cause.Error()); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func (m *Manager) persist(
	ctx context.Context,
	svc services.Service,
	state services.State,
	active bool,
	operation string,
	lastError string,
) error {
	updatedAt := m.now().UTC()
	rec := services.Record{
		ID:            svc.ID,
		State:         state,
		Image:         svc.Image,
		Active:        active,
		LastError:     lastError,
		LastOperation: operation,
		UpdatedAt:     updatedAt,
	}
	if err := m.store.SaveServiceState(ctx, rec); err != nil {
		return fmt.Errorf("persist service %q state: %w", svc.ID, err)
	}
	return nil
}

func (m *Manager) currentActive(ctx context.Context, id services.ID) bool {
	rec, err := m.store.GetServiceState(ctx, id)
	if err != nil {
		return false
	}
	return rec.Active
}

func (m *Manager) buildView(
	svc services.Service,
	rec services.Record,
	hasRecord bool,
	status helperproto.ServiceStatus,
	hasStatus bool,
) services.View {
	view := services.View{
		ID:          svc.ID,
		DisplayName: svc.DisplayName,
		Group:       svc.Group,
		State:       services.StateNotInstalled,
		Image:       svc.Image,
		Ports:       svc.Ports,
	}
	if m.root != "" {
		view.ConfigPath = filepath.Join(m.root, "services", string(svc.ID), "config")
		view.DataPath = filepath.Join(m.root, "services", string(svc.ID), "data")
	}

	if hasRecord {
		if rec.State != "" {
			view.State = rec.State
		}
		view.Active = rec.Active
		view.LastError = rec.LastError
		view.Installed = view.State != services.StateNotInstalled
		if rec.Image != "" {
			view.Image = rec.Image
		}
	}

	if hasStatus {
		view.Installed = status.Installed
		if status.State != "" {
			view.State = services.State(status.State)
		} else {
			switch {
			case status.Active:
				view.State = services.StateActive
			case status.Installed:
				view.State = services.StateInstalledButStopped
			default:
				view.State = services.StateNotInstalled
			}
		}
		view.Active = status.Active
		view.ContainerID = status.ContainerID
		view.Health = status.Health
		if status.Image != "" {
			view.Image = status.Image
		}
		if status.ConfigPath != "" {
			view.ConfigPath = status.ConfigPath
		}
		if status.DataPath != "" {
			view.DataPath = status.DataPath
		}
		if status.LastError != "" {
			view.LastError = status.LastError
		}
		if len(status.Ports) > 0 {
			view.Ports = make([]services.Port, 0, len(status.Ports))
			for _, port := range status.Ports {
				view.Ports = append(view.Ports, services.Port{
					Host:      port.Host,
					Container: port.Container,
					Protocol:  port.Protocol,
					Loopback:  port.Loopback,
				})
			}
		}
	}

	return view
}

// helperFailure marks a helper RPC failure as unavailable while preserving the
// original cause and passing invalid requests through for the API's 400 path.
func helperFailure(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, helperproto.ErrInvalidRequest) {
		return err
	}
	return fmt.Errorf("%w: %s: %w", ErrHelperUnavailable, operation, err)
}
