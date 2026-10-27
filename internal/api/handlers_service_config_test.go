package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/serviceconfig"
	"github.com/AramZahedi/dev-web-stack/internal/servicecontrol"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

type fakeServiceConfig struct {
	listing servicecontrol.ConfigListing
	content servicecontrol.ConfigContent
	diff    servicecontrol.ConfigDiff
	apply   servicecontrol.ConfigApplyResult
	reset   servicecontrol.ConfigApplyResult
	err     error

	gotID         services.ID
	gotPath       string
	gotContent    string
	gotResolution string
	applyCalls    int
	resetCalls    int
}

func (f *fakeServiceConfig) List(_ context.Context, id services.ID) (servicecontrol.ConfigListing, error) {
	f.gotID = id
	if f.err != nil {
		return servicecontrol.ConfigListing{}, f.err
	}
	return f.listing, nil
}

func (f *fakeServiceConfig) Content(_ context.Context, id services.ID, path string) (servicecontrol.ConfigContent, error) {
	f.gotID = id
	f.gotPath = path
	if f.err != nil {
		return servicecontrol.ConfigContent{}, f.err
	}
	return f.content, nil
}

func (f *fakeServiceConfig) Diff(_ context.Context, id services.ID, path, content string) (servicecontrol.ConfigDiff, error) {
	f.gotID = id
	f.gotPath = path
	f.gotContent = content
	if f.err != nil {
		return servicecontrol.ConfigDiff{}, f.err
	}
	return f.diff, nil
}

func (f *fakeServiceConfig) Apply(_ context.Context, id services.ID, req servicecontrol.ConfigApplyRequest) (servicecontrol.ConfigApplyResult, error) {
	f.gotID = id
	f.gotPath = req.Path
	f.gotContent = req.Content
	f.gotResolution = req.Resolution
	f.applyCalls++
	if f.err != nil {
		return servicecontrol.ConfigApplyResult{}, f.err
	}
	return f.apply, nil
}

func (f *fakeServiceConfig) Reset(_ context.Context, id services.ID, path string) (servicecontrol.ConfigApplyResult, error) {
	f.gotID = id
	f.gotPath = path
	f.resetCalls++
	if f.err != nil {
		return servicecontrol.ConfigApplyResult{}, f.err
	}
	return f.reset, nil
}

func newServiceConfigFixture(t *testing.T) (*apiFixture, *fakeServiceConfig) {
	t.Helper()
	fake := &fakeServiceConfig{
		listing: servicecontrol.ConfigListing{
			Service: "nginx",
			Files: []servicecontrol.ConfigStatus{{
				Path:      "nginx.conf",
				Class:     serviceconfig.ClassBase,
				Hash:      strings.Repeat("a", 64),
				Size:      123,
				Generated: false,
				Conflict: servicecontrol.ConfigConflictSummary{
					Active:             false,
					ResolutionRequired: false,
				},
			}},
		},
		content: servicecontrol.ConfigContent{
			ConfigStatus: servicecontrol.ConfigStatus{
				Path:  "nginx.conf",
				Class: serviceconfig.ClassBase,
				Hash:  strings.Repeat("a", 64),
				Size:  9,
			},
			Content: "worker_processes 1;\n",
		},
		diff: servicecontrol.ConfigDiff{
			Path:         "nginx.conf",
			Diff:         "--- a/nginx.conf\n+++ b/nginx.conf\n",
			CurrentHash:  strings.Repeat("a", 64),
			ProposedHash: strings.Repeat("b", 64),
			Conflict: servicecontrol.ConfigConflictSummary{
				Active:             true,
				ResolutionRequired: true,
				AllowedResolutions: []string{"keep", "use", "merge"},
			},
		},
		apply: servicecontrol.ConfigApplyResult{
			Path:    "nginx.conf",
			Action:  "apply",
			Changed: true,
			Live:    true,
			Hash:    strings.Repeat("b", 64),
			Size:    9,
		},
		reset: servicecontrol.ConfigApplyResult{
			Path:         "nginx.conf",
			Action:       "reset",
			Changed:      true,
			Live:         true,
			RestoredFrom: "/tmp/backup",
		},
	}
	fx := newFixture(t, Config{ServiceConfig: fake})
	return fx, fake
}

