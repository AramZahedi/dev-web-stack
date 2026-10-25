package services_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/services"
)

type fakeStore struct {
	records map[services.ID]services.Record
	err     error
}

func (f *fakeStore) GetServiceState(_ context.Context, id services.ID) (services.Record, error) {
	if f.err != nil {
		return services.Record{}, f.err
	}
	rec, ok := f.records[id]
	if !ok {
		return services.Record{}, services.ErrNotFound
	}
	return rec, nil
}

func (f *fakeStore) ListServiceStates(_ context.Context) ([]services.Record, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []services.Record
	for _, r := range f.records {
		out = append(out, r)
	}
	return out, nil
}

func TestCoordinatorListCombinesCatalogAndStore(t *testing.T) {
	ctx := context.Background()
	cat := services.Catalog{
		Services: []services.Service{
			{
				ID:          services.IDNginx,
				DisplayName: "Nginx Web Server",
				Group:       services.GroupWeb,
				Image:       "fixture-nginx@sha256:1111111111111111111111111111111111111111111111111111111111111111",
				Ports: []services.Port{
					{Host: 80, Container: 80, Protocol: "http", Loopback: true},
				},
			},
			{
				ID:          services.IDMySQL,
				DisplayName: "MySQL Database",
				Group:       services.GroupMySQLFamily,
				Image:       "fixture-mysql@sha256:2222222222222222222222222222222222222222222222222222222222222222",
				Ports: []services.Port{
					{Host: 3306, Container: 3306, Protocol: "tcp", Loopback: true},
				},
			},
		},
	}

	st := &fakeStore{
		records: map[services.ID]services.Record{
			services.IDMySQL: {
				ID:        services.IDMySQL,
				State:     services.StateInstalled,
				Active:    true,
				Image:     "fixture-mysql@sha256:2222222222222222222222222222222222222222222222222222222222222222",
				UpdatedAt: time.Now(),
			},
		},
	}

	coord := services.NewCoordinator(cat, st, "/data/devstack")

	list, err := coord.List(ctx)
	if err != nil {
		t.Fatalf("List() err = %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List() returned %d views, want 2", len(list))
	}

	// First: Nginx (not installed in store)
	if list[0].ID != services.IDNginx {
		t.Errorf("list[0].ID = %q, want %q", list[0].ID, services.IDNginx)
	}
	if list[0].State != services.StateNotInstalled {
		t.Errorf("list[0].State = %q, want %q", list[0].State, services.StateNotInstalled)
	}
	if list[0].Active != false {
		t.Errorf("list[0].Active = %v, want false", list[0].Active)
	}
	if list[0].ConfigPath != "/data/devstack/services/nginx/config" {
		t.Errorf("list[0].ConfigPath = %q, want %q", list[0].ConfigPath, "/data/devstack/services/nginx/config")
	}

	// Second: MySQL (installed & active)
	if list[1].ID != services.IDMySQL {
		t.Errorf("list[1].ID = %q, want %q", list[1].ID, services.IDMySQL)
	}
	if list[1].State != services.StateInstalled {
		t.Errorf("list[1].State = %q, want %q", list[1].State, services.StateInstalled)
	}
	if list[1].Active != true {
		t.Errorf("list[1].Active = %v, want true", list[1].Active)
	}
}

func TestCoordinatorListEmptyCatalog(t *testing.T) {
	ctx := context.Background()
	coord := services.NewCoordinator(services.DefaultCatalog(), &fakeStore{records: map[services.ID]services.Record{}}, "/data/devstack")
	list, err := coord.List(ctx)
	if err != nil {
		t.Fatalf("List() err = %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("List() on empty catalog returned %d items, want 0", len(list))
	}
}

func TestCoordinatorGet(t *testing.T) {
	ctx := context.Background()
	cat := services.Catalog{
		Services: []services.Service{
			{
				ID:          services.IDNginx,
				DisplayName: "Nginx Web Server",
				Group:       services.GroupWeb,
				Image:       "fixture-nginx@sha256:1111111111111111111111111111111111111111111111111111111111111111",
			},
		},
	}
	st := &fakeStore{records: map[services.ID]services.Record{}}
	coord := services.NewCoordinator(cat, st, "/data/devstack")

	view, err := coord.Get(ctx, services.IDNginx)
	if err != nil {
		t.Fatalf("Get(nginx) err = %v", err)
	}
	if view.ID != services.IDNginx || view.State != services.StateNotInstalled {
		t.Errorf("unexpected view: %+v", view)
	}

	_, err = coord.Get(ctx, services.IDApache)
	if !errors.Is(err, services.ErrNotFound) {
		t.Fatalf("Get(apache) err = %v, want ErrNotFound", err)
	}
}
