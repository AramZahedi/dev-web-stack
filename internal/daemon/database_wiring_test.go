package daemon

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/servicecontrol"
	"github.com/AramZahedi/dev-web-stack/internal/services"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

type fakeDatabaseServiceState struct{}

func (fakeDatabaseServiceState) Get(context.Context, services.ID) (services.View, error) {
	return services.View{ID: services.IDMySQL, State: services.StateActive, Active: true, Installed: true}, nil
}

func (fakeDatabaseServiceState) List(context.Context) ([]services.View, error) {
	return []services.View{{ID: services.IDMySQL, State: services.StateActive, Active: true, Installed: true}}, nil
}

func TestDatabaseWiringBuildsManagerAndResolvers(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := newDatabaseManager(st, servicecontrol.NewHelperDockerAPI(nil), fakeDatabaseServiceState{}, logger)
	if manager == nil {
		t.Fatal("database manager is nil")
	}
	if databaseEnvResolver(manager) == nil {
		t.Fatal("database env resolver is nil")
	}
	if databaseLinksResolver(manager) == nil {
		t.Fatal("database links resolver is nil")
	}
	// A manager without a platform must fail closed rather than panic.
	if _, err := manager.ListDatabases(context.Background(), "mysql"); err == nil {
		t.Fatal("expected an error from an unconfigured platform")
	}
}
