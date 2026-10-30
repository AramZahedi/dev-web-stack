package databases

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/services"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

var envSlugPattern = regexp.MustCompile(`[^A-Z0-9]+`)

// ErrSiteNotFound distinguishes a missing site reference from a missing
// database object so the API can return the generic NOT_FOUND code.
var ErrSiteNotFound = errors.New("database: site not found")

// ListSiteLinks returns every database link for a site with live connection
// details. A stopped service or externally dropped database is reported as
// unavailable rather than failing the whole list.
func (m *Manager) ListSiteLinks(ctx context.Context, siteRef string) ([]model.DatabaseLink, error) {
	site, err := m.resolveSite(ctx, siteRef)
	if err != nil {
		return nil, err
	}
	records, err := m.deps.Store.ListDatabaseLinksBySite(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	databasesByService := make(map[string]map[string]model.Database)
	views := make(map[string]services.View)
	for _, record := range records {
		serviceID := canonicalServiceID(record.ServiceID)
		if _, ok := views[serviceID]; !ok {
			view, viewErr := m.serviceView(ctx, serviceID)
			if viewErr == nil {
				views[serviceID] = view
			}
		}
		view, serviceKnown := views[serviceID]
		if serviceKnown && view.Active {
			if _, ok := databasesByService[serviceID]; !ok {
				list, listErr := m.deps.Platform.ListDatabases(ctx, serviceID)
				if listErr != nil {
					return nil, listErr
				}
				byName := make(map[string]model.Database, len(list))
				for _, database := range list {
					byName[database.Name] = database
				}
				databasesByService[serviceID] = byName
			}
		}
	}
	links := make([]model.DatabaseLink, 0, len(records))
	for _, record := range records {
		link, _ := m.buildLink(ctx, record, views, databasesByService)
		links = append(links, link)
	}
	sort.SliceStable(links, func(i, j int) bool {
		if links[i].Primary != links[j].Primary {
			return links[i].Primary
		}
		return links[i].Database < links[j].Database
	})
	return links, nil
}

// buildLink assembles one link view. Errors are reported on the link rather
// than failing the list.
func (m *Manager) buildLink(ctx context.Context, record model.DatabaseLinkRecord, views map[string]services.View, databases map[string]map[string]model.Database) (model.DatabaseLink, error) {
	serviceID := canonicalServiceID(record.ServiceID)
	engine := engineForService(serviceID)
	link := model.DatabaseLink{
		ID:        record.ID,
		SiteID:    record.SiteID,
		SiteName:  record.SiteName,
		ServiceID: serviceID,
		Engine:    engine,
		Database:  record.DatabaseName,
		UserID:    record.UserID,
		Primary:   record.Primary,
	}
	var problems []string
	user, userErr := m.deps.Store.GetDatabaseUser(ctx, record.UserID)
	if userErr != nil {
		problems = append(problems, "database user no longer exists")
	} else {
		link.Username = user.Username
		link.Host = user.Host
		link.PasswordManaged = user.PasswordManaged
	}
	view, serviceKnown := views[serviceID]
	if !serviceKnown {
		problems = append(problems, "database service is not installed")
	} else if !view.Active {
		problems = append(problems, "database service is not active")
	}
	if serviceKnown && view.Active {
		byName, listed := databases[serviceID]
		if listed {
			if _, ok := byName[record.DatabaseName]; !ok {
				problems = append(problems, "database no longer exists")
			}
		}
	}
	port := defaultServicePort(engine)
	if serviceKnown {
		port = servicePort(view, engine)
	}
	link.Connection = model.DatabaseConnection{
		Engine:          engine,
		Host:            "127.0.0.1",
		Port:            port,
		Database:        record.DatabaseName,
		Username:        link.Username,
		PasswordManaged: link.PasswordManaged,
	}
	link.Connection.Env = metadataEnv(link)
	if link.PasswordManaged {
		link.Connection.URL = safeConnectionURL(engine, port, record.DatabaseName, link.Username)
	}
	link.Available = len(problems) == 0
	if len(problems) > 0 {
		link.Error = strings.Join(problems, "; ")
	}
	return link, nil
}

// LinkDatabase links an existing site to an existing database/user. It never
// grants access automatically; a missing grant is surfaced as a warning.
func (m *Manager) LinkDatabase(ctx context.Context, siteRef string, req LinkDatabaseRequest) (model.DatabaseLink, error) {
	site, err := m.resolveSite(ctx, siteRef)
	if err != nil {
		return model.DatabaseLink{}, err
	}
	serviceID := canonicalServiceID(req.ServiceID)
	if isRedisService(serviceID) {
		return model.DatabaseLink{}, fmt.Errorf("%w: Redis users cannot be linked to sites", ErrUnsupported)
	}
	if _, err := m.requireActive(ctx, serviceID); err != nil {
		return model.DatabaseLink{}, err
	}
	database := strings.TrimSpace(req.Database)
	if database == "" {
		return model.DatabaseLink{}, fmt.Errorf("%w: database is required", ErrInvalidRequest)
	}
	user, err := m.deps.Store.GetDatabaseUser(ctx, req.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return model.DatabaseLink{}, fmt.Errorf("%w: user %q", ErrNotFound, req.UserID)
		}
		return model.DatabaseLink{}, err
	}
	if canonicalServiceID(user.ServiceID) != serviceID {
		return model.DatabaseLink{}, fmt.Errorf("%w: user %q does not belong to service %q", ErrInvalidRequest, user.Username, serviceID)
	}
	if strings.TrimSpace(req.Password) != "" {
		return model.DatabaseLink{}, fmt.Errorf("%w: the platform contract cannot store an external password; create a metadata-only link and set the password in the engine", ErrUnsupported)
	}
	if err := m.ensureDatabaseExists(ctx, serviceID, database); err != nil {
		return model.DatabaseLink{}, err
	}
	unlock := m.lockService(serviceID)
	defer unlock()

	records, err := m.deps.Store.ListDatabaseLinksBySite(ctx, site.ID)
	if err != nil {
		return model.DatabaseLink{}, err
	}
	primary := req.Primary
	if len(records) == 0 || !hasPrimaryLink(records) {
		primary = true
	}
	for _, record := range records {
		if canonicalServiceID(record.ServiceID) == serviceID && record.DatabaseName == database {
			return model.DatabaseLink{}, fmt.Errorf("%w: database %q is already linked to site %q", ErrConflict, database, site.Name)
		}
	}
	record := model.DatabaseLinkRecord{
		ID:           m.newID("lnk"),
		SiteID:       site.ID,
		SiteName:     site.Name,
		ServiceID:    serviceID,
		DatabaseName: database,
		UserID:       user.ID,
		Primary:      primary,
		CreatedAt:    m.now().Unix(),
	}
	if err := m.deps.Store.CreateDatabaseLink(ctx, record); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return model.DatabaseLink{}, fmt.Errorf("%w: database %q is already linked to site %q", ErrConflict, database, site.Name)
		}
		return model.DatabaseLink{}, err
	}
	link := m.linkFromRecord(ctx, record, user)
	grants := m.storedGrants(ctx, user.ID)
	if live, liveErr := m.existingUser(ctx, serviceID, user.ID); liveErr == nil {
		grants = live.Grants
	}
	if !grantsHaveDatabase(grants, database) {
		link.Error = "database user has no grant on this database"
	}
	m.audit(ctx, "database.link.changed", serviceID, site.ID, "database_link", database, "ok", map[string]any{"link_id": record.ID, "user_id": user.ID, "primary": primary})
	m.emit(ctx, site.ID, "database.link.changed", fmt.Sprintf("site %s linked to database %s on %s", site.Name, database, serviceID))
	return link, nil
}

