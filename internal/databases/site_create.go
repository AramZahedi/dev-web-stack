package databases

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

// ProvisionSiteDatabase creates or reuses a database/user pair before a site
// row exists. It never grants access to an existing user.
func (m *Manager) ProvisionSiteDatabase(ctx context.Context, req SiteDatabaseRequest) (ProvisionedDatabase, error) {
	serviceID := canonicalServiceID(req.ServiceID)
	if isRedisService(serviceID) {
		return ProvisionedDatabase{}, fmt.Errorf("%w: Redis users cannot be linked to sites", ErrUnsupported)
	}
	if _, err := m.requireActive(ctx, serviceID); err != nil {
		return ProvisionedDatabase{}, err
	}
	if strings.TrimSpace(req.Password) != "" {
		return ProvisionedDatabase{}, fmt.Errorf("%w: the platform contract cannot store an external password; use a metadata-only link", ErrUnsupported)
	}
	req.ServiceID = serviceID

	var database model.Database
	var err error
	switch {
	case req.NewDatabase != nil:
		database, err = m.CreateDatabase(ctx, serviceID, *req.NewDatabase)
		if err != nil {
			return ProvisionedDatabase{}, err
		}
	case strings.TrimSpace(req.Database) != "":
		database, err = m.existingDatabase(ctx, serviceID, req.Database)
		if err != nil {
			return ProvisionedDatabase{}, err
		}
	default:
		return ProvisionedDatabase{}, fmt.Errorf("%w: choose an existing database or create a new one", ErrInvalidRequest)
	}

	var user model.DatabaseUser
	warning := ""
	switch {
	case req.NewUser != nil:
		create := *req.NewUser
		if len(create.Grants) == 0 {
			preset := req.Grant
			if preset == "" {
				preset = model.GrantReadWriteSchema
			}
			create.Grants = []model.DatabaseGrant{{Database: database.Name, Preset: preset}}
		}
		user, err = m.CreateUser(ctx, serviceID, create)
		if err != nil {
			return ProvisionedDatabase{}, err
		}
	case strings.TrimSpace(req.UserID) != "":
		user, err = m.existingUser(ctx, serviceID, req.UserID)
		if err != nil {
			return ProvisionedDatabase{}, err
		}
		if !userHasGrant(user, database.Name) {
			warning = fmt.Sprintf("database user %s has no grant on database %s; grant access before relying on this link", user.Username, database.Name)
		}
	default:
		return ProvisionedDatabase{}, fmt.Errorf("%w: choose an existing user or create a new one", ErrInvalidRequest)
	}

	return ProvisionedDatabase{
		Database: database,
		User:     user,
		Warning:  warning,
		Request:  req,
	}, nil
}

