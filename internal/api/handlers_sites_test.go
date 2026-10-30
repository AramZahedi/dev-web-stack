package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestCreateSiteReturnsDatabaseWarnings(t *testing.T) {
	fx := newFixture(t, Config{})
	fx.sites.createWarnings = []string{"database user has no grant on this database"}
	rec := fx.request(t, http.MethodPost, "/api/v1/sites", strings.NewReader(`{"name":"blog","path":"/tmp","kind":"static"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no grant") {
		t.Fatalf("warning missing from response: %s", rec.Body.String())
	}
}

func TestCreateSiteWithContracts(t *testing.T) {
	_, fake := newTestAPIWithFakes(t)

	// Session
	rec := httptest.NewRecorder()
	fake.raw.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1/api/v1/session", nil))
	var session struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	cookies := rec.Result().Cookies()

	body := `{
		"name": "contractapp",
		"path": "/tmp",
		"kind": "php",
		"php_version": "8.5",
		"php_build": "php@8.5.1-r2",
		"front_controller_file": "bootstrap.php",
		"front_controller_mode": "generated",
		"apache_allow_override": "fileinfo",
		"spa_fallback_file": "app.html",
		"build": {
			"node_version": "20.0.0",
			"command": "npm run build",
			"output": "dist",
			"env": {"NODE_ENV": "production"}
		}
	}`
	req := httptest.NewRequest("POST", "http://127.0.0.1/api/v1/sites", strings.NewReader(body))
	req.AddCookie(cookies[0])
	req.Header.Set("X-CSRF-Token", session.CSRFToken)
	rec = httptest.NewRecorder()
	fake.raw.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var created model.Site
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.FrontControllerFile != "bootstrap.php" ||
		created.FrontControllerMode != "generated" ||
		created.ApacheAllowOverride != "fileinfo" ||
		created.SPAFallbackFile != "app.html" ||
		created.Build == nil ||
		created.Build.Command != "npm run build" {
		t.Fatalf("created site contracts mismatch: %+v", created)
	}

	// GET site
	reqGet := httptest.NewRequest("GET", "http://127.0.0.1/api/v1/sites/"+created.ID, nil)
	recGet := httptest.NewRecorder()
	fake.raw.ServeHTTP(recGet, reqGet)
	if recGet.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", recGet.Code, recGet.Body.String())
	}
	var fetched model.Site
	if err := json.Unmarshal(recGet.Body.Bytes(), &fetched); err != nil {
		t.Fatal(err)
	}
	if fetched.FrontControllerFile != "bootstrap.php" ||
		fetched.FrontControllerMode != "generated" ||
		fetched.ApacheAllowOverride != "fileinfo" ||
		fetched.SPAFallbackFile != "app.html" ||
		fetched.Build == nil ||
		fetched.Build.Command != "npm run build" {
		t.Fatalf("fetched site contracts mismatch: %+v", fetched)
	}
}

func TestCreateSiteContractsValidationErrors(t *testing.T) {
	_, fake := newTestAPIWithFakes(t)

	rec := httptest.NewRecorder()
	fake.raw.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1/api/v1/session", nil))
	var session struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	cookies := rec.Result().Cookies()

	invalidPayloads := []string{
		`{"name": "bad1", "path": "/tmp", "kind": "php", "front_controller_file": "../bad.php"}`,
		`{"name": "bad2", "path": "/tmp", "kind": "php", "front_controller_mode": "invalid_mode"}`,
		`{"name": "bad3", "path": "/tmp", "kind": "php", "apache_allow_override": "invalid_override"}`,
		`{"name": "bad4", "path": "/tmp", "kind": "php", "spa_fallback_file": "bad.php"}`,
		`{"name": "bad5", "path": "/tmp", "kind": "php", "build": {"output": "dist"}}`,
		`{"name": "bad6", "path": "/tmp", "kind": "php", "front_controller_file": "null\x00byte.php"}`,
	}

	for _, p := range invalidPayloads {
		req := httptest.NewRequest("POST", "http://127.0.0.1/api/v1/sites", strings.NewReader(p))
		req.AddCookie(cookies[0])
		req.Header.Set("X-CSRF-Token", session.CSRFToken)
		rec = httptest.NewRecorder()
		fake.raw.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("for payload %s: expected 400 Bad Request, got %d, body: %s", p, rec.Code, rec.Body.String())
		}
	}
}

func TestUpdateSiteWithContracts(t *testing.T) {
	_, fake := newTestAPIWithFakes(t)

	rec := httptest.NewRecorder()
	fake.raw.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1/api/v1/session", nil))
	var session struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	cookies := rec.Result().Cookies()

	// Create site
	reqCreate := httptest.NewRequest("POST", "http://127.0.0.1/api/v1/sites",
		strings.NewReader(`{"name":"upcontract","path":"/tmp","kind":"php","php_version":"8.5","php_build":"php@8.5.1-r2"}`))
	reqCreate.AddCookie(cookies[0])
	reqCreate.Header.Set("X-CSRF-Token", session.CSRFToken)
	rec = httptest.NewRecorder()
	fake.raw.ServeHTTP(rec, reqCreate)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", rec.Code, rec.Body.String())
	}
	var created model.Site
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	// Update site
	updateBody := `{
		"front_controller_file": "app.php",
		"front_controller_mode": "htaccess",
		"apache_allow_override": "none",
		"spa_fallback_file": "custom.html",
		"build": {
			"node_version": "22.0.0",
			"command": "npm run build"
		}
	}`
	reqUpdate := httptest.NewRequest("PATCH", "http://127.0.0.1/api/v1/sites/"+created.ID, strings.NewReader(updateBody))
	reqUpdate.AddCookie(cookies[0])
	reqUpdate.Header.Set("X-CSRF-Token", session.CSRFToken)
	recUpdate := httptest.NewRecorder()
	fake.raw.ServeHTTP(recUpdate, reqUpdate)

	if recUpdate.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", recUpdate.Code, recUpdate.Body.String())
	}

	var updated model.Site
	if err := json.Unmarshal(recUpdate.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.FrontControllerFile != "app.php" ||
		updated.FrontControllerMode != "htaccess" ||
		updated.ApacheAllowOverride != "none" ||
		updated.SPAFallbackFile != "custom.html" ||
		updated.Build == nil ||
		updated.Build.NodeVersion != "22.0.0" {
		t.Fatalf("updated site contracts mismatch: %+v", updated)
	}
}