// UnlinkDatabase deletes only the link. Database, user, grants, passwords, and
// backups are untouched.
func (m *Manager) UnlinkDatabase(ctx context.Context, siteRef, linkID string) error {
	site, err := m.resolveSite(ctx, siteRef)
	if err != nil {
		return err
	}
	record, err := m.deps.Store.GetDatabaseLink(ctx, linkID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("%w: link %q", ErrNotFound, linkID)
		}
		return err
	}
	if record.SiteID != site.ID {
		return fmt.Errorf("%w: link %q does not belong to site %q", ErrNotFound, linkID, site.Name)
	}
	if err := m.deps.Store.DeleteDatabaseLink(ctx, linkID); err != nil {
		return err
	}
	m.audit(ctx, "database.link.changed", canonicalServiceID(record.ServiceID), site.ID, "database_link", record.DatabaseName, "ok", map[string]any{"link_id": linkID, "unlinked": true})
	m.emit(ctx, site.ID, "database.link.changed", fmt.Sprintf("site %s unlinked from database %s", site.Name, record.DatabaseName))
	return nil
}

// SiteDatabaseEnv resolves the environment for every link of a site. Managed
// links are resolved by the privileged platform; metadata-only links use the
// safe daemon-side values. The returned map may contain secrets and must never
// be persisted or logged.
func (m *Manager) SiteDatabaseEnv(ctx context.Context, siteRef string) (map[string]string, error) {
	site, err := m.resolveSite(ctx, siteRef)
	if err != nil {
		return nil, err
	}
	links, err := m.ListSiteLinks(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	if len(links) == 0 {
		m.audit(ctx, "database.env.resolved", "", site.ID, "site", site.Name, "ok", nil)
		return map[string]string{}, nil
	}
	allMetadataOnly := true
	for _, link := range links {
		if link.PasswordManaged {
			allMetadataOnly = false
			break
		}
	}
	if allMetadataOnly {
		env, err := SiteEnv(links)
		if err != nil {
			m.audit(ctx, "database.env.resolved", "", site.ID, "site", site.Name, "failed", map[string]any{"error": safeErrorMessage(err)})
			return nil, err
		}
		m.audit(ctx, "database.env.resolved", "", site.ID, "site", site.Name, "ok", nil)
		return env, nil
	}
	grouped := make(map[string][]model.DatabaseLink)
	for _, link := range links {
		serviceID := canonicalServiceID(link.ServiceID)
		grouped[serviceID] = append(grouped[serviceID], link)
	}
	env := map[string]string{}
	for serviceID, serviceLinks := range grouped {
		resolved, err := m.deps.Platform.ResolveSiteEnv(ctx, serviceID, serviceLinks)
		if err != nil {
			m.audit(ctx, "database.env.resolved", "", site.ID, "site", site.Name, "failed", map[string]any{"error": safeErrorMessage(err)})
			return nil, err
		}
		for key, value := range resolved {
			env[key] = value
		}
	}
	m.audit(ctx, "database.env.resolved", "", site.ID, "site", site.Name, "ok", nil)
	return env, nil
}

// GuardSite blocks a site start only when a different member of an exclusive
// engine group is active. A stopped exact service is a visible warning.
func (m *Manager) GuardSite(ctx context.Context, site model.Site) error {
	records, err := m.deps.Store.ListDatabaseLinksBySite(ctx, site.ID)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}
	views, err := m.deps.Services.List(ctx)
	if err != nil {
		return err
	}
	byID := make(map[services.ID]services.View, len(views))
	for _, view := range views {
		byID[view.ID] = view
	}
	for _, record := range records {
		serviceID := services.ID(canonicalServiceID(record.ServiceID))
		view, installed := byID[serviceID]
		group := view.Group
		if !installed {
			// An uninstalled linked service still belongs to its catalog group.
			// Resolve the group from the catalog so a conflicting active engine
			// (for example MariaDB while the site links MySQL) blocks the start.
			if catalogService, ok := services.DefaultCatalog().Get(serviceID); ok {
				group = catalogService.Group
			}
		}
		if installed && view.Active {
			continue
		}
		for _, other := range views {
			if other.ID == serviceID || !other.Active {
				continue
			}
			if group != "" && other.Group == group && group.Exclusive() {
				return fmt.Errorf("%w: site %s links %s but %s is active on the same exclusive port group; deactivate %s or switch the link", ErrConflict, site.Name, serviceID, other.ID, other.ID)
			}
		}
		if !installed {
			m.emit(ctx, site.ID, "database.service.offline", fmt.Sprintf("site %s: database service %s is not installed", site.Name, serviceID))
			continue
		}
		m.emit(ctx, site.ID, "database.service.offline", fmt.Sprintf("site %s: database service %s is stopped", site.Name, serviceID))
	}
	return nil
}

