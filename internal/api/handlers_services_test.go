package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/services"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

type fakeServiceCoordinator struct {
	views []services.View
	err   error
}

func (f *fakeServiceCoordinator) List(_ context.Context) ([]services.View, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.views == nil {
		return []services.View{}, nil
	}
	return f.views, nil
}

func (f *fakeServiceCoordinator) Get(_ context.Context, id services.ID) (services.View, error) {
	if f.err != nil {
		return services.View{}, f.err
	}
	for _, v := range f.views {
		if v.ID == id {
			return v, nil
		}
	}
	return services.View{}, store.ErrNotFound
}

func TestListServices(t *testing.T) {
	fakeCoord := &fakeServiceCoordinator{
		views: []services.View{
			{
				ID:          services.IDNginx,
				DisplayName: "Nginx Web Server",
				Group:       services.GroupWeb,
				State:       services.StateActive,
				Active:      true,
				Image:       "fixture-nginx@sha256:1111111111111111111111111111111111111111111111111111111111111111",
				Ports: []services.Port{
					{Host: 80, Container: 80, Protocol: "http", Loopback: true},
				},
				ConfigPath: "/data/devstack/services/nginx/config",
			},
		},
	}

	fx := newFixture(t, Config{Services: fakeCoord})
	rec := fx.request(t, http.MethodGet, "/api/v1/services", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/services returned code %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Services []services.View `json:"services"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(resp.Services))
	}
	if resp.Services[0].ID != services.IDNginx {
		t.Fatalf("expected service id nginx, got %s", resp.Services[0].ID)
	}
}

func TestListServicesEmpty(t *testing.T) {
	fakeCoord := &fakeServiceCoordinator{views: nil}
	fx := newFixture(t, Config{Services: fakeCoord})
	rec := fx.request(t, http.MethodGet, "/api/v1/services", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/services returned code %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Services []services.View `json:"services"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Services == nil || len(resp.Services) != 0 {
		t.Fatalf("expected empty non-nil services slice, got %+v", resp.Services)
	}
}

func TestGetService(t *testing.T) {
	fakeCoord := &fakeServiceCoordinator{
		views: []services.View{
			{
				ID:          services.IDMySQL,
				DisplayName: "MySQL Database",
				Group:       services.GroupMySQLFamily,
				State:       services.StateInstalled,
				Active:      false,
			},
		},
	}
	fx := newFixture(t, Config{Services: fakeCoord})

	rec := fx.request(t, http.MethodGet, "/api/v1/services/mysql", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/services/mysql returned %d, body = %s", rec.Code, rec.Body.String())
	}

	var view services.View
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("unmarshal view: %v", err)
	}
	if view.ID != services.IDMySQL || view.State != services.StateInstalled {
		t.Fatalf("unexpected view: %+v", view)
	}

	// 404 for unknown
	recNotFound := fx.request(t, http.MethodGet, "/api/v1/services/unknown", nil)
	if recNotFound.Code != http.StatusNotFound {
		t.Fatalf("GET /api/v1/services/unknown returned %d, want 404", recNotFound.Code)
	}
	var errResp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recNotFound.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if errResp.Code != "NOT_FOUND" {
		t.Fatalf("error code = %q, want %q", errResp.Code, "NOT_FOUND")
	}
}
