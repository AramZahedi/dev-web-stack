package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

func TestVhostStatusAndPreviewReadWithoutSession(t *testing.T) {
	_, fx := newTestAPIWithFakes(t)
	fx.vhosts.status = helperproto.VhostStatusResponse{
		Mode:           "nginx",
		Server:         "nginx",
		ServerVersion:  "1.26.0",
		IncludePresent: true,
		ConfigOK:       true,
	}
	fx.vhosts.preview = "server { server_name blog.test; }\n"

	rec := fx.request(t, http.MethodGet, "/api/v1/vhosts/status", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d body=%s", rec.Code, rec.Body.String())
	}
	var status helperproto.VhostStatusResponse
	decode(t, rec, &status)
	if status.Mode != "nginx" || !status.ConfigOK {
		t.Fatalf("status = %+v", status)
	}

	rec = fx.request(t, http.MethodGet, "/api/v1/vhosts/preview?site=blog&server=nginx", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview code = %d body=%s", rec.Code, rec.Body.String())
	}
	var preview struct {
		Server  string `json:"server"`
		Site    string `json:"site"`
		Content string `json:"content"`
	}
	decode(t, rec, &preview)
	if preview.Server != "nginx" || preview.Site != "blog" || !strings.Contains(preview.Content, "blog.test") {
		t.Fatalf("preview = %+v", preview)
	}
}

func TestVhostModeAndSyncRequireCSRFAndReturnRefreshedStatus(t *testing.T) {
	_, fx := newTestAPIWithFakes(t)

	rec := httptest.NewRecorder()
	fx.raw.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/v1/vhosts/mode", strings.NewReader(`{"mode":"nginx"}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("mode without CSRF = %d, want 403", rec.Code)
	}
	rec = httptest.NewRecorder()
	fx.raw.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/v1/vhosts/sync", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("sync without CSRF = %d, want 403", rec.Code)
	}

	rec = fx.request(t, http.MethodPost, "/api/v1/vhosts/mode", strings.NewReader(`{"mode":"nginx"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("mode code = %d body=%s", rec.Code, rec.Body.String())
	}
	var status helperproto.VhostStatusResponse
	decode(t, rec, &status)
	if status.Mode != "nginx" {
		t.Fatalf("mode status = %+v", status)
	}
	if len(fx.vhosts.modes) != 1 || fx.vhosts.modes[0] != "nginx" {
		t.Fatalf("mode calls = %v", fx.vhosts.modes)
	}

	rec = fx.request(t, http.MethodPost, "/api/v1/vhosts/sync", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync code = %d body=%s", rec.Code, rec.Body.String())
	}
	if fx.vhosts.syncs != 1 {
		t.Fatalf("sync calls = %d", fx.vhosts.syncs)
	}
	if statuses := fx.vhosts.statuses; statuses < 2 {
		t.Fatalf("refreshed status calls = %d", statuses)
	}
}

func TestVhostModeRejectsUnknownMode(t *testing.T) {
	_, fx := newTestAPIWithFakes(t)
	rec := fx.request(t, http.MethodPost, "/api/v1/vhosts/mode", strings.NewReader(`{"mode":"caddy"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestVhostServiceNilReturns503(t *testing.T) {
	_, fx := newTestAPIWithFakes(t)
	fx.server.vhosts = nil
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/v1/vhosts/status", nil),
		httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/v1/vhosts/preview?site=blog&server=nginx", nil),
	} {
		rec := httptest.NewRecorder()
		fx.raw.ServeHTTP(rec, request)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s = %d, want 503", request.Method, request.URL.Path, rec.Code)
		}
	}
}

func TestVhostErrorMapping(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"unavailable", helperproto.ErrServingModeUnavailable, http.StatusConflict, "SERVING_MODE_UNAVAILABLE"},
		{"busy", helperproto.ErrVhostSyncBusy, http.StatusConflict, "VHOST_SYNC_BUSY"},
		{"include", helperproto.ErrVhostIncludeMissing, http.StatusConflict, "VHOST_INCLUDE_MISSING"},
		{"config", helperproto.ErrVhostConfigInvalid, http.StatusUnprocessableEntity, "VHOST_CONFIG_INVALID"},
		{"reload", helperproto.ErrServerReloadFailed, http.StatusBadGateway, "SERVER_RELOAD_FAILED"},
		{"sync", helperproto.ErrVhostSyncFailed, http.StatusInternalServerError, "VHOST_SYNC_FAILED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, fx := newTestAPIWithFakes(t)
			fx.vhosts.syncErr = tt.err
			rec := fx.request(t, http.MethodPost, "/api/v1/vhosts/sync", nil)
			assertError(t, rec, tt.status, tt.code)
		})
	}
}

func TestVhostPreviewRequiresSiteAndServer(t *testing.T) {
	_, fx := newTestAPIWithFakes(t)
	fx.vhosts.previewErr = errors.New("sites: unsupported preview server \"caddy\"")
	rec := fx.request(t, http.MethodGet, "/api/v1/vhosts/preview?site=blog&server=caddy", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
	}
	rec = fx.request(t, http.MethodGet, "/api/v1/vhosts/preview", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing query code = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSettingsReportsServingMode(t *testing.T) {
	_, fx := newTestAPIWithFakes(t)
	if err := fx.store.SetSetting(t.Context(), "serving_mode", "apache"); err != nil {
		t.Fatal(err)
	}
	rec := fx.request(t, http.MethodGet, "/api/v1/settings", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
	}
	var settings struct {
		ServingMode string `json:"serving_mode"`
	}
	decode(t, rec, &settings)
	if settings.ServingMode != "apache" {
		t.Fatalf("serving_mode = %q", settings.ServingMode)
	}
}