// DeleteServiceLinks removes every link to a service after an uninstall with
// delete-data succeeded. It never touches databases, users, or backups.
func (m *Manager) DeleteServiceLinks(ctx context.Context, serviceID string) error {
	serviceID = canonicalServiceID(serviceID)
	removed, err := m.deps.Store.DeleteDatabaseLinksByService(ctx, serviceID)
	if err != nil {
		return err
	}
	if removed > 0 {
		m.audit(ctx, "database.link.changed", serviceID, "", "database_link", serviceID, "ok", map[string]any{"removed": removed})
		m.emit(ctx, "", "database.link.changed", fmt.Sprintf("removed %d site links to service %s after uninstall", removed, serviceID))
	}
	return nil
}

// ensureLinkedSitesStopped blocks destructive database operations while a
// linked site is running.
func (m *Manager) ensureLinkedSitesStopped(ctx context.Context, serviceID, database string) error {
	records, err := m.deps.Store.ListDatabaseLinksByService(ctx, canonicalServiceID(serviceID))
	if err != nil {
		return err
	}
	var running []string
	for _, record := range records {
		if record.DatabaseName != database {
			continue
		}
		site, err := m.deps.Sites.GetSite(ctx, record.SiteID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return err
		}
		if site.Status == model.StatusRunning || site.DesiredState == model.DesiredRunning {
			running = append(running, site.Name)
		}
	}
	if len(running) > 0 {
		sort.Strings(running)
		return fmt.Errorf("%w: stop linked sites first: %s", ErrConflict, strings.Join(running, ", "))
	}
	return nil
}

