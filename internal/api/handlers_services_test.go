package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/servicecontrol"
	"github.com/AramZahedi/dev-web-stack/internal/services"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

type fakeServiceCoordinator struct {
	views []services.View
	err   error

	actionErr      error
	logsErr        error
	credentialsErr error

	calls           []string
	actionID        services.ID
	deleteData      bool
	tail            int
	logs            string
	credentials     servicecontrol.Credentials
	uninstallResult servicecontrol.UninstallResult
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

func (f *fakeServiceCoordinator) record(action string, id services.ID) {
	f.calls = append(f.calls, action+":"+string(id))
	f.actionID = id
}

func (f *fakeServiceCoordinator) Install(_ context.Context, id services.ID) error {
	f.record("install", id)
	return f.actionErr
}

func (f *fakeServiceCoordinator) Activate(_ context.Context, id services.ID) error {
	f.record("activate", id)
	return f.actionErr
}

func (f *fakeServiceCoordinator) Deactivate(_ context.Context, id services.ID) error {
	f.record("deactivate", id)
	return f.actionErr
}

func (f *fakeServiceCoordinator) Restart(_ context.Context, id services.ID) error {
	f.record("restart", id)
	return f.actionErr
}

func (f *fakeServiceCoordinator) Uninstall(_ context.Context, id services.ID, deleteData bool) (servicecontrol.UninstallResult, error) {
	f.record("uninstall", id)
	f.deleteData = deleteData
	return f.uninstallResult, f.actionErr
}

func (f *fakeServiceCoordinator) Logs(_ context.Context, id services.ID, tail int) (string, error) {
	f.record("logs", id)
	f.tail = tail
	return f.logs, f.logsErr
}

func (f *fakeServiceCoordinator) Credentials(_ context.Context, id services.ID) (servicecontrol.Credentials, error) {
	f.record("credentials", id)
	return f.credentials, f.credentialsErr
}

func (f *fakeServiceCoordinator) RotateCredentials(_ context.Context, id services.ID) (servicecontrol.Credentials, error) {
	f.record("rotate_credentials", id)
	return f.credentials, f.credentialsErr
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
				Installed:   true,
				Health:      "healthy",
				ContainerID: "container-nginx",
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
	if resp.Services[0].Health != "healthy" || resp.Services[0].ContainerID != "container-nginx" || !resp.Services[0].Installed {
		t.Fatalf("service view did not include runtime fields: %+v", resp.Services[0])
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

func TestServiceActionRoutes(t *testing.T) {
	tests := []struct {
		action string
		path   string
	}{
		{"install", "/api/v1/services/nginx/install"},
		{"activate", "/api/v1/services/nginx/activate"},
		{"deactivate", "/api/v1/services/nginx/deactivate"},
		{"restart", "/api/v1/services/nginx/restart"},
	}
	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			fakeCoord := &fakeServiceCoordinator{}
			fx := newFixture(t, Config{Services: fakeCoord})

			rec := fx.request(t, http.MethodPost, tt.path, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("POST %s returned %d, body = %s", tt.path, rec.Code, rec.Body.String())
			}
			var resp struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal response: %v", err)
			}
			if resp.Status != "ok" {
				t.Fatalf("status = %q, want ok", resp.Status)
			}
			if got := strings.Join(fakeCoord.calls, ","); got != tt.action+":nginx" {
				t.Fatalf("calls = %q, want %s:nginx", got, tt.action)
			}
		})
	}
}

