package databases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/services"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

// Store is the narrow persistence surface the manager needs. *store.Store
// implements it.
type Store interface {
	CreateDatabaseLink(ctx context.Context, link model.DatabaseLinkRecord) error
	GetDatabaseLink(ctx context.Context, id string) (model.DatabaseLinkRecord, error)
	ListDatabaseLinksBySite(ctx context.Context, siteID string) ([]model.DatabaseLinkRecord, error)
	ListDatabaseLinksByService(ctx context.Context, serviceID string) ([]model.DatabaseLinkRecord, error)
	DeleteDatabaseLink(ctx context.Context, id string) error
	DeleteDatabaseLinksByService(ctx context.Context, serviceID string) (int64, error)
	SetPrimaryDatabaseLink(ctx context.Context, siteID, linkID string) error
	RenameDatabaseReferences(ctx context.Context, serviceID, oldName, newName string) error

	UpsertDatabaseUser(ctx context.Context, user model.DatabaseUserRecord) error
	GetDatabaseUser(ctx context.Context, id string) (model.DatabaseUserRecord, error)
	ListDatabaseUsers(ctx context.Context, serviceID string) ([]model.DatabaseUserRecord, error)
	DeleteDatabaseUser(ctx context.Context, id string) error
	SetDatabaseUserPasswordState(ctx context.Context, id string, managed bool, rotatedAt int64) error

	SaveDatabaseGrant(ctx context.Context, grant model.DatabaseGrantRecord) error
	ListDatabaseGrants(ctx context.Context, userID string) ([]model.DatabaseGrantRecord, error)
	DeleteDatabaseGrant(ctx context.Context, userID, databaseName string) error

	CreateDatabaseBackup(ctx context.Context, backup model.DatabaseBackupRecord) error
	GetDatabaseBackup(ctx context.Context, id string) (model.DatabaseBackupRecord, error)
	ListDatabaseBackups(ctx context.Context, serviceID string) ([]model.DatabaseBackupRecord, error)
	UpdateDatabaseBackup(ctx context.Context, backup model.DatabaseBackupRecord) error
	DeleteDatabaseBackup(ctx context.Context, id string) error

	CreateDatabaseJob(ctx context.Context, job model.DatabaseJobRecord) error
	GetDatabaseJob(ctx context.Context, id string) (model.DatabaseJobRecord, error)
	UpdateDatabaseJob(ctx context.Context, job model.DatabaseJobRecord) error
	ListDatabaseJobs(ctx context.Context, serviceID string) ([]model.DatabaseJobRecord, error)
	ListAllDatabaseJobs(ctx context.Context) ([]model.DatabaseJobRecord, error)

	AddAuditEvent(ctx context.Context, event model.AuditEventRecord) error
	ListAuditEvents(ctx context.Context, limit int) ([]model.AuditEventRecord, error)
	CountAuditEvents(ctx context.Context) (int64, error)
}

// ServiceState is the service lifecycle surface used for active/installed and
// exclusive-group checks. *servicecontrol.Manager implements it.
type ServiceState interface {
	Get(ctx context.Context, id services.ID) (services.View, error)
	List(ctx context.Context) ([]services.View, error)
}

// SiteResolver resolves site references and names. *store.Store implements it.
type SiteResolver interface {
	GetSite(ctx context.Context, id string) (model.Site, error)
	GetSiteByName(ctx context.Context, name string) (model.Site, error)
	ListSites(ctx context.Context) ([]model.Site, error)
}

// EventSink records the diagnostic/SSE event feed.
type EventSink interface {
	AddEvent(ctx context.Context, event model.Event) error
}

// AuditSink records durable audit rows.
type AuditSink interface {
	AddAuditEvent(ctx context.Context, event model.AuditEventRecord) error
}

// Deps configures a Manager.
type Deps struct {
	Store    Store
	Platform Platform
	Services ServiceState
	Sites    SiteResolver
	Events   EventSink
	Audit    AuditSink
	Logger   *slog.Logger
	Now      func() time.Time
	NewID    func() string
}

// Manager owns database metadata, site links, jobs, and audit. Engine work is
// delegated to the privileged platform adapter.
type Manager struct {
	deps Deps

	locksMu sync.Mutex
	locks   map[string]*sync.Mutex

	jobsMu       sync.Mutex
	cancels      map[string]context.CancelFunc
	platformJobs map[string]string
}

// NewManager returns a database manager. Nil logger/clock/id dependencies use
// production defaults.
func NewManager(deps Deps) *Manager {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	return &Manager{
		deps:         deps,
		locks:        make(map[string]*sync.Mutex),
		cancels:      make(map[string]context.CancelFunc),
		platformJobs: make(map[string]string),
	}
}