// ensureDatabaseExists verifies a database name against the live engine.
func (m *Manager) ensureDatabaseExists(ctx context.Context, serviceID, database string) error {
	exists, err := m.databaseExists(ctx, serviceID, database)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return fmt.Errorf("%w: database %q", ErrNotFound, database)
}

// databaseExists reports whether the live engine currently has a database.
// Inventory failures are returned so destructive operations fail closed.
func (m *Manager) databaseExists(ctx context.Context, serviceID, database string) (bool, error) {
	databases, err := m.deps.Platform.ListDatabases(ctx, serviceID)
	if err != nil {
		return false, err
	}
	for _, candidate := range databases {
		if candidate.Name == database {
			return true, nil
		}
	}
	return false, nil
}

// resolveSite resolves a site id first and then its unique name.
func (m *Manager) resolveSite(ctx context.Context, ref string) (model.Site, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return model.Site{}, fmt.Errorf("%w: site reference is empty", ErrInvalidRequest)
	}
	site, err := m.deps.Sites.GetSite(ctx, ref)
	if err == nil {
		return site, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return model.Site{}, err
	}
	site, err = m.deps.Sites.GetSiteByName(ctx, strings.ToLower(ref))
	if err == nil {
		return site, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return model.Site{}, err
	}
	return model.Site{}, fmt.Errorf("%w: site %q", ErrSiteNotFound, ref)
}

// linkFromRecord assembles a link view without querying the engine.
func (m *Manager) linkFromRecord(_ context.Context, record model.DatabaseLinkRecord, user model.DatabaseUserRecord) model.DatabaseLink {
	serviceID := canonicalServiceID(record.ServiceID)
	engine := engineForService(serviceID)
	port := defaultServicePort(engine)
	link := model.DatabaseLink{
		ID:              record.ID,
		SiteID:          record.SiteID,
		SiteName:        record.SiteName,
		ServiceID:       serviceID,
		Engine:          engine,
		Database:        record.DatabaseName,
		UserID:          user.ID,
		Username:        user.Username,
		Host:            user.Host,
		Primary:         record.Primary,
		PasswordManaged: user.PasswordManaged,
		Available:       true,
		Connection: model.DatabaseConnection{
			Engine:          engine,
			Host:            "127.0.0.1",
			Port:            port,
			Database:        record.DatabaseName,
			Username:        user.Username,
			PasswordManaged: user.PasswordManaged,
		},
	}
	link.Connection.Env = metadataEnv(link)
	if link.PasswordManaged {
		link.Connection.URL = safeConnectionURL(engine, port, record.DatabaseName, user.Username)
	}
	return link
}