func TestServiceActionsRequireCSRF(t *testing.T) {
	fakeCoord := &fakeServiceCoordinator{}
	fx := newFixture(t, Config{Services: fakeCoord})

	request := func(method, path, body string) *httptest.ResponseRecorder {
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, "http://127.0.0.1"+path, reader)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: fx.session})
		rec := httptest.NewRecorder()
		fx.raw.ServeHTTP(rec, req)
		return rec
	}
	for _, path := range []string{
		"/api/v1/services/nginx/install",
		"/api/v1/services/nginx/activate",
		"/api/v1/services/nginx/deactivate",
		"/api/v1/services/nginx/restart",
	} {
		rec := request(http.MethodPost, path, "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("POST %s without CSRF returned %d, want 403", path, rec.Code)
		}
	}
	rec := request(http.MethodDelete, "/api/v1/services/nginx", `{"delete_data":false}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("DELETE without CSRF returned %d, want 403", rec.Code)
	}
	if len(fakeCoord.calls) != 0 {
		t.Fatalf("actions ran without CSRF: %v", fakeCoord.calls)
	}
}

func TestServiceActionErrorMapping(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"not found", services.ErrNotFound, http.StatusNotFound, "NOT_FOUND"},
		{"invalid request", fmt.Errorf("%w: bad id", helperproto.ErrInvalidRequest), http.StatusBadRequest, "INVALID_REQUEST"},
		{"service conflict", fmt.Errorf("%w: host port 8080 is already in use", helperproto.ErrServiceConflict), http.StatusConflict, "SERVICE_CONFLICT"},
		{"helper internal", fmt.Errorf("%w: docker daemon failed", helperproto.ErrInternal), http.StatusInternalServerError, "HELPER_ERROR"},
		{"helper not implemented", fmt.Errorf("%w: services", helperproto.ErrNotImplemented), http.StatusServiceUnavailable, "HELPER_UNAVAILABLE"},
		{"helper unavailable", fmt.Errorf("%w: socket down", servicecontrol.ErrHelperUnavailable), http.StatusServiceUnavailable, "HELPER_UNAVAILABLE"},
		{"credentials unsupported", servicecontrol.ErrCredentialsUnsupported, http.StatusBadRequest, "INVALID_REQUEST"},
		{"credentials rotation unavailable", servicecontrol.ErrCredentialsRotationUnavailable, http.StatusNotImplemented, "CREDENTIAL_ROTATION_UNAVAILABLE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeCoord := &fakeServiceCoordinator{actionErr: tt.err}
			fx := newFixture(t, Config{Services: fakeCoord})
			rec := fx.request(t, http.MethodPost, "/api/v1/services/nginx/install", nil)
			assertError(t, rec, tt.status, tt.code)
		})
	}
}

func TestServiceActionSuccessResponse(t *testing.T) {
	fakeCoord := &fakeServiceCoordinator{}
	fx := newFixture(t, Config{Services: fakeCoord})

	rec := fx.request(t, http.MethodPost, "/api/v1/services/nginx/install", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != `{"status":"ok"}` {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestUninstallService(t *testing.T) {
	t.Run("defaults to false and returns backup path", func(t *testing.T) {
		fakeCoord := &fakeServiceCoordinator{
			uninstallResult: servicecontrol.UninstallResult{BackupPath: "/data/backups/mysql.tar.gz"},
		}
		fx := newFixture(t, Config{Services: fakeCoord})

		rec := fx.request(t, http.MethodDelete, "/api/v1/services/mysql", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
		}
		if fakeCoord.deleteData {
			t.Fatal("delete_data defaulted to true")
		}
		var resp servicecontrol.UninstallResult
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.BackupPath != "/data/backups/mysql.tar.gz" {
			t.Fatalf("backup path = %q", resp.BackupPath)
		}
		if got := strings.Join(fakeCoord.calls, ","); got != "uninstall:mysql" {
			t.Fatalf("calls = %q", got)
		}
	})

	t.Run("passes delete_data", func(t *testing.T) {
		fakeCoord := &fakeServiceCoordinator{}
		fx := newFixture(t, Config{Services: fakeCoord})

		rec := fx.request(t, http.MethodDelete, "/api/v1/services/mysql", strings.NewReader(`{"delete_data":true}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
		}
		if !fakeCoord.deleteData {
			t.Fatal("delete_data = false, want true")
		}
	})

	t.Run("rejects malformed body", func(t *testing.T) {
		fakeCoord := &fakeServiceCoordinator{}
		fx := newFixture(t, Config{Services: fakeCoord})

		rec := fx.request(t, http.MethodDelete, "/api/v1/services/mysql", strings.NewReader(`{"delete_data":`))
		assertError(t, rec, http.StatusBadRequest, "INVALID_REQUEST")
		if len(fakeCoord.calls) != 0 {
			t.Fatalf("uninstall ran with malformed body: %v", fakeCoord.calls)
		}
	})
}