// lockService serializes mutations for one service.
func (m *Manager) lockService(serviceID string) func() {
	serviceID = canonicalServiceID(serviceID)
	m.locksMu.Lock()
	lock := m.locks[serviceID]
	if lock == nil {
		lock = &sync.Mutex{}
		m.locks[serviceID] = lock
	}
	m.locksMu.Unlock()
	lock.Lock()
	return lock.Unlock
}

// serviceView returns the catalog/service state for a service.
func (m *Manager) serviceView(ctx context.Context, serviceID string) (services.View, error) {
	if m.deps.Services == nil {
		return services.View{}, fmt.Errorf("%w: service state is unavailable", ErrServiceInactive)
	}
	view, err := m.deps.Services.Get(ctx, services.ID(canonicalServiceID(serviceID)))
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			return services.View{}, fmt.Errorf("%w: service %q", ErrServiceInactive, serviceID)
		}
		return services.View{}, err
	}
	return view, nil
}

// requireActive rejects engine operations on an installed-but-stopped or
// uninstalled service.
func (m *Manager) requireActive(ctx context.Context, serviceID string) (services.View, error) {
	view, err := m.serviceView(ctx, serviceID)
	if err != nil {
		return services.View{}, err
	}
	if !view.Active || view.State != services.StateActive {
		return services.View{}, fmt.Errorf("%w: service %q is not active", ErrServiceInactive, serviceID)
	}
	return view, nil
}

// requireInstalled allows backup listing/download/deletion while a service is
// installed but stopped.
func (m *Manager) requireInstalled(ctx context.Context, serviceID string) (services.View, error) {
	view, err := m.serviceView(ctx, serviceID)
	if err != nil {
		return services.View{}, err
	}
	if !view.Installed {
		return services.View{}, fmt.Errorf("%w: service %q is not installed", ErrServiceInactive, serviceID)
	}
	return view, nil
}

// ListDatabases merges engine-discovered databases with site links. Linked
// databases that no longer exist are returned with Missing set.
func (m *Manager) ListDatabases(ctx context.Context, serviceID string) ([]model.Database, error) {
	serviceID = canonicalServiceID(serviceID)
	if _, err := m.requireActive(ctx, serviceID); err != nil {
		return nil, err
	}
	if isRedisService(serviceID) {
		return nil, fmt.Errorf("%w: Redis has no logical database inventory", ErrUnsupported)
	}
	databases, err := m.deps.Platform.ListDatabases(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	engine := engineForService(serviceID)
	links, err := m.deps.Store.ListDatabaseLinksByService(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]int, len(databases))
	for i := range databases {
		if databases[i].ServiceID == "" {
			databases[i].ServiceID = serviceID
		}
		if databases[i].Engine == "" {
			databases[i].Engine = engine
		}
		byName[databases[i].Name] = i
	}
	for _, link := range links {
		if index, ok := byName[link.DatabaseName]; ok {
			databases[index].LinkedSites = append(databases[index].LinkedSites, model.DatabaseSiteRef{ID: link.SiteID, Name: link.SiteName})
			continue
		}
		databases = append(databases, model.Database{
			Name:      link.DatabaseName,
			ServiceID: serviceID,
			Engine:    engine,
			Missing:   true,
			LinkedSites: []model.DatabaseSiteRef{{
				ID: link.SiteID, Name: link.SiteName,
			}},
		})
	}
	sort.SliceStable(databases, func(i, j int) bool { return databases[i].Name < databases[j].Name })
	return databases, nil
}

// CreateDatabase creates a service-wide database and audits the mutation.
func (m *Manager) CreateDatabase(ctx context.Context, serviceID string, req CreateDatabaseRequest) (model.Database, error) {
	serviceID = canonicalServiceID(serviceID)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return model.Database{}, fmt.Errorf("%w: database name is required", ErrInvalidRequest)
	}
	if protectedDatabase(name) {
		return model.Database{}, fmt.Errorf("%w: database %q is protected", ErrProtected, name)
	}
	if isRedisService(serviceID) {
		return model.Database{}, fmt.Errorf("%w: Redis has no logical databases", ErrUnsupported)
	}
	if _, err := m.requireActive(ctx, serviceID); err != nil {
		return model.Database{}, err
	}
	unlock := m.lockService(serviceID)
	defer unlock()

	req.Name = name
	db, err := m.deps.Platform.CreateDatabase(ctx, serviceID, req)
	if err != nil {
		m.audit(ctx, "database.created", serviceID, "", "database", name, "failed", map[string]any{"error": safeErrorMessage(err)})
		return model.Database{}, err
	}
	if db.ServiceID == "" {
		db.ServiceID = serviceID
	}
	if db.Engine == "" {
		db.Engine = engineForService(serviceID)
	}
	m.audit(ctx, "database.created", serviceID, "", "database", db.Name, "ok", nil)
	m.emit(ctx, "", "database.created", fmt.Sprintf("database %s created on %s", db.Name, serviceID))
	return db, nil
}