// safeConnectionURL builds a URL without a password.
func safeConnectionURL(engine model.DatabaseEngine, port int, database, username string) string {
	scheme := connectionScheme(engine)
	if scheme == "" || username == "" {
		return ""
	}
	return (&url.URL{
		Scheme: scheme,
		User:   url.User(username),
		Host:   fmt.Sprintf("127.0.0.1:%d", port),
		Path:   "/" + database,
	}).String()
}

// SiteEnv builds the safe, non-secret part of a site's database environment.
// The privileged platform adds password-bearing keys for managed links.
func SiteEnv(links []model.DatabaseLink) (map[string]string, error) {
	primaryIndex := -1
	for i, link := range links {
		if !link.Primary {
			continue
		}
		if primaryIndex >= 0 {
			return nil, fmt.Errorf("%w: multiple primary database links", ErrConflict)
		}
		primaryIndex = i
	}
	if primaryIndex < 0 && len(links) > 0 {
		primaryIndex = 0
	}
	env := make(map[string]string, len(links)*7)
	for i, link := range links {
		prefix := "DB_"
		if i != primaryIndex {
			prefix = "DB_" + envSlug(link.Database) + "_"
		}
		engine := link.Engine
		if engine == "" {
			engine = link.Connection.Engine
		}
		port := link.Connection.Port
		if port == 0 {
			port = defaultServicePort(engine)
		}
		username := link.Username
		if username == "" {
			username = link.Connection.Username
		}
		env[prefix+"CONNECTION"] = dbConnectionValue(engine)
		env[prefix+"HOST"] = "127.0.0.1"
		env[prefix+"PORT"] = fmt.Sprintf("%d", port)
		env[prefix+"DATABASE"] = link.Database
		env[prefix+"USERNAME"] = username
		if link.PasswordManaged && link.Connection.URL != "" {
			if i == primaryIndex {
				env["DATABASE_URL"] = link.Connection.URL
			} else {
				env[prefix+"URL"] = link.Connection.URL
			}
		}
	}
	return env, nil
}

// metadataEnv returns the non-secret environment keys for one link.
func metadataEnv(link model.DatabaseLink) map[string]string {
	engine := link.Engine
	if engine == "" {
		engine = link.Connection.Engine
	}
	port := link.Connection.Port
	if port == 0 {
		port = defaultServicePort(engine)
	}
	username := link.Username
	if username == "" {
		username = link.Connection.Username
	}
	prefix := "DB_"
	if !link.Primary {
		prefix = "DB_" + envSlug(link.Database) + "_"
	}
	return map[string]string{
		prefix + "CONNECTION": dbConnectionValue(engine),
		prefix + "HOST":       "127.0.0.1",
		prefix + "PORT":       fmt.Sprintf("%d", port),
		prefix + "DATABASE":   link.Database,
		prefix + "USERNAME":   username,
	}
}

// envSlug converts a database name into an env-variable segment.
func envSlug(value string) string {
	slug := strings.ToUpper(strings.TrimSpace(value))
	slug = envSlugPattern.ReplaceAllString(slug, "_")
	slug = strings.Trim(slug, "_")
	if slug == "" {
		return "DB"
	}
	if slug[0] >= '0' && slug[0] <= '9' {
		return "DB_" + slug
	}
	return slug
}

// hasPrimaryLink reports whether a site already has a primary link.
func hasPrimaryLink(records []model.DatabaseLinkRecord) bool {
	for _, record := range records {
		if record.Primary {
			return true
		}
	}
	return false
}

// userHasGrant reports whether a user has any grant on a database.
func userHasGrant(user model.DatabaseUser, database string) bool {
	return grantsHaveDatabase(user.Grants, database)
}

// grantsHaveDatabase reports whether any grant targets a database.
func grantsHaveDatabase(grants []model.DatabaseGrant, database string) bool {
	for _, grant := range grants {
		if grant.Database == database {
			return true
		}
	}
	return false
}
