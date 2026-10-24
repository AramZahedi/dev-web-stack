package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/backend"
	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/phpfpm"
	"github.com/AramZahedi/dev-web-stack/internal/phpini"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
)

func TestAPIPHPApplyRequiresCSRFAndReturnsConfigError(t *testing.T) {
	h, fake := newTestAPIWithFakes(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("PUT", "http://127.0.0.1/api/v1/sites/blog/php", strings.NewReader(`{"ini":{"memory_limit":"bad"}}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("csrf code = %d", rec.Code)
	}

	fake.sites.phpErr = &phpini.LintError{Line: 7, Output: "syntax error"}
	rec = fake.request(t, http.MethodPut, "/api/v1/sites/blog/php", strings.NewReader(`{"ini":{"memory_limit":"bad"}}`))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"PHP_CONFIG_INVALID"`) || !strings.Contains(rec.Body.String(), `"line":7`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"output":"syntax error"`) {
		t.Fatalf("missing lint output: %s", rec.Body.String())
	}

	fake.sites.phpErr = &phpfpm.ConfigError{Field: "listen port", Message: "out of range"}
	rec = fake.request(t, http.MethodPut, "/api/v1/sites/blog/php", strings.NewReader(`{"ini":{"memory_limit":"bad"}}`))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"PHP_CONFIG_INVALID"`) ||
		!strings.Contains(rec.Body.String(), `"field":"listen port"`) || !strings.Contains(rec.Body.String(), `"message":"out of range"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestAPIRemoveRuntimeInUseReturnsPHPVersionInUse(t *testing.T) {
	_, fake := newTestAPIWithFakes(t)
	fake.sites.checkRemovalErr = &sites.RuntimeInUseError{BuildID: "php@8.4.4-r1", Sites: []string{"blog", "shop"}}

	rec := fake.request(t, http.MethodDelete, "/api/v1/runtimes/php@8.4.4-r1", nil)
	assertError(t, rec, http.StatusConflict, "PHP_VERSION_IN_USE")
	if !strings.Contains(rec.Body.String(), `"blog"`) || !strings.Contains(rec.Body.String(), `"shop"`) {
		t.Fatalf("missing site names: %s", rec.Body.String())
	}
	fake.runtimes.mu.Lock()
	removed := append([]string(nil), fake.runtimes.removed...)
	fake.runtimes.mu.Unlock()
	if len(removed) != 0 {
		t.Fatalf("runtime was removed despite in-use error: %v", removed)
	}
}

func TestAPIPHPExecReturnsOutput(t *testing.T) {
	_, fake := newTestAPIWithFakes(t)
	fake.sites.phpExecResult = backend.Result{ExitCode: 0, Stdout: "PHP 8.4.4\n", Stderr: ""}

	rec := fake.request(t, http.MethodPost, "/api/v1/sites/blog/php/exec", strings.NewReader(`{"argv":["-v"]}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("exec code = %d body=%s", rec.Code, rec.Body.String())
	}
	var result phpExecResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode exec: %v", err)
	}
	if result.ExitCode != 0 || result.Stdout != "PHP 8.4.4\n" {
		t.Fatalf("result = %+v", result)
	}
	if len(fake.sites.phpArgv) != 1 || fake.sites.phpArgv[0] != "-v" {
		t.Fatalf("argv = %+v", fake.sites.phpArgv)
	}
}

func TestAPIPHPInfoDefaultsAndMigrate(t *testing.T) {
	_, fake := newTestAPIWithFakes(t)
	fake.sites.phpInfo = sites.PHPInfo{
		Site:         model.Site{Name: "blog", Kind: model.SitePHP, PHPVersion: "8.4"},
		GeneratedIni: "memory_limit = 128M\n",
		Ini:          map[string]string{"memory_limit": "128M"},
	}
	rec := fake.request(t, http.MethodGet, "/api/v1/sites/blog/php", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("info code = %d body=%s", rec.Code, rec.Body.String())
	}
	var info sites.PHPInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode info: %v", err)
	}
	if info.Site.Name != "blog" || !strings.Contains(info.GeneratedIni, "memory_limit") {
		t.Fatalf("info = %+v", info)
	}

	fake.sites.phpDefaults = model.PHPDefaults{
		Version:    "8.4",
		Ini:        map[string]string{"memory_limit": "256M"},
		Extensions: map[string]bool{"xdebug": false},
	}
	rec = fake.request(t, http.MethodGet, "/api/v1/php/defaults/8.4", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("defaults code = %d body=%s", rec.Code, rec.Body.String())
	}
	var defaults model.PHPDefaults
	if err := json.Unmarshal(rec.Body.Bytes(), &defaults); err != nil {
		t.Fatalf("decode defaults: %v", err)
	}
	if defaults.Version != "8.4" {
		t.Fatalf("defaults = %+v", defaults)
	}

	fake.sites.phpSaveResult = sites.PHPDefaultsResult{Defaults: defaults, Sites: []sites.PHPMigrationResult{}}
	rec = fake.request(t, http.MethodPut, "/api/v1/php/defaults/8.4",
		strings.NewReader(`{"version":"8.5","ini":{"memory_limit":"512M"},"extensions":{"xdebug":true}}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("save defaults code = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(fake.sites.savedDefaults) != 1 {
		t.Fatalf("saved defaults = %+v", fake.sites.savedDefaults)
	}
	saved := fake.sites.savedDefaults[0]
	if saved.Version != "8.4" || saved.Ini["memory_limit"] != "512M" || !saved.Extensions["xdebug"] {
		t.Fatalf("saved defaults = %+v", saved)
	}

	fake.sites.phpMigration = []sites.PHPMigrationResult{{Site: "blog", OK: true}}
	rec = fake.request(t, http.MethodPost, "/api/v1/php/migrate",
		strings.NewReader(`{"from_version":"8.4","to_version":"8.5","to_build":"php@8.5.0-r1"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("migrate code = %d body=%s", rec.Code, rec.Body.String())
	}
	var results []sites.PHPMigrationResult
	if err := json.Unmarshal(rec.Body.Bytes(), &results); err != nil || len(results) != 1 || !results[0].OK {
		t.Fatalf("migrate results = %+v %v", results, err)
	}
	if fake.sites.migrationReq.FromVersion != "8.4" || fake.sites.migrationReq.ToVersion != "8.5" || fake.sites.migrationReq.ToBuild != "php@8.5.0-r1" {
		t.Fatalf("migration request = %+v", fake.sites.migrationReq)
	}
}

func TestAPIPHPExtensionsFiltersAndSorts(t *testing.T) {
	_, fake := newTestAPIWithFakes(t)

	rec := fake.request(t, http.MethodGet, "/api/v1/php/extensions?version=8.4", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("extensions code = %d body=%s", rec.Code, rec.Body.String())
	}
	var extensions []catalog.Extension
	if err := json.Unmarshal(rec.Body.Bytes(), &extensions); err != nil {
		t.Fatalf("decode extensions: %v", err)
	}
	if len(extensions) == 0 {
		t.Fatal("extensions are empty")
	}
	for i := 1; i < len(extensions); i++ {
		if extensions[i-1].Name > extensions[i].Name {
			t.Fatalf("extensions are not sorted at %d: %q > %q", i, extensions[i-1].Name, extensions[i].Name)
		}
	}
	for _, extension := range extensions {
		if extension.Name == "imap" {
			t.Fatal("imap is not available on PHP 8.4")
		}
	}

	rec = fake.request(t, http.MethodGet, "/api/v1/php/extensions?version=8.2", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("8.2 extensions code = %d body=%s", rec.Code, rec.Body.String())
	}
	found := false
	var older []catalog.Extension
	if err := json.Unmarshal(rec.Body.Bytes(), &older); err != nil {
		t.Fatalf("decode 8.2 extensions: %v", err)
	}
	for _, extension := range older {
		if extension.Name == "imap" {
			found = true
		}
	}
	if !found {
		t.Fatal("imap should be available on PHP 8.2")
	}

	for _, target := range []string{"/api/v1/php/extensions?version=9.9", "/api/v1/php/extensions"} {
		rec = fake.request(t, http.MethodGet, target, nil)
		assertError(t, rec, http.StatusBadRequest, "INVALID_REQUEST")
	}
}

func TestAPICreateAndUpdateSiteMapPHPFields(t *testing.T) {
	_, fake := newTestAPIWithFakes(t)

	rec := fake.request(t, http.MethodPost, "/api/v1/sites", strings.NewReader(`{
		"name":"blog","path":"/tmp/blog","kind":"php",
		"php_version":"8.4","php_build":"php@8.4.4-r1",
		"php_extensions":{"xdebug":true},"php_ini":{"memory_limit":"512M"},
		"php_ini_raw":"display_errors = On\n","front_controller":true
	}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create code = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(fake.sites.created) != 1 {
		t.Fatalf("created = %+v", fake.sites.created)
	}
	created := fake.sites.created[0]
	if created.PHPVersion != "8.4" || created.PHPBuild != "php@8.4.4-r1" ||
		!created.PHPExtensions["xdebug"] || created.PHPIni["memory_limit"] != "512M" ||
		created.PHPIniRaw != "display_errors = On\n" || !created.FrontController {
		t.Fatalf("create request = %+v", created)
	}

	rec = fake.request(t, http.MethodPatch, "/api/v1/sites/blog", strings.NewReader(`{"front_controller":false,"document_root":"public"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("update code = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(fake.sites.updates) != 1 {
		t.Fatalf("updates = %+v", fake.sites.updates)
	}
	update := fake.sites.updates[0]
	if update.FrontController == nil || *update.FrontController {
		t.Fatalf("front_controller = %#v", update.FrontController)
	}
	if update.DocumentRoot != "public" {
		t.Fatalf("document root = %q", update.DocumentRoot)
	}
}

func TestClassifyPHPRuntimeAndConfigErrors(t *testing.T) {
	status, code, _, details := classifyError(&sites.RuntimeInUseError{BuildID: "php@8.4.4-r1", Sites: []string{"blog"}})
	if status != http.StatusConflict || code != "PHP_VERSION_IN_USE" {
		t.Fatalf("in-use = %d %s", status, code)
	}
	if names, ok := details.(map[string]any)["sites"].([]string); !ok || len(names) != 1 || names[0] != "blog" {
		t.Fatalf("in-use details = %#v", details)
	}

	status, code, _, details = classifyError(&phpini.ValidationError{Line: 3, Message: "bad directive"})
	if status != http.StatusUnprocessableEntity || code != "PHP_CONFIG_INVALID" {
		t.Fatalf("validation = %d %s", status, code)
	}
	fields := details.(map[string]any)
	if fields["line"] != 3 || fields["message"] != "bad directive" {
		t.Fatalf("validation details = %#v", fields)
	}

	status, code, _, details = classifyError(sites.ErrRuntimeMissing)
	if status != http.StatusConflict || code != "RUNTIME_MISSING" || details != nil {
		t.Fatalf("missing = %d %s %#v", status, code, details)
	}
}

func TestAPIPHPInvalidExtensionsReturnConfigInvalid(t *testing.T) {
	type errorBody struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details struct {
			Field   string `json:"field"`
			Message string `json:"message"`
		} `json:"details"`
	}
	tests := []struct {
		name    string
		err     error
		wantMsg string
	}{
		{
			name:    "unknown",
			err:     fmt.Errorf("unknown php extension %q: %w", "xdebugd", catalog.ErrInvalidExtension),
			wantMsg: "xdebugd",
		},
		{
			name:    "unavailable",
			err:     fmt.Errorf("extension %q is not available on PHP 8.4: %w", "imap", catalog.ErrInvalidExtension),
			wantMsg: "not available",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, fake := newTestAPIWithFakes(t)
			fake.sites.phpErr = test.err

			rec := fake.request(t, http.MethodPut, "/api/v1/sites/blog/php", strings.NewReader(`{"extensions":{"xdebugd":true}}`))
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("apply status = %d body=%s", rec.Code, rec.Body.String())
			}
			var apply errorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &apply); err != nil {
				t.Fatalf("decode apply error: %v", err)
			}
			if apply.Code != "PHP_CONFIG_INVALID" || apply.Details.Field != "extensions" ||
				!strings.Contains(apply.Details.Message, test.wantMsg) {
				t.Fatalf("apply error = %+v body=%s", apply, rec.Body.String())
			}

			rec = fake.request(t, http.MethodPut, "/api/v1/php/defaults/8.4",
				strings.NewReader(`{"extensions":{"xdebugd":true}}`))
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("defaults status = %d body=%s", rec.Code, rec.Body.String())
			}
			var defaults errorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &defaults); err != nil {
				t.Fatalf("decode defaults error: %v", err)
			}
			if defaults.Code != "PHP_CONFIG_INVALID" || defaults.Details.Field != "extensions" ||
				!strings.Contains(defaults.Details.Message, test.wantMsg) {
				t.Fatalf("defaults error = %+v body=%s", defaults, rec.Body.String())
			}
		})
	}
}

func TestAPIPHPExecArgumentReturnsInvalidRequest(t *testing.T) {
	for _, argument := range []string{"-n", "--php-ini=/tmp/evil.ini"} {
		t.Run(argument, func(t *testing.T) {
			_, fake := newTestAPIWithFakes(t)
			fake.sites.phpExecErr = &sites.PHPExecArgumentError{Argument: argument}

			rec := fake.request(t, http.MethodPost, "/api/v1/sites/blog/php/exec",
				strings.NewReader(`{"argv":["`+argument+`"]}`))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
			var body struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				Details struct {
					Argument string `json:"argument"`
				} `json:"details"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error: %v", err)
			}
			if body.Code != "INVALID_REQUEST" || body.Details.Argument != argument {
				t.Fatalf("error = %+v body=%s", body, rec.Body.String())
			}
			if !strings.Contains(body.Message, "php.ini") {
				t.Fatalf("message = %q, want php.ini guidance", body.Message)
			}
		})
	}
}