// RenameDatabase renames a PostgreSQL database. MySQL/MariaDB have no safe
// native rename and return ErrUnsupported.
func (m *Manager) RenameDatabase(ctx context.Context, serviceID, name string, req RenameDatabaseRequest) (model.Database, error) {
	serviceID = canonicalServiceID(serviceID)
	if engineForService(serviceID) != model.DatabasePostgreSQL {
		return model.Database{}, fmt.Errorf("%w: %s does not support database rename; use backup, create, and import instead", ErrUnsupported, engineForService(serviceID))
	}
	name = strings.TrimSpace(name)
	newName := strings.TrimSpace(req.Name)
	if name == "" || newName == "" {
		return model.Database{}, fmt.Errorf("%w: current and new database names are required", ErrInvalidRequest)
	}
	if protectedDatabase(name) || protectedDatabase(newName) {
		return model.Database{}, fmt.Errorf("%w: database is protected", ErrProtected)
	}
	if strings.TrimSpace(req.Confirm) != name {
		return model.Database{}, fmt.Errorf("%w: type %q to confirm the rename", ErrConflict, name)
	}
	if _, err := m.requireActive(ctx, serviceID); err != nil {
		return model.Database{}, err
	}
	if err := m.ensureLinkedSitesStopped(ctx, serviceID, name); err != nil {
		return model.Database{}, err
	}
	unlock := m.lockService(serviceID)
	defer unlock()

	db, err := m.deps.Platform.RenameDatabase(ctx, serviceID, name, req)
	if err != nil {
		m.audit(ctx, "database.renamed", serviceID, "", "database", name, "failed", map[string]any{"error": safeErrorMessage(err)})
		return model.Database{}, err
	}
	if db.Name == "" {
		db.Name = newName
	}
	if db.ServiceID == "" {
		db.ServiceID = serviceID
	}
	if db.Engine == "" {
		db.Engine = model.DatabasePostgreSQL
	}
	if err := m.deps.Store.RenameDatabaseReferences(ctx, serviceID, name, newName); err != nil {
		return model.Database{}, err
	}
	m.audit(ctx, "database.renamed", serviceID, "", "database", name, "ok", map[string]any{"new_name": newName})
	m.emit(ctx, "", "database.renamed", fmt.Sprintf("database %s renamed to %s on %s", name, newName, serviceID))
	return db, nil
}

// DropDatabase drops a database after typed confirmation and a stopped-site
// check. BackupFirst creates a pre_drop backup before the engine mutation.
func (m *Manager) DropDatabase(ctx context.Context, serviceID, name string, req DropDatabaseRequest) error {
	serviceID = canonicalServiceID(serviceID)
	name = strings.TrimSpace(name)
	if !isSQLService(serviceID) {
		return fmt.Errorf("%w: service %q has no logical databases", ErrUnsupported, serviceID)
	}
	if protectedDatabase(name) {
		return fmt.Errorf("%w: database %q is protected", ErrProtected, name)
	}
	if strings.TrimSpace(req.Confirm) != name {
		return fmt.Errorf("%w: type %q to confirm the drop", ErrConflict, name)
	}
	if _, err := m.requireActive(ctx, serviceID); err != nil {
		return err
	}
	if err := m.ensureLinkedSitesStopped(ctx, serviceID, name); err != nil {
		return err
	}

	if req.BackupFirst {
		job, err := m.CreateBackup(ctx, serviceID, name, CreateBackupRequest{Kind: model.BackupPreDrop})
		if err != nil {
			return fmt.Errorf("backup before drop: %w", err)
		}
		if job.Status != model.JobSucceeded {
			return fmt.Errorf("%w: backup before drop did not succeed: %s", ErrConflict, job.Message)
		}
	}
	unlock := m.lockService(serviceID)
	defer unlock()
	if err := m.deps.Platform.DropDatabase(ctx, serviceID, name, req); err != nil {
		m.audit(ctx, "database.dropped", serviceID, "", "database", name, "failed", map[string]any{"error": safeErrorMessage(err)})
		return err
	}
	m.audit(ctx, "database.dropped", serviceID, "", "database", name, "ok", nil)
	m.emit(ctx, "", "database.dropped", fmt.Sprintf("database %s dropped on %s", name, serviceID))
	return nil
}