func TestServiceLogs(t *testing.T) {
	fakeCoord := &fakeServiceCoordinator{logs: "line one\nline two\n"}
	fx := newFixture(t, Config{Services: fakeCoord})

	rec := fx.request(t, http.MethodGet, "/api/v1/services/mysql/logs?tail=25", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
	}
	if fakeCoord.tail != 25 {
		t.Fatalf("tail = %d, want 25", fakeCoord.tail)
	}
	var resp struct {
		Logs string `json:"logs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Logs != fakeCoord.logs {
		t.Fatalf("logs = %q", resp.Logs)
	}

	rec = fx.request(t, http.MethodGet, "/api/v1/services/mysql/logs", nil)
	if rec.Code != http.StatusOK || fakeCoord.tail != 0 {
		t.Fatalf("default tail code = %d tail = %d", rec.Code, fakeCoord.tail)
	}

	for _, query := range []string{"?tail=abc", "?tail=-1"} {
		rec = fx.request(t, http.MethodGet, "/api/v1/services/mysql/logs"+query, nil)
		assertError(t, rec, http.StatusBadRequest, "INVALID_REQUEST")
	}

	rec = fx.request(t, http.MethodGet, "/api/v1/services/mysql/logs?tail=999999", nil)
	if rec.Code != http.StatusOK || fakeCoord.tail != 1000 {
		t.Fatalf("capped tail code = %d tail = %d, want 200/1000", rec.Code, fakeCoord.tail)
	}
}

func TestServiceCredentials(t *testing.T) {
	creds := servicecontrol.Credentials{
		Engine:   "mysql",
		Username: "devstack",
		Password: "secret-password",
		Database: "devstack",
		Host:     "127.0.0.1",
		Port:     3306,
	}
	fakeCoord := &fakeServiceCoordinator{credentials: creds}
	fx := newFixture(t, Config{Services: fakeCoord})

	rec := fx.request(t, http.MethodPost, "/api/v1/services/mysql/credentials/reveal", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reveal code = %d body = %s", rec.Code, rec.Body.String())
	}
	var got servicecontrol.Credentials
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal reveal: %v", err)
	}
	if got != creds {
		t.Fatalf("reveal = %+v, want %+v", got, creds)
	}

	rec = fx.request(t, http.MethodPost, "/api/v1/services/mysql/credentials/rotate", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate code = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := strings.Join(fakeCoord.calls, ","); got != "credentials:mysql,rotate_credentials:mysql" {
		t.Fatalf("calls = %q", got)
	}

	fakeCoord.credentialsErr = servicecontrol.ErrCredentialsUnsupported
	rec = fx.request(t, http.MethodPost, "/api/v1/services/redis/credentials/reveal", nil)
	assertError(t, rec, http.StatusBadRequest, "INVALID_REQUEST")
}

func TestServiceCredentialAuditLogNeverLogsSecrets(t *testing.T) {
	const secret = "audit-secret-password"
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	fakeCoord := &fakeServiceCoordinator{credentials: servicecontrol.Credentials{
		Engine: "mysql", Username: "devstack", Password: secret, Database: "devstack", Host: "127.0.0.1", Port: 3306,
	}}
	fx := newFixture(t, Config{Services: fakeCoord, Logger: logger})

	for _, action := range []string{"reveal", "rotate"} {
		rec := fx.request(t, http.MethodPost, "/api/v1/services/mysql/credentials/"+action, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s code = %d body = %s", action, rec.Code, rec.Body.String())
		}
	}

	fakeCoord.credentialsErr = fmt.Errorf("credential lookup failed for password=%s", secret)
	rec := fx.request(t, http.MethodPost, "/api/v1/services/mysql/credentials/reveal", nil)
	if rec.Code < http.StatusInternalServerError {
		t.Fatalf("failure code = %d, want server error", rec.Code)
	}

	audit := logs.String()
	if strings.Contains(audit, secret) {
		t.Fatalf("audit log leaks credentials: %s", audit)
	}
	for _, want := range []string{
		"service=mysql",
		"action=reveal",
		"action=rotate",
		"success=true",
		"success=false",
	} {
		if !strings.Contains(audit, want) {
			t.Fatalf("audit log missing %q: %s", want, audit)
		}
	}
}

func TestServiceListAndGetRedactCredentialBearingLastError(t *testing.T) {
	const secret = "last-error-secret-password"
	fakeCoord := &fakeServiceCoordinator{
		views: []services.View{{
			ID:          services.IDMySQL,
			DisplayName: "MySQL Database",
			Group:       services.GroupMySQLFamily,
			State:       services.StateError,
			LastError:   `startup failed: password=last-error-secret-password`,
		}},
	}
	fx := newFixture(t, Config{Services: fakeCoord})

	for _, path := range []string{"/api/v1/services", "/api/v1/services/mysql"} {
		rec := fx.request(t, http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d body = %s", path, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("GET %s leaks secret: %s", path, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "[REDACTED]") {
			t.Fatalf("GET %s did not redact last_error: %s", path, rec.Body.String())
		}
	}
}

func TestServiceListAndGetNeverIncludeCredentials(t *testing.T) {
	fakeCoord := &fakeServiceCoordinator{
		views: []services.View{{ID: services.IDMySQL, DisplayName: "MySQL Database", Group: services.GroupMySQLFamily}},
	}
	fx := newFixture(t, Config{Services: fakeCoord})

	for _, path := range []string{"/api/v1/services", "/api/v1/services/mysql"} {
		rec := fx.request(t, http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d body = %s", path, rec.Code, rec.Body.String())
		}
		body := strings.ToLower(rec.Body.String())
		for _, forbidden := range []string{"password", "username", "rotate", "credentials"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("GET %s response leaks %q: %s", path, forbidden, rec.Body.String())
			}
		}
	}
}

func TestSettingsServingProvider(t *testing.T) {
	fx := newFixture(t, Config{})
	rec := fx.request(t, http.MethodGet, "/api/v1/settings", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings code = %d body = %s", rec.Code, rec.Body.String())
	}
	var settings struct {
		ServingProvider string `json:"serving_provider"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &settings); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	if settings.ServingProvider != "native" {
		t.Fatalf("serving_provider = %q, want native", settings.ServingProvider)
	}

	if err := fx.store.SetSetting(context.Background(), "serving_provider", "docker"); err != nil {
		t.Fatal(err)
	}
	rec = fx.request(t, http.MethodGet, "/api/v1/settings", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &settings); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	if settings.ServingProvider != "docker" {
		t.Fatalf("serving_provider = %q, want docker", settings.ServingProvider)
	}
}

func TestServiceListReadOnlyFallback(t *testing.T) {
	fx := newFixture(t, Config{})

	rec := fx.request(t, http.MethodGet, "/api/v1/services", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read-only list returned %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = fx.request(t, http.MethodPost, "/api/v1/services/nginx/install", nil)
	assertError(t, rec, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE")
}

func TestServiceActionUnknownIDIsNotFound(t *testing.T) {
	fakeCoord := &fakeServiceCoordinator{actionErr: services.ErrNotFound}
	fx := newFixture(t, Config{Services: fakeCoord})

	rec := fx.request(t, http.MethodPost, "/api/v1/services/missing/install", nil)
	assertError(t, rec, http.StatusNotFound, "NOT_FOUND")
}