func TestServiceConfigListAndContent(t *testing.T) {
	fx, fake := newServiceConfigFixture(t)

	rec := fx.request(t, http.MethodGet, "/api/v1/services/nginx/config", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list code = %d body=%s", rec.Code, rec.Body.String())
	}
	cold := httptest.NewRecorder()
	fx.raw.ServeHTTP(cold, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/v1/services/nginx/config", nil))
	if cold.Code != http.StatusOK {
		t.Fatalf("cold list code = %d body=%s", cold.Code, cold.Body.String())
	}
	var listing servicecontrol.ConfigListing
	decode(t, rec, &listing)
	if listing.Service != "nginx" || len(listing.Files) != 1 || listing.Files[0].Path != "nginx.conf" {
		t.Fatalf("listing = %+v", listing)
	}
	if strings.Contains(rec.Body.String(), "worker_processes") {
		t.Fatalf("listing leaked contents: %s", rec.Body.String())
	}

	rec = fx.request(t, http.MethodGet,
		"/api/v1/services/nginx/config/content?path="+url.QueryEscape("conf.d/custom/app.conf"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("content code = %d body=%s", rec.Code, rec.Body.String())
	}
	var content servicecontrol.ConfigContent
	decode(t, rec, &content)
	if content.Content != "worker_processes 1;\n" {
		t.Fatalf("content = %+v", content)
	}
	if fake.gotID != services.IDNginx || fake.gotPath != "conf.d/custom/app.conf" {
		t.Fatalf("fake got id/path = %q/%q", fake.gotID, fake.gotPath)
	}

	coldContent := httptest.NewRequest(http.MethodGet,
		"http://127.0.0.1/api/v1/services/nginx/config/content?path="+url.QueryEscape("conf.d/custom/app.conf"), nil)
	coldRec := httptest.NewRecorder()
	fx.raw.ServeHTTP(coldRec, coldContent)
	if coldRec.Code != http.StatusForbidden {
		t.Fatalf("cold content read = %d, want 403", coldRec.Code)
	}
}

func TestServiceConfigMutationsRequireCSRF(t *testing.T) {
	fx, fake := newServiceConfigFixture(t)

	body := strings.NewReader(`{"path":"nginx.conf","content":"worker_processes 2;\n"}`)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/v1/services/nginx/config/apply", body)
	rec := httptest.NewRecorder()
	fx.authedHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing csrf code = %d body=%s", rec.Code, rec.Body.String())
	}
	if fake.applyCalls != 0 {
		t.Fatal("apply ran without CSRF")
	}

	recorder := fx.request(t, http.MethodPost, "/api/v1/services/nginx/config/apply",
		strings.NewReader(`{"path":"nginx.conf","content":"worker_processes 2;\n","resolution":"use"}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("apply code = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if fake.applyCalls != 1 || fake.gotResolution != "use" || fake.gotContent != "worker_processes 2;\n" {
		t.Fatalf("apply request = %+v", fake)
	}
}

func TestServiceConfigDiffAndReset(t *testing.T) {
	fx, fake := newServiceConfigFixture(t)

	rec := fx.request(t, http.MethodPost, "/api/v1/services/nginx/config/diff",
		strings.NewReader(`{"path":"conf.d/app.conf","content":"server { listen 81; }\n"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("diff code = %d body=%s", rec.Code, rec.Body.String())
	}
	var diff servicecontrol.ConfigDiff
	decode(t, rec, &diff)
	if !diff.Conflict.Active || len(diff.Conflict.AllowedResolutions) != 3 {
		t.Fatalf("diff = %+v", diff)
	}
	if fake.gotPath != "conf.d/app.conf" || fake.gotContent != "server { listen 81; }\n" {
		t.Fatalf("diff request = %q/%q", fake.gotPath, fake.gotContent)
	}

	rec = fx.request(t, http.MethodPost, "/api/v1/services/nginx/config/reset",
		strings.NewReader(`{"path":"nginx.conf"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("reset code = %d body=%s", rec.Code, rec.Body.String())
	}
	if fake.resetCalls != 1 || fake.gotPath != "nginx.conf" {
		t.Fatalf("reset request = %+v", fake)
	}
}

func TestServiceConfigErrorsRedactSecrets(t *testing.T) {
	fx, fake := newServiceConfigFixture(t)
	fake.err = &servicecontrol.ConfigApplyFailure{
		Kind: servicecontrol.ConfigFailureValidate,
		Err:  errors.New("nginx: invalid config password=supersecret"),
	}
	rec := fx.request(t, http.MethodPost, "/api/v1/services/nginx/config/apply",
		strings.NewReader(`{"path":"nginx.conf","content":"x"}`))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "supersecret") {
		t.Fatalf("response leaked a secret: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "REDACTED") {
		t.Fatalf("response was not redacted: %s", rec.Body.String())
	}
}

func TestServiceConfigErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"conflict", &servicecontrol.ConfigConflictError{
			Path: "nginx.conf", Reason: "modified", Diff: "--- a\n+++ b\n",
			Conflict: servicecontrol.ConfigConflictSummary{Active: true, ResolutionRequired: true},
		}, http.StatusConflict, "CONFIG_CONFLICT"},
		{"not installed", servicecontrol.ErrConfigNotInstalled, http.StatusConflict, "SERVICE_CONFLICT"},
		{"path denied", servicecontrol.ErrConfigPathDenied, http.StatusBadRequest, "INVALID_REQUEST"},
		{"path invalid", servicecontrol.ErrConfigPathInvalid, http.StatusBadRequest, "INVALID_REQUEST"},
		{"too large", servicecontrol.ErrConfigTooLarge, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE"},
		{"not found", servicecontrol.ErrConfigNotFound, http.StatusNotFound, "NOT_FOUND"},
		{"reset unavailable", servicecontrol.ErrConfigResetUnavailable, http.StatusConflict, "CONFIG_RESET_UNAVAILABLE"},
		{"validation", &servicecontrol.ConfigApplyFailure{Kind: servicecontrol.ConfigFailureValidate, Err: errors.New("bad config")}, http.StatusUnprocessableEntity, "CONFIG_VALIDATION_FAILED"},
		{"reload", &servicecontrol.ConfigApplyFailure{Kind: servicecontrol.ConfigFailureReload, Err: errors.New("reload failed")}, http.StatusBadGateway, "CONFIG_RELOAD_FAILED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx, fake := newServiceConfigFixture(t)
			fake.err = tc.err
			rec := fx.request(t, http.MethodPost, "/api/v1/services/nginx/config/apply",
				strings.NewReader(`{"path":"nginx.conf","content":"x"}`))
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, tc.status, rec.Body.String())
			}
			var payload apiError
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Code != tc.code {
				t.Fatalf("code = %q, want %q", payload.Code, tc.code)
			}
		})
	}
}

type fakeAgentForAPITest struct {
	listing agentproto.ServiceConfigResponse
	content agentproto.ServiceContentResponse
	writes  []agentproto.ServiceConfigRequest
	resets  []agentproto.ServiceConfigRequest
}

func (f *fakeAgentForAPITest) ServiceConfig(_ context.Context, _ string) (agentproto.ServiceConfigResponse, error) {
	return f.listing, nil
}

func (f *fakeAgentForAPITest) ServiceContent(_ context.Context, _ agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error) {
	return f.content, nil
}

func (f *fakeAgentForAPITest) ServiceApply(_ context.Context, req agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error) {
	f.writes = append(f.writes, req)
	return agentproto.ServiceContentResponse{
		Path:    req.Rel,
		Content: req.Content,
		Hash:    "new-hash",
	}, nil
}

func (f *fakeAgentForAPITest) ServiceReset(_ context.Context, req agentproto.ServiceConfigRequest) error {
	f.resets = append(f.resets, req)
	return nil
}

func TestServiceConfigWithAgentBackend(t *testing.T) {
	root := t.TempDir()
	agent := &fakeAgentForAPITest{
		listing: agentproto.ServiceConfigResponse{
			Files: []agentproto.ServiceConfigFile{
				{Path: "my.cnf", Class: "custom", Hash: "initial-hash"},
			},
		},
		content: agentproto.ServiceContentResponse{
			Path:    "my.cnf",
			Content: "[mysqld]\nmax_connections=100\n",
			Hash:    "initial-hash",
		},
	}

	cfgSvc, err := servicecontrol.NewConfigService(servicecontrol.ConfigDeps{
		Root:    root,
		Catalog: services.DefaultCatalog(),
		Agent:   agent,
	})
	if err != nil {
		t.Fatalf("NewConfigService: %v", err)
	}

	fx := newFixture(t, Config{ServiceConfig: cfgSvc})

	// 1. GET /api/v1/services/mysql/config
	rec := fx.request(t, http.MethodGet, "/api/v1/services/mysql/config", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET config list status = %d, want 200", rec.Code)
	}
	var listing servicecontrol.ConfigListing
	if err := json.NewDecoder(rec.Body).Decode(&listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Files) != 1 || listing.Files[0].Path != "my.cnf" {
		t.Fatalf("unexpected listing: %+v", listing)
	}

	// 2. GET /api/v1/services/mysql/config/content?path=my.cnf
	rec = fx.request(t, http.MethodGet, "/api/v1/services/mysql/config/content?path=my.cnf", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET config content status = %d, want 200", rec.Code)
	}
	var content servicecontrol.ConfigContent
	if err := json.NewDecoder(rec.Body).Decode(&content); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content.Content, "max_connections=100") {
		t.Fatalf("unexpected content: %s", content.Content)
	}

	// 3. POST /api/v1/services/mysql/config/diff
	diffBody := `{"path":"my.cnf","content":"[mysqld]\nmax_connections=200\n"}`
	rec = fx.request(t, http.MethodPost, "/api/v1/services/mysql/config/diff", strings.NewReader(diffBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST diff status = %d, want 200", rec.Code)
	}
	var diff servicecontrol.ConfigDiff
	if err := json.NewDecoder(rec.Body).Decode(&diff); err != nil {
		t.Fatal(err)
	}
	if diff.Path != "my.cnf" || diff.Diff == "" {
		t.Fatalf("unexpected diff response: %+v", diff)
	}

	// 4. POST /api/v1/services/mysql/config/apply
	applyBody := `{"path":"my.cnf","content":"[mysqld]\nmax_connections=200\n"}`
	rec = fx.request(t, http.MethodPost, "/api/v1/services/mysql/config/apply", strings.NewReader(applyBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST apply status = %d, want 200", rec.Code)
	}
	var applyRes servicecontrol.ConfigApplyResult
	if err := json.NewDecoder(rec.Body).Decode(&applyRes); err != nil {
		t.Fatal(err)
	}
	if applyRes.Path != "my.cnf" || !applyRes.Changed {
		t.Fatalf("unexpected apply result: %+v", applyRes)
	}
	if len(agent.writes) != 1 {
		t.Fatalf("expected 1 write to agent, got %d", len(agent.writes))
	}

	// 5. POST /api/v1/services/mysql/config/reset
	resetBody := `{"path":"my.cnf"}`
	rec = fx.request(t, http.MethodPost, "/api/v1/services/mysql/config/reset", strings.NewReader(resetBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST reset status = %d, want 200", rec.Code)
	}
	var resetRes servicecontrol.ConfigApplyResult
	if err := json.NewDecoder(rec.Body).Decode(&resetRes); err != nil {
		t.Fatal(err)
	}
	if resetRes.Path != "my.cnf" || !resetRes.Changed {
		t.Fatalf("unexpected reset result: %+v", resetRes)
	}
	if len(agent.resets) != 1 {
		t.Fatalf("expected 1 reset to agent, got %d", len(agent.resets))
	}
}