// ListUsers merges engine users with persisted metadata, grants, and links.
// Every discovered user is upserted with a stable opaque ID so it can be
// selected in Add Site.
func (m *Manager) ListUsers(ctx context.Context, serviceID string) ([]model.DatabaseUser, error) {
	serviceID = canonicalServiceID(serviceID)
	if _, err := m.requireActive(ctx, serviceID); err != nil {
		return nil, err
	}
	engineUsers, err := m.deps.Platform.ListUsers(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	stored, err := m.deps.Store.ListDatabaseUsers(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	links, err := m.deps.Store.ListDatabaseLinksByService(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	linkedByUser := make(map[string][]model.DatabaseSiteRef)
	for _, link := range links {
		linkedByUser[link.UserID] = append(linkedByUser[link.UserID], model.DatabaseSiteRef{ID: link.SiteID, Name: link.SiteName})
	}
	byID := make(map[string]model.DatabaseUserRecord, len(stored))
	byKey := make(map[string]model.DatabaseUserRecord, len(stored))
	for _, user := range stored {
		byID[user.ID] = user
		byKey[user.Username+"\x00"+user.Host] = user
	}
	now := m.now().Unix()
	engine := engineForService(serviceID)
	out := make([]model.DatabaseUser, 0, len(engineUsers))
	for _, engineUser := range engineUsers {
		id := strings.TrimSpace(engineUser.ID)
		record, ok := byID[id]
		if !ok || id == "" {
			record, ok = byKey[engineUser.Username+"\x00"+engineUser.Host]
			if ok {
				id = record.ID
			}
		}
		if id == "" {
			id = stableUserID(serviceID, engineUser.Username, engineUser.Host)
			record, _ = byID[id]
		}
		record.ID = id
		record.ServiceID = serviceID
		record.Engine = engine
		record.Username = engineUser.Username
		record.Host = engineUser.Host
		record.Enabled = engineUser.Enabled
		record.AccessLevel = engineUser.AccessLevel
		record.ManagedByDevstack = record.ManagedByDevstack || engineUser.Managed
		record.PasswordManaged = record.PasswordManaged || engineUser.PasswordManaged
		record.LastSeenAt = now
		if record.CreatedAt == 0 {
			record.CreatedAt = now
		}
		if err := m.deps.Store.UpsertDatabaseUser(ctx, record); err != nil {
			return nil, err
		}
		grants := engineUser.Grants
		if len(grants) == 0 {
			grants = m.storedGrants(ctx, id)
		}
		user := model.DatabaseUser{
			ID:              id,
			ServiceID:       serviceID,
			Engine:          engine,
			Username:        engineUser.Username,
			Host:            engineUser.Host,
			Enabled:         record.Enabled,
			Managed:         record.ManagedByDevstack,
			PasswordManaged: record.PasswordManaged,
			AccessLevel:     record.AccessLevel,
			Grants:          grants,
			LinkedSites:     linkedByUser[id],
		}
		if record.CreatedAt > 0 {
			user.CreatedAt = unixTime(record.CreatedAt)
		}
		if record.LastPasswordRotatedAt > 0 {
			rotated := unixTime(record.LastPasswordRotatedAt)
			user.LastPasswordRotatedAt = &rotated
		}
		out = append(out, user)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Username == out[j].Username {
			return out[i].Host < out[j].Host
		}
		return out[i].Username < out[j].Username
	})
	return out, nil
}

// storedGrants converts persisted grant intent rows into view grants.
func (m *Manager) storedGrants(ctx context.Context, userID string) []model.DatabaseGrant {
	records, err := m.deps.Store.ListDatabaseGrants(ctx, userID)
	if err != nil {
		return nil
	}
	grants := make([]model.DatabaseGrant, 0, len(records))
	for _, record := range records {
		grants = append(grants, model.DatabaseGrant{
			Database:   record.DatabaseName,
			Preset:     model.DatabaseGrantPreset(record.Preset),
			Privileges: decodePrivileges(record.CustomJSON),
		})
	}
	return grants
}

// CreateUser creates an engine user and records its managed metadata.
func (m *Manager) CreateUser(ctx context.Context, serviceID string, req CreateUserRequest) (model.DatabaseUser, error) {
	serviceID = canonicalServiceID(serviceID)
	username := strings.TrimSpace(req.Username)
	if username == "" {
		return model.DatabaseUser{}, fmt.Errorf("%w: username is required", ErrInvalidRequest)
	}
	if protectedUser(username) {
		return model.DatabaseUser{}, fmt.Errorf("%w: user %q is protected", ErrProtected, username)
	}
	if _, err := m.requireActive(ctx, serviceID); err != nil {
		return model.DatabaseUser{}, err
	}
	unlock := m.lockService(serviceID)
	defer unlock()

	req.Username = username
	user, err := m.deps.Platform.CreateUser(ctx, serviceID, req)
	if err != nil {
		m.audit(ctx, "database.user.created", serviceID, "", "user", username, "failed", map[string]any{"error": safeErrorMessage(err)})
		return model.DatabaseUser{}, err
	}
	if user.ID == "" {
		user.ID = stableUserID(serviceID, username, user.Host)
	}
	record := model.DatabaseUserRecord{
		ID:                user.ID,
		ServiceID:         serviceID,
		Engine:            engineForService(serviceID),
		Username:          user.Username,
		Host:              user.Host,
		ManagedByDevstack: true,
		PasswordManaged:   true,
		Enabled:           true,
		AccessLevel:       user.AccessLevel,
		CreatedAt:         m.now().Unix(),
		LastSeenAt:        m.now().Unix(),
	}
	if record.Username == "" {
		record.Username = username
	}
	if record.Host == "" {
		record.Host = strings.TrimSpace(req.Host)
	}
	if record.Host == "" && (record.Engine == model.DatabaseMySQL || record.Engine == model.DatabaseMariaDB) {
		record.Host = "%"
	}
	user.Host = record.Host
	user.Username = record.Username
	if err := m.deps.Store.UpsertDatabaseUser(ctx, record); err != nil {
		return model.DatabaseUser{}, err
	}
	for _, grant := range req.Grants {
		preset := grant.Preset
		if preset == "" {
			preset = model.GrantReadWriteSchema
		}
		if err := m.saveGrant(ctx, user.ID, grant.Database, preset, grant.Privileges); err != nil {
			return model.DatabaseUser{}, err
		}
	}
	user.Managed = true
	user.PasswordManaged = true
	user.ServiceID = serviceID
	user.Engine = record.Engine
	user.Enabled = record.Enabled
	m.audit(ctx, "database.user.created", serviceID, "", "user", record.Username, "ok", nil)
	m.emit(ctx, "", "database.user.created", fmt.Sprintf("database user %s created on %s", record.Username, serviceID))
	return user, nil
}

// UpdateUser applies enabled/grants/access-level changes and records intent.
func (m *Manager) UpdateUser(ctx context.Context, serviceID, userID string, req UpdateUserRequest) (model.DatabaseUser, error) {
	serviceID = canonicalServiceID(serviceID)
	if _, err := m.requireActive(ctx, serviceID); err != nil {
		return model.DatabaseUser{}, err
	}
	record, err := m.deps.Store.GetDatabaseUser(ctx, userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return model.DatabaseUser{}, fmt.Errorf("%w: user %q", ErrNotFound, userID)
		}
		return model.DatabaseUser{}, err
	}
	if protectedUser(record.Username) {
		return model.DatabaseUser{}, fmt.Errorf("%w: user %q is protected", ErrProtected, record.Username)
	}
	unlock := m.lockService(serviceID)
	defer unlock()

	user, err := m.deps.Platform.UpdateUser(ctx, serviceID, userID, req)
	if err != nil {
		m.audit(ctx, "database.user.grants_changed", serviceID, "", "user", record.Username, "failed", map[string]any{"error": safeErrorMessage(err)})
		return model.DatabaseUser{}, err
	}
	if req.Enabled != nil {
		record.Enabled = *req.Enabled
	} else if user.Enabled {
		record.Enabled = true
	}
	if req.AccessLevel != "" {
		record.AccessLevel = req.AccessLevel
	}
	record.LastSeenAt = m.now().Unix()
	if err := m.deps.Store.UpsertDatabaseUser(ctx, record); err != nil {
		return model.DatabaseUser{}, err
	}
	if req.Grants != nil {
		kept := make(map[string]struct{}, len(req.Grants))
		for _, grant := range req.Grants {
			preset := grant.Preset
			if preset == "" {
				preset = model.GrantReadWriteSchema
			}
			kept[grant.Database] = struct{}{}
			if err := m.saveGrant(ctx, userID, grant.Database, preset, grant.Privileges); err != nil {
				return model.DatabaseUser{}, err
			}
		}
		existing, err := m.deps.Store.ListDatabaseGrants(ctx, userID)
		if err != nil {
			return model.DatabaseUser{}, err
		}
		for _, grant := range existing {
			if _, ok := kept[grant.DatabaseName]; ok {
				continue
			}
			if err := m.deps.Store.DeleteDatabaseGrant(ctx, userID, grant.DatabaseName); err != nil {
				return model.DatabaseUser{}, err
			}
		}
	}
	m.audit(ctx, "database.user.grants_changed", serviceID, "", "user", record.Username, "ok", nil)
	m.emit(ctx, "", "database.user.grants_changed", fmt.Sprintf("database user %s grants updated on %s", record.Username, serviceID))
	return m.userView(ctx, serviceID, record, user.Grants), nil
}

// userView assembles a user view from a persisted record and engine grants.
func (m *Manager) userView(ctx context.Context, serviceID string, record model.DatabaseUserRecord, grants []model.DatabaseGrant) model.DatabaseUser {
	links, _ := m.deps.Store.ListDatabaseLinksByService(ctx, serviceID)
	var linked []model.DatabaseSiteRef
	for _, link := range links {
		if link.UserID == record.ID {
			linked = append(linked, model.DatabaseSiteRef{ID: link.SiteID, Name: link.SiteName})
		}
	}
	if len(grants) == 0 {
		grants = m.storedGrants(ctx, record.ID)
	}
	user := model.DatabaseUser{
		ID:              record.ID,
		ServiceID:       record.ServiceID,
		Engine:          record.Engine,
		Username:        record.Username,
		Host:            record.Host,
		Enabled:         record.Enabled,
		Managed:         record.ManagedByDevstack,
		PasswordManaged: record.PasswordManaged,
		AccessLevel:     record.AccessLevel,
		Grants:          grants,
		LinkedSites:     linked,
	}
	if record.CreatedAt > 0 {
		user.CreatedAt = unixTime(record.CreatedAt)
	}
	if record.LastPasswordRotatedAt > 0 {
		rotated := unixTime(record.LastPasswordRotatedAt)
		user.LastPasswordRotatedAt = &rotated
	}
	return user
}

// DeleteUser deletes an unlinked engine user.
func (m *Manager) DeleteUser(ctx context.Context, serviceID, userID string, req DeleteUserRequest) error {
	serviceID = canonicalServiceID(serviceID)
	record, err := m.deps.Store.GetDatabaseUser(ctx, userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("%w: user %q", ErrNotFound, userID)
		}
		return err
	}
	if protectedUser(record.Username) {
		return fmt.Errorf("%w: user %q is protected", ErrProtected, record.Username)
	}
	if strings.TrimSpace(req.Confirm) != record.Username {
		return fmt.Errorf("%w: type %q to confirm the delete", ErrConflict, record.Username)
	}
	if _, err := m.requireActive(ctx, serviceID); err != nil {
		return err
	}
	links, err := m.deps.Store.ListDatabaseLinksByService(ctx, serviceID)
	if err != nil {
		return err
	}
	for _, link := range links {
		if link.UserID == userID {
			return fmt.Errorf("%w: user %q is linked to site %q", ErrLinkedSites, record.Username, link.SiteName)
		}
	}
	unlock := m.lockService(serviceID)
	defer unlock()

	if err := m.deps.Platform.DeleteUser(ctx, serviceID, userID, req); err != nil {
		m.audit(ctx, "database.user.deleted", serviceID, "", "user", record.Username, "failed", map[string]any{"error": safeErrorMessage(err)})
		return err
	}
	if err := m.deps.Store.DeleteDatabaseUser(ctx, userID); err != nil && !errors.Is(err, store.ErrNotFound) {
		if errors.Is(err, store.ErrConflict) {
			return fmt.Errorf("%w: user %q is linked to a site", ErrLinkedSites, record.Username)
		}
		return err
	}
	m.audit(ctx, "database.user.deleted", serviceID, "", "user", record.Username, "ok", nil)
	m.emit(ctx, "", "database.user.deleted", fmt.Sprintf("database user %s deleted on %s", record.Username, serviceID))
	return nil
}

