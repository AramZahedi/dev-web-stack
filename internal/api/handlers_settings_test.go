package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/config"
)

func settingsTestHome(t *testing.T) (string, *apiFixture) {
	t.Helper()
	userHome := t.TempDir()
	home := filepath.Join(userHome, ".devstack")
	if _, err := config.Load(home); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	fx := newFixture(t, Config{
		UserHome:  userHome,
		Listeners: ListenerPorts{HTTPPort: 8080, HTTPSPort: 8443, UIPort: 7070},
	})
	return home, fx
}

func settingsFreePort(t *testing.T, used map[int]bool) int {
	t.Helper()
	for attempt := 0; attempt < 100; attempt++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("reserve port: %v", err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		_ = ln.Close()
		if port >= 41000 && port <= 43999 {
			continue
		}
		if used[port] {
			continue
		}
		used[port] = true
		return port
	}
	t.Fatal("could not allocate a distinct test port")
	return 0
}

func TestPatchSettingsPersistsListenerPortsAndReportsRestart(t *testing.T) {
	home, fx := settingsTestHome(t)
	used := map[int]bool{}
	httpPort := settingsFreePort(t, used)
	httpsPort := settingsFreePort(t, used)
	uiPort := settingsFreePort(t, used)

	body := fmt.Sprintf(`{"http_port":%d,"https_port":%d,"ui_port":%d}`, httpPort, httpsPort, uiPort)
	rec := fx.request(t, http.MethodPatch, "/api/v1/settings", strings.NewReader(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /settings = %d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		HTTPPort        int      `json:"http_port"`
		HTTPSPort       int      `json:"https_port"`
		UIPort          int      `json:"ui_port"`
		RestartRequired bool     `json:"restart_required"`
		Warnings        []string `json:"warnings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.HTTPPort != httpPort || response.HTTPSPort != httpsPort || response.UIPort != uiPort {
		t.Fatalf("response ports = %+v", response)
	}
	if !response.RestartRequired {
		t.Fatal("restart_required = false, want true for listener changes")
	}
	if len(response.Warnings) == 0 {
		t.Fatal("warnings are empty, want restart/forwarder guidance")
	}

	loaded, err := config.Load(home)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if loaded.HTTPPort != httpPort || loaded.HTTPSPort != httpsPort || loaded.UIPort != uiPort {
		t.Fatalf("persisted ports = %d/%d/%d, want %d/%d/%d",
			loaded.HTTPPort, loaded.HTTPSPort, loaded.UIPort, httpPort, httpsPort, uiPort)
	}
	rec = fx.request(t, http.MethodGet, "/api/v1/settings", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /settings after patch = %d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if !response.RestartRequired {
		t.Fatal("GET /settings restart_required = false after a port change")
	}

	for key, want := range map[string]string{
		"http_port":  fmt.Sprint(httpPort),
		"https_port": fmt.Sprint(httpsPort),
		"ui_port":    fmt.Sprint(uiPort),
	} {
		got, err := fx.store.GetSetting(context.Background(), key)
		if err != nil {
			t.Fatalf("store %s: %v", key, err)
		}
		if got != want {
			t.Fatalf("store %s = %q, want %q", key, got, want)
		}
	}

	rec = fx.request(t, http.MethodPatch, "/api/v1/settings", strings.NewReader(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("second PATCH /settings = %d body=%s", rec.Code, rec.Body.String())
	}
	response = struct {
		HTTPPort        int      `json:"http_port"`
		HTTPSPort       int      `json:"https_port"`
		UIPort          int      `json:"ui_port"`
		RestartRequired bool     `json:"restart_required"`
		Warnings        []string `json:"warnings"`
	}{}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if !response.RestartRequired {
		t.Fatal("restart_required = false while the running daemon still owns the old listeners")
	}
}

func TestPatchSettingsPreservesOmittedPendingPorts(t *testing.T) {
	home, fx := settingsTestHome(t)
	used := map[int]bool{}
	firstHTTP := settingsFreePort(t, used)
	firstHTTPS := settingsFreePort(t, used)
	firstUI := settingsFreePort(t, used)
	body := fmt.Sprintf(`{"http_port":%d,"https_port":%d,"ui_port":%d}`, firstHTTP, firstHTTPS, firstUI)
	rec := fx.request(t, http.MethodPatch, "/api/v1/settings", strings.NewReader(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("first PATCH = %d body=%s", rec.Code, rec.Body.String())
	}

	nextHTTP := settingsFreePort(t, used)
	rec = fx.request(t, http.MethodPatch, "/api/v1/settings",
		strings.NewReader(fmt.Sprintf(`{"http_port":%d}`, nextHTTP)))
	if rec.Code != http.StatusOK {
		t.Fatalf("second PATCH = %d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		HTTPPort        int  `json:"http_port"`
		HTTPSPort       int  `json:"https_port"`
		UIPort          int  `json:"ui_port"`
		RestartRequired bool `json:"restart_required"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.HTTPPort != nextHTTP || response.HTTPSPort != firstHTTPS || response.UIPort != firstUI {
		t.Fatalf("second patch reverted omitted pending ports: %+v", response)
	}
	if !response.RestartRequired {
		t.Fatal("restart_required = false while saved ports still differ from running listeners")
	}
	loaded, err := config.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.HTTPPort != nextHTTP || loaded.HTTPSPort != firstHTTPS || loaded.UIPort != firstUI {
		t.Fatalf("config ports = %d/%d/%d", loaded.HTTPPort, loaded.HTTPSPort, loaded.UIPort)
	}
}

func TestPatchSettingsValidatesPorts(t *testing.T) {
	t.Run("duplicate", func(t *testing.T) {
		_, fx := settingsTestHome(t)
		rec := fx.request(t, http.MethodPatch, "/api/v1/settings", strings.NewReader(`{"http_port":9090,"https_port":9090}`))
		assertError(t, rec, http.StatusBadRequest, codeInvalidRequest)
	})
	t.Run("out of range", func(t *testing.T) {
		_, fx := settingsTestHome(t)
		rec := fx.request(t, http.MethodPatch, "/api/v1/settings", strings.NewReader(`{"ui_port":70000}`))
		assertError(t, rec, http.StatusBadRequest, codeInvalidRequest)
	})
	t.Run("reserved backend range", func(t *testing.T) {
		_, fx := settingsTestHome(t)
		rec := fx.request(t, http.MethodPatch, "/api/v1/settings", strings.NewReader(`{"http_port":42000}`))
		assertError(t, rec, http.StatusBadRequest, codeInvalidRequest)
	})
	t.Run("port already in use", func(t *testing.T) {
		_, fx := settingsTestHome(t)
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = ln.Close() }()
		port := ln.Addr().(*net.TCPAddr).Port
		if port >= 41000 && port <= 43999 {
			t.Skip("ephemeral port landed in the reserved backend range")
		}
		rec := fx.request(t, http.MethodPatch, "/api/v1/settings",
			strings.NewReader(fmt.Sprintf(`{"http_port":%d}`, port)))
		assertError(t, rec, http.StatusConflict, codePortInUse)
	})
	t.Run("csrf", func(t *testing.T) {
		_, fx := settingsTestHome(t)
		req := httptest.NewRequest(http.MethodPatch, "http://127.0.0.1/api/v1/settings",
			strings.NewReader(`{"http_port":9090}`))
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: fx.session})
		rec := httptest.NewRecorder()
		fx.raw.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("missing CSRF code = %d, want 403", rec.Code)
		}
	})
}
