package servicecontrol

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

func testCatalogWithPhpMyAdmin() services.Catalog {
	catalog := testCatalog()
	catalog.Services = append(catalog.Services,
		services.Service{
			ID:          services.IDMariaDB,
			DisplayName: "MariaDB Database",
			Group:       services.GroupMySQLFamily,
			Image:       "mariadb@sha256:" + strings.Repeat("d", 64),
			Ports:       []services.Port{{Host: 3306, Container: 3306, Protocol: "tcp", Loopback: true}},
		},
		services.Service{
			ID:          services.IDPHPMyAdmin,
			DisplayName: "phpMyAdmin",
			Group:       services.GroupStandalone,
			Image:       "phpmyadmin@sha256:" + strings.Repeat("e", 64),
			Ports:       []services.Port{{Host: 8082, Container: 80, Protocol: "http", Loopback: true}},
		},
	)
	return catalog
}

func newTestManagerWithCatalog(t *testing.T, catalog services.Catalog, st Store, helper Helper, preparers map[services.ID]Preparer) *Manager {
	t.Helper()
	manager := NewManager(t.TempDir(), st, helper, catalog, preparers)
	manager.now = func() time.Time { return testNow }
	return manager
}

func activeStatus(id services.ID) helperproto.ServiceStatus {
	return helperproto.ServiceStatus{
		ID:        string(id),
		State:     string(services.StateActive),
		Active:    true,
		Installed: true,
	}
}

func TestManagerActivatePhpMyAdminRequiresActiveDatabase(t *testing.T) {
	helper := &fakeHelper{statuses: []helperproto.ServiceStatus{
		{ID: string(services.IDMySQL), State: string(services.StateInstalledButStopped), Installed: true},
	}}
	store := newFakeStore()
	manager := newTestManagerWithCatalog(t, testCatalogWithPhpMyAdmin(), store, helper, nil)

	err := manager.Activate(context.Background(), services.IDPHPMyAdmin)
	if !errors.Is(err, ErrActiveDatabaseRequired) {
		t.Fatalf("Activate(phpmyadmin) error = %v, want ErrActiveDatabaseRequired", err)
	}
	if !strings.Contains(err.Error(), "active MySQL or MariaDB") {
		t.Fatalf("error = %v, want a clear dependency message", err)
	}
	for _, call := range helper.calls {
		if strings.HasPrefix(call, "helper_activate:") {
			t.Fatalf("helper activated phpmyadmin without a database: %v", helper.calls)
		}
	}
	rec := store.last(t)
	if rec.ID != services.IDPHPMyAdmin || rec.State != services.StateError || !strings.Contains(rec.LastError, "active MySQL or MariaDB") {
		t.Fatalf("saved record = %+v", rec)
	}
}

func TestManagerActivatePhpMyAdminWithActiveMariaDB(t *testing.T) {
	helper := &fakeHelper{statuses: []helperproto.ServiceStatus{activeStatus(services.IDMariaDB)}}
	store := newFakeStore()
	manager := newTestManagerWithCatalog(t, testCatalogWithPhpMyAdmin(), store, helper, nil)

	if err := manager.Activate(context.Background(), services.IDPHPMyAdmin); err != nil {
		t.Fatalf("Activate(phpmyadmin) = %v", err)
	}
	if got := strings.Join(helper.calls, ","); got != "helper_activate:phpmyadmin" {
		t.Fatalf("helper calls = %q", got)
	}
	rec := store.last(t)
	if rec.ID != services.IDPHPMyAdmin || rec.State != services.StateActive || !rec.Active {
		t.Fatalf("saved record = %+v", rec)
	}
}

func TestManagerActivateMySQLFamilyRestartsActivePhpMyAdmin(t *testing.T) {
	helper := &fakeHelper{statuses: []helperproto.ServiceStatus{activeStatus(services.IDPHPMyAdmin)}}
	store := newFakeStore()
	manager := newTestManagerWithCatalog(t, testCatalogWithPhpMyAdmin(), store, helper, nil)

	if err := manager.Activate(context.Background(), services.IDMariaDB); err != nil {
		t.Fatalf("Activate(mariadb) = %v", err)
	}
	want := []string{"helper_activate:mariadb", "helper_restart:phpmyadmin"}
	if got := strings.Join(helper.calls, ","); got != strings.Join(want, ",") {
		t.Fatalf("helper calls = %q, want %q", got, strings.Join(want, ","))
	}
	rec, ok := store.records[services.IDPHPMyAdmin]
	if !ok || rec.State != services.StateActive || !rec.Active {
		t.Fatalf("phpmyadmin record = %+v, present = %t", rec, ok)
	}
}