// SetUserPassword generates and stores a new password on the platform.
func (m *Manager) SetUserPassword(ctx context.Context, serviceID, userID string) (Secret, error) {
	serviceID = canonicalServiceID(serviceID)
	if _, err := m.requireActive(ctx, serviceID); err != nil {
		return Secret{}, err
	}
	unlock := m.lockService(serviceID)
	defer unlock()

	secret, err := m.deps.Platform.SetUserPassword(ctx, serviceID, userID)
	if err != nil {
		return Secret{}, err
	}
	if record, err := m.deps.Store.GetDatabaseUser(ctx, userID); err == nil {
		if err := m.deps.Store.SetDatabaseUserPasswordState(ctx, userID, true, m.now().Unix()); err != nil {
			return Secret{}, err
		}
		m.audit(ctx, "database.user.password_changed", serviceID, "", "user", record.Username, "ok", nil)
		m.emit(ctx, "", "database.user.password_changed", fmt.Sprintf("database user %s password changed on %s", record.Username, serviceID))
	} else {
		m.audit(ctx, "database.user.password_changed", serviceID, "", "user", userID, "ok", nil)
		m.emit(ctx, "", "database.user.password_changed", fmt.Sprintf("database user %s password changed on %s", userID, serviceID))
	}
	return secret, nil
}

