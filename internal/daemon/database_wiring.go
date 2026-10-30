package daemon

import (
	"context"
	"log/slog"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

// newDatabaseManager constructs the daemon database manager from the platform
// adapter, service state, store, and event/audit sinks.
func newDatabaseManager(st *store.Store, platform databases.Platform, services databases.ServiceState, logger *slog.Logger) *databases.Manager {
	return databases.NewManager(databases.Deps{
		Store:    st,
		Platform: platform,
		Services: services,
		Sites:    st,
		Events:   st,
		Audit:    st,
		Logger:   logger,
	})
}

// databaseEnvResolver adapts the manager to the site/terminal dependency. The
// returned map may contain passwords; callers must never persist or log it.
func databaseEnvResolver(manager *databases.Manager) func(context.Context, model.Site) (map[string]string, error) {
	return func(ctx context.Context, site model.Site) (map[string]string, error) {
		return manager.SiteDatabaseEnv(ctx, site.ID)
	}
}

// databaseLinksResolver adapts the manager to the password-free link metadata
// consumed by the Windows agent.
func databaseLinksResolver(manager *databases.Manager) func(context.Context, model.Site) ([]model.DatabaseLink, error) {
	return func(ctx context.Context, site model.Site) ([]model.DatabaseLink, error) {
		return manager.ListSiteLinks(ctx, site.ID)
	}
}