func TestManagerActivateMySQLFamilyLeavesInactivePhpMyAdmin(t *testing.T) {
	helper := &fakeHelper{statuses: []helperproto.ServiceStatus{
		{ID: string(services.IDPHPMyAdmin), State: string(services.StateInstalledButStopped), Installed: true},
	}}
	store := newFakeStore()
	manager := newTestManagerWithCatalog(t, testCatalogWithPhpMyAdmin(), store, helper, nil)

	if err := manager.Activate(context.Background(), services.IDMySQL); err != nil {
		t.Fatalf("Activate(mysql) = %v", err)
	}
	for _, call := range helper.calls {
		if strings.HasPrefix(call, "helper_restart:") {
			t.Fatalf("inactive phpmyadmin was restarted: %v", helper.calls)
		}
	}
}

func TestManagerActivateMySQLFamilyReportsPhpMyAdminRestartFailure(t *testing.T) {
	helper := &fakeHelper{
		statuses:   []helperproto.ServiceStatus{activeStatus(services.IDPHPMyAdmin)},
		restartErr: errors.New("restart failed"),
	}
	store := newFakeStore()
	manager := newTestManagerWithCatalog(t, testCatalogWithPhpMyAdmin(), store, helper, nil)

	err := manager.Activate(context.Background(), services.IDMySQL)
	if err == nil || !strings.Contains(err.Error(), "restart failed") {
		t.Fatalf("Activate(mysql) error = %v", err)
	}
	if rec := store.records[services.IDMySQL]; rec.State != services.StateActive || !rec.Active {
		t.Fatalf("mysql record after phpMyAdmin restart failure = %+v", rec)
	}
	if rec := store.records[services.IDPHPMyAdmin]; rec.State != services.StateError || !strings.Contains(rec.LastError, "restart failed") {
		t.Fatalf("phpmyadmin record = %+v", rec)
	}
}

func TestManagerRotateCredentialsRestartsActivePhpMyAdmin(t *testing.T) {
	rotated := Credentials{Engine: "mysql", Username: "devstack", Password: "new-secret", Database: "devstack", Host: "127.0.0.1", Port: 3306}
	prep := &fakePreparer{rotated: rotated}
	helper := &fakeHelper{statuses: []helperproto.ServiceStatus{activeStatus(services.IDPHPMyAdmin)}}
	store := newFakeStore()
	manager := newTestManagerWithCatalog(t, testCatalogWithPhpMyAdmin(), store, helper,
		map[services.ID]Preparer{services.IDMySQL: prep})

	got, err := manager.RotateCredentials(context.Background(), services.IDMySQL)
	if err != nil {
		t.Fatalf("RotateCredentials() = %v", err)
	}
	if got != rotated {
		t.Fatalf("credentials = %+v, want %+v", got, rotated)
	}
	want := []string{"rotate_credentials:mysql", "helper_restart:phpmyadmin"}
	if calls := strings.Join(append(prep.calls, helper.calls...), ","); calls != strings.Join(want, ",") {
		t.Fatalf("calls = %q, want %q", calls, strings.Join(want, ","))
	}
	if rec := store.records[services.IDPHPMyAdmin]; rec.State != services.StateActive || !rec.Active {
		t.Fatalf("phpmyadmin record = %+v", rec)
	}
}

func TestManagerRotateCredentialsReportsPhpMyAdminRestartFailure(t *testing.T) {
	rotated := Credentials{Engine: "mysql", Username: "devstack", Password: "new-secret"}
	prep := &fakePreparer{rotated: rotated}
	helper := &fakeHelper{
		statuses:   []helperproto.ServiceStatus{activeStatus(services.IDPHPMyAdmin)},
		restartErr: errors.New("restart failed"),
	}
	store := newFakeStore()
	manager := newTestManagerWithCatalog(t, testCatalogWithPhpMyAdmin(), store, helper,
		map[services.ID]Preparer{services.IDMySQL: prep})

	got, err := manager.RotateCredentials(context.Background(), services.IDMySQL)
	if err == nil || !strings.Contains(err.Error(), "restart failed") {
		t.Fatalf("RotateCredentials() error = %v", err)
	}
	if got != rotated {
		t.Fatalf("rotated credentials = %+v, want %+v", got, rotated)
	}
	if rec := store.records[services.IDPHPMyAdmin]; rec.State != services.StateError {
		t.Fatalf("phpmyadmin record = %+v", rec)
	}
}

func TestManagerPhpMyAdminDeactivateAndUninstallAllowed(t *testing.T) {
	helper := &fakeHelper{}
	store := newFakeStore(services.Record{ID: services.IDPHPMyAdmin, State: services.StateActive, Active: true})
	manager := newTestManagerWithCatalog(t, testCatalogWithPhpMyAdmin(), store, helper, nil)

	if err := manager.Deactivate(context.Background(), services.IDPHPMyAdmin); err != nil {
		t.Fatalf("Deactivate(phpmyadmin) = %v", err)
	}
	if _, err := manager.Uninstall(context.Background(), services.IDPHPMyAdmin, false); err != nil {
		t.Fatalf("Uninstall(phpmyadmin) = %v", err)
	}
}