// SetDefaultUserPassword resolves the Redis default ACL user by username and
// rotates its password through the opaque user ID.
func (m *Manager) SetDefaultUserPassword(ctx context.Context, serviceID string) (Secret, error) {
	serviceID = canonicalServiceID(serviceID)
	users, err := m.ListUsers(ctx, serviceID)
	if err != nil {
		return Secret{}, err
	}
	for _, user := range users {
		if strings.EqualFold(user.Username, "default") {
			return m.SetUserPassword(ctx, serviceID, user.ID)
		}
	}
	return Secret{}, fmt.Errorf("%w: default user", ErrNotFound)
}

// RevealUserPassword returns a stored secret once and audits the reveal. The
// value is never persisted by the daemon.
func (m *Manager) RevealUserPassword(ctx context.Context, serviceID, userID string) (Secret, error) {
	serviceID = canonicalServiceID(serviceID)
	if _, err := m.requireActive(ctx, serviceID); err != nil {
		return Secret{}, err
	}
	secret, err := m.deps.Platform.RevealUserPassword(ctx, serviceID, userID)
	if err != nil {
		m.audit(ctx, "database.user.password_revealed", serviceID, "", "user", userID, "failed", map[string]any{"error": safeErrorMessage(err)})
		return Secret{}, err
	}
	objectName := userID
	if record, err := m.deps.Store.GetDatabaseUser(ctx, userID); err == nil {
		objectName = record.Username
	}
	m.audit(ctx, "database.user.password_revealed", serviceID, "", "user", objectName, "ok", nil)
	return secret, nil
}