// LinkProvisioned creates the site link for a provisioned database/user pair.
// It never re-grants or recreates anything.
func (m *Manager) LinkProvisioned(ctx context.Context, siteID string, provisioned ProvisionedDatabase) (SiteDatabaseResult, error) {
	site, err := m.deps.Sites.GetSite(ctx, siteID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return SiteDatabaseResult{}, fmt.Errorf("%w: site %q", ErrNotFound, siteID)
		}
		return SiteDatabaseResult{}, err
	}
	serviceID := canonicalServiceID(provisioned.Request.ServiceID)
	if serviceID == "" {
		serviceID = canonicalServiceID(provisioned.Database.ServiceID)
	}
	if serviceID == "" {
		return SiteDatabaseResult{}, fmt.Errorf("%w: service is required", ErrInvalidRequest)
	}
	unlock := m.lockService(serviceID)
	defer unlock()

	records, err := m.deps.Store.ListDatabaseLinksBySite(ctx, site.ID)
	if err != nil {
		return SiteDatabaseResult{}, err
	}
	primary := provisioned.Request.Primary
	if len(records) == 0 || !hasPrimaryLink(records) {
		primary = true
	}
	record := model.DatabaseLinkRecord{
		ID:           m.newID("lnk"),
		SiteID:       site.ID,
		SiteName:     site.Name,
		ServiceID:    serviceID,
		DatabaseName: provisioned.Database.Name,
		UserID:       provisioned.User.ID,
		Primary:      primary,
		CreatedAt:    m.now().Unix(),
	}
	if err := m.deps.Store.CreateDatabaseLink(ctx, record); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return SiteDatabaseResult{}, fmt.Errorf("%w: database %q is already linked to site %q", ErrConflict, record.DatabaseName, site.Name)
		}
		return SiteDatabaseResult{}, err
	}
	link := m.linkFromRecord(ctx, record, model.DatabaseUserRecord{
		ID:              provisioned.User.ID,
		ServiceID:       provisioned.User.ServiceID,
		Engine:          provisioned.User.Engine,
		Username:        provisioned.User.Username,
		Host:            provisioned.User.Host,
		PasswordManaged: provisioned.User.PasswordManaged,
	})
	if provisioned.Warning != "" {
		link.Error = provisioned.Warning
	}
	m.audit(ctx, "database.link.changed", serviceID, site.ID, "database_link", record.DatabaseName, "ok", map[string]any{
		"link_id": record.ID, "user_id": provisioned.User.ID, "primary": primary,
	})
	m.emit(ctx, site.ID, "database.link.changed", fmt.Sprintf("site %s linked to database %s on %s", site.Name, record.DatabaseName, serviceID))
	return SiteDatabaseResult{
		Link:     link,
		Database: provisioned.Database,
		User:     provisioned.User,
		Warning:  provisioned.Warning,
	}, nil
}

// CreateSiteDatabase provisions and links a database for an existing site.
func (m *Manager) CreateSiteDatabase(ctx context.Context, siteRef string, req SiteDatabaseRequest) (SiteDatabaseResult, error) {
	site, err := m.resolveSite(ctx, siteRef)
	if err != nil {
		return SiteDatabaseResult{}, err
	}
	provisioned, err := m.ProvisionSiteDatabase(ctx, req)
	if err != nil {
		return SiteDatabaseResult{}, err
	}
	return m.LinkProvisioned(ctx, site.ID, provisioned)
}

// existingDatabase returns the live engine view for a database.
func (m *Manager) existingDatabase(ctx context.Context, serviceID, name string) (model.Database, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return model.Database{}, fmt.Errorf("%w: database is required", ErrInvalidRequest)
	}
	databases, err := m.deps.Platform.ListDatabases(ctx, serviceID)
	if err != nil {
		return model.Database{}, err
	}
	for _, database := range databases {
		if database.Name == name {
			if database.ServiceID == "" {
				database.ServiceID = serviceID
			}
			if database.Engine == "" {
				database.Engine = engineForService(serviceID)
			}
			return database, nil
		}
	}
	return model.Database{}, fmt.Errorf("%w: database %q", ErrNotFound, name)
}

// existingUser returns a user with live grants, falling back to persisted
// metadata when the engine inventory is unavailable.
func (m *Manager) existingUser(ctx context.Context, serviceID, userID string) (model.DatabaseUser, error) {
	users, err := m.ListUsers(ctx, serviceID)
	if err == nil {
		for _, user := range users {
			if user.ID == userID {
				return user, nil
			}
		}
		return model.DatabaseUser{}, fmt.Errorf("%w: user %q", ErrNotFound, userID)
	}
	record, storeErr := m.deps.Store.GetDatabaseUser(ctx, userID)
	if storeErr != nil {
		if errors.Is(storeErr, store.ErrNotFound) {
			return model.DatabaseUser{}, fmt.Errorf("%w: user %q", ErrNotFound, userID)
		}
		return model.DatabaseUser{}, storeErr
	}
	return m.userView(ctx, serviceID, record, nil), nil
}