// ListBackups returns platform backups and refreshes the persisted metadata.
func (m *Manager) ListBackups(ctx context.Context, serviceID string) ([]model.DatabaseBackup, error) {
	serviceID = canonicalServiceID(serviceID)
	if !isSQLService(serviceID) {
		return nil, fmt.Errorf("%w: service %q has no SQL backups", ErrUnsupported, serviceID)
	}
	if _, err := m.requireInstalled(ctx, serviceID); err != nil {
		return nil, err
	}
	backups, err := m.deps.Platform.ListBackups(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	for i := range backups {
		if backups[i].ServiceID == "" {
			backups[i].ServiceID = serviceID
		}
		if backups[i].Engine == "" {
			backups[i].Engine = engineForService(serviceID)
		}
		record := backupFromView(backups[i])
		existing, err := m.deps.Store.GetDatabaseBackup(ctx, record.ID)
		switch {
		case errors.Is(err, store.ErrNotFound):
			if err := m.deps.Store.CreateDatabaseBackup(ctx, record); err != nil {
				return nil, err
			}
		case err != nil:
			return nil, err
		default:
			// The platform view intentionally omits the private path. Preserve
			// the daemon's stored path and kind when refreshing metadata.
			if record.Path == "" {
				record.Path = existing.Path
			}
			if record.Kind == "" {
				record.Kind = existing.Kind
			}
			if record.CreatedAt == 0 {
				record.CreatedAt = existing.CreatedAt
			}
			if err := m.deps.Store.UpdateDatabaseBackup(ctx, record); err != nil {
				return nil, err
			}
		}
	}
	sort.SliceStable(backups, func(i, j int) bool { return backups[i].CreatedAt.After(backups[j].CreatedAt) })
	return backups, nil
}

// DeleteBackup deletes a backup file and its metadata.
func (m *Manager) DeleteBackup(ctx context.Context, serviceID, backupID string) error {
	serviceID = canonicalServiceID(serviceID)
	if !isSQLService(serviceID) {
		return fmt.Errorf("%w: service %q has no SQL backups", ErrUnsupported, serviceID)
	}
	if _, err := m.requireInstalled(ctx, serviceID); err != nil {
		return err
	}
	unlock := m.lockService(serviceID)
	defer unlock()

	if err := m.deps.Platform.DeleteBackup(ctx, serviceID, backupID); err != nil {
		m.audit(ctx, "database.backup.deleted", serviceID, "", "backup", backupID, "failed", map[string]any{"error": safeErrorMessage(err)})
		return err
	}
	if err := m.deps.Store.DeleteDatabaseBackup(ctx, backupID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	m.audit(ctx, "database.backup.deleted", serviceID, "", "backup", backupID, "ok", nil)
	m.emit(ctx, "", "database.backup.deleted", fmt.Sprintf("database backup %s deleted on %s", backupID, serviceID))
	return nil
}

// ExportDatabase streams a logical dump.
func (m *Manager) ExportDatabase(ctx context.Context, serviceID, database string) (io.ReadCloser, error) {
	serviceID = canonicalServiceID(serviceID)
	if !isSQLService(serviceID) {
		return nil, fmt.Errorf("%w: service %q has no SQL export", ErrUnsupported, serviceID)
	}
	if _, err := m.requireActive(ctx, serviceID); err != nil {
		return nil, err
	}
	reader, err := m.deps.Platform.ExportDatabase(ctx, serviceID, database)
	if err != nil {
		return nil, err
	}
	m.audit(ctx, "database.export.started", serviceID, "", "database", database, "ok", nil)
	m.emit(ctx, "", "database.export.started", fmt.Sprintf("database %s export started on %s", database, serviceID))
	return reader, nil
}

// DownloadBackup streams a backup file while the service is stopped.
func (m *Manager) DownloadBackup(ctx context.Context, serviceID, backupID string) (io.ReadCloser, error) {
	serviceID = canonicalServiceID(serviceID)
	if !isSQLService(serviceID) {
		return nil, fmt.Errorf("%w: service %q has no SQL backups", ErrUnsupported, serviceID)
	}
	if _, err := m.requireInstalled(ctx, serviceID); err != nil {
		return nil, err
	}
	return m.deps.Platform.DownloadBackup(ctx, serviceID, backupID)
}

// GetJob returns a persisted job.
func (m *Manager) GetJob(ctx context.Context, jobID string) (model.DatabaseJob, error) {
	record, err := m.deps.Store.GetDatabaseJob(ctx, jobID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return model.DatabaseJob{}, fmt.Errorf("%w: job %q", ErrJobNotFound, jobID)
		}
		return model.DatabaseJob{}, err
	}
	if record.Status == string(model.JobRunning) || record.Status == string(model.JobQueued) {
		m.jobsMu.Lock()
		platformID := m.platformJobs[jobID]
		m.jobsMu.Unlock()
		if platformID != "" {
			if platformJob, platformErr := m.deps.Platform.GetJob(ctx, platformID); platformErr == nil {
				record.Status = string(platformJob.Status)
				record.Progress = platformJob.Progress
				if platformJob.Message != "" {
					record.Message = platformJob.Message
				}
				if platformJob.BackupID != "" {
					record.BackupID = platformJob.BackupID
				}
				record.UpdatedAt = m.now().Unix()
				_ = m.deps.Store.UpdateDatabaseJob(ctx, record)
				if record.Status != string(model.JobRunning) && record.Status != string(model.JobQueued) {
					m.auditJob(ctx, recordToJob(record), string(record.Status))
				}
			}
		}
	}
	return recordToJob(record), nil
}

// CancelJob cancels a running platform job and records the canceled state.
func (m *Manager) CancelJob(ctx context.Context, jobID string) error {
	record, err := m.deps.Store.GetDatabaseJob(ctx, jobID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("%w: job %q", ErrJobNotFound, jobID)
		}
		return err
	}
	// Terminal jobs are immutable. Cancellation is idempotent for a job that
	// already finished or was canceled.
	if record.Status != string(model.JobQueued) && record.Status != string(model.JobRunning) {
		return nil
	}
	m.jobsMu.Lock()
	cancel := m.cancels[jobID]
	platformID := m.platformJobs[jobID]
	m.jobsMu.Unlock()
	if cancel != nil {
		cancel()
	}
	platformJobID := platformID
	if platformJobID == "" {
		platformJobID = jobID
	}
	if err := m.deps.Platform.CancelJob(ctx, platformJobID); err != nil && !errors.Is(err, ErrJobNotFound) {
		return err
	}
	record.Status = string(model.JobCanceled)
	record.UpdatedAt = m.now().Unix()
	if err := m.deps.Store.UpdateDatabaseJob(ctx, record); err != nil {
		return err
	}
	m.audit(ctx, "database.job.updated", record.ServiceID, "", "database", record.DatabaseName, "canceled", map[string]any{"job_id": jobID})
	m.emit(ctx, "", "database.job.updated", fmt.Sprintf("database job %s canceled", jobID))
	return nil
}

// ResolveSiteEnv delegates site environment resolution to the platform. The
// returned map may contain secrets; callers must never persist or log it.
func (m *Manager) ResolveSiteEnv(ctx context.Context, serviceID string, links []model.DatabaseLink) (map[string]string, error) {
	serviceID = canonicalServiceID(serviceID)
	return m.deps.Platform.ResolveSiteEnv(ctx, serviceID, links)
}

// saveGrant persists one grant intent.
func (m *Manager) saveGrant(ctx context.Context, userID, database string, preset model.DatabaseGrantPreset, privileges []string) error {
	if strings.TrimSpace(database) == "" {
		return fmt.Errorf("%w: grant database is required", ErrInvalidRequest)
	}
	custom := "[]"
	if len(privileges) > 0 {
		raw, err := json.Marshal(privileges)
		if err != nil {
			return err
		}
		custom = string(raw)
	}
	return m.deps.Store.SaveDatabaseGrant(ctx, model.DatabaseGrantRecord{
		UserID:       userID,
		DatabaseName: database,
		Preset:       string(preset),
		CustomJSON:   custom,
		UpdatedAt:    m.now().Unix(),
	})
}
