package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/build"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/tooling"
)

type fakeToolService struct {
	status     tooling.PHPDefaultStatus
	ini        tooling.GlobalIni
	tools      []helperproto.ToolStatus
	lastValue  string
	lastForce  bool
	lastIni    string
	lastID     string
	setErr     error
	clearCalls int
}

func (f *fakeToolService) PHPDefault(context.Context) (tooling.PHPDefaultStatus, error) {
	return f.status, nil
}

func (f *fakeToolService) SetPHPDefault(_ context.Context, value string, force bool) (tooling.PHPDefaultStatus, error) {
	f.lastValue = value
	f.lastForce = force
	return f.status, f.setErr
}

func (f *fakeToolService) ClearPHPDefault(context.Context) (tooling.PHPDefaultStatus, error) {
	f.clearCalls++
	f.status = tooling.PHPDefaultStatus{}
	return f.status, nil
}

func (f *fakeToolService) GlobalIni(context.Context) (tooling.GlobalIni, error) {
	return f.ini, nil
}

func (f *fakeToolService) SaveGlobalIni(_ context.Context, content string) (tooling.GlobalIni, error) {
	f.lastIni = content
	f.ini.Content = content
	return f.ini, nil
}

func (f *fakeToolService) Tools(context.Context) ([]helperproto.ToolStatus, error) {
	return f.tools, nil
}

func (f *fakeToolService) InstallTool(_ context.Context, id string, force bool) (helperproto.ToolStatus, error) {
	f.lastID = id
	f.lastForce = force
	for _, tool := range f.tools {
		if tool.ID == id {
			tool.Installed = true
			return tool, nil
		}
	}
	return helperproto.ToolStatus{ID: id, Installed: true}, nil
}

func (f *fakeToolService) RemoveTool(_ context.Context, id string) (helperproto.ToolStatus, error) {
	f.lastID = id
	return helperproto.ToolStatus{ID: id}, nil
}

type fakeBuildService struct {
	result build.Result
	logs   build.Logs
	err    error
	site   model.Site
	tail   int
}

func (f *fakeBuildService) Build(_ context.Context, site model.Site) (build.Result, error) {
	f.site = site
	return f.result, f.err
}

func (f *fakeBuildService) Logs(_ context.Context, site model.Site, tail int) (build.Logs, error) {
	f.site = site
	f.tail = tail
	return f.logs, f.err
}

func TestPHPDefaultHandlers(t *testing.T) {
	toolSvc := &fakeToolService{
		status: tooling.PHPDefaultStatus{Configured: "8.4", GlobalIni: "/home/config/cli/php.ini", ShimInstalled: true},
		ini:    tooling.GlobalIni{Path: "/home/config/cli/php.ini", Content: "[PHP]\n", BuildID: "php@8.4.12-r1"},
	}
	fx := newFixture(t, Config{Tooling: toolSvc})

	rec := fx.request(t, http.MethodGet, "/api/v1/php/default", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET default code = %d body=%s", rec.Code, rec.Body.String())
	}
	var status tooling.PHPDefaultStatus
	decode(t, rec, &status)
	if status.Configured != "8.4" || !status.ShimInstalled {
		t.Fatalf("status = %+v", status)
	}

	rec = fx.request(t, http.MethodPut, "/api/v1/php/default", strings.NewReader(`{"value":"8.3","force":true}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT default code = %d body=%s", rec.Code, rec.Body.String())
	}
	if toolSvc.lastValue != "8.3" || !toolSvc.lastForce {
		t.Fatalf("set call = %q force=%v", toolSvc.lastValue, toolSvc.lastForce)
	}

	rec = fx.request(t, http.MethodPut, "/api/v1/php/default", strings.NewReader(`{"clear":true}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("clear code = %d body=%s", rec.Code, rec.Body.String())
	}
	if toolSvc.clearCalls != 1 {
		t.Fatalf("clear calls = %d", toolSvc.clearCalls)
	}

	rec = fx.request(t, http.MethodGet, "/api/v1/php/default/ini", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET ini code = %d body=%s", rec.Code, rec.Body.String())
	}
	var ini tooling.GlobalIni
	decode(t, rec, &ini)
	if ini.BuildID != "php@8.4.12-r1" {
		t.Fatalf("ini = %+v", ini)
	}

	rec = fx.request(t, http.MethodPut, "/api/v1/php/default/ini", strings.NewReader(`{"content":"[PHP]\nmemory_limit = 1G\n"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT ini code = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(toolSvc.lastIni, "memory_limit") {
		t.Fatalf("last ini = %q", toolSvc.lastIni)
	}
}

func TestToolHandlers(t *testing.T) {
	toolSvc := &fakeToolService{tools: []helperproto.ToolStatus{
		{ID: helperproto.ToolComposer, Managed: true, WrapperPath: "/usr/local/bin/composer", Version: "2.10.3"},
	}}
	fx := newFixture(t, Config{Tooling: toolSvc})

	rec := fx.request(t, http.MethodGet, "/api/v1/tools", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /tools code = %d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Tools []helperproto.ToolStatus `json:"tools"`
	}
	decode(t, rec, &response)
	if len(response.Tools) != 1 || response.Tools[0].ID != helperproto.ToolComposer {
		t.Fatalf("tools = %+v", response)
	}

	rec = fx.request(t, http.MethodPost, "/api/v1/tools/composer/install", strings.NewReader(`{"force":true}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("install code = %d body=%s", rec.Code, rec.Body.String())
	}
	if toolSvc.lastID != helperproto.ToolComposer || !toolSvc.lastForce {
		t.Fatalf("install call = %q force=%v", toolSvc.lastID, toolSvc.lastForce)
	}

	rec = fx.request(t, http.MethodPost, "/api/v1/tools/composer/remove", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove code = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestBuildHandlers(t *testing.T) {
	buildSvc := &fakeBuildService{
		result: build.Result{
			Site:       "app",
			Success:    true,
			DurationMS: 42,
			LogPath:    "/home/logs/builds/app/build.log",
			OutputPath: "/home/projects/app/dist",
		},
		logs: build.Logs{Site: "app", Path: "/home/logs/builds/app/build.log", Content: "line\n"},
	}
	fx := newFixture(t, Config{Build: buildSvc})
	site := model.Site{ID: "site-1", Name: "app", Kind: model.SiteStatic, HostPath: "/home/projects/app"}
	fx.sites.mu.Lock()
	fx.sites.sites[site.Name] = site
	fx.sites.mu.Unlock()
	if err := fx.store.CreateSite(context.Background(), site); err != nil {
		t.Fatal(err)
	}

	rec := fx.request(t, http.MethodPost, "/api/v1/sites/app/build", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST build code = %d body=%s", rec.Code, rec.Body.String())
	}
	var result build.Result
	decode(t, rec, &result)
	if !result.Success || result.DurationMS != 42 || buildSvc.site.Name != "app" {
		t.Fatalf("result = %+v site=%+v", result, buildSvc.site)
	}

	rec = fx.request(t, http.MethodGet, "/api/v1/sites/app/build/logs?tail=5", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET logs code = %d body=%s", rec.Code, rec.Body.String())
	}
	var logs build.Logs
	decode(t, rec, &logs)
	if logs.Content != "line\n" || buildSvc.tail != 5 {
		t.Fatalf("logs = %+v tail=%d", logs, buildSvc.tail)
	}

	rec = fx.request(t, http.MethodGet, "/api/v1/sites/app/build/logs?tail=bad", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad tail code = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestBuildHandlerFailureResult(t *testing.T) {
	buildSvc := &fakeBuildService{result: build.Result{
		Site: "app", Success: false, Error: "npm failed", LogPath: "/home/logs/builds/app/build.log",
	}}
	fx := newFixture(t, Config{Build: buildSvc})
	site := model.Site{ID: "site-1", Name: "app", Kind: model.SiteStatic, HostPath: "/home/projects/app"}
	fx.sites.mu.Lock()
	fx.sites.sites[site.Name] = site
	fx.sites.mu.Unlock()
	if err := fx.store.CreateSite(context.Background(), site); err != nil {
		t.Fatal(err)
	}
	rec := fx.request(t, http.MethodPost, "/api/v1/sites/app/build", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
	}
	var result build.Result
	decode(t, rec, &result)
	if result.Success || result.Error == "" {
		t.Fatalf("result = %+v", result)
	}
}

func TestBuildRequestWithUnknownSite(t *testing.T) {
	fx := newFixture(t, Config{Build: &fakeBuildService{}})
	rec := fx.request(t, http.MethodPost, "/api/v1/sites/missing/build", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload apiError
	_ = json.Unmarshal(rec.Body.Bytes(), &payload)
	if payload.Code != codeNotFound {
		t.Fatalf("code = %q", payload.Code)
	}
}

func TestBuildSetupErrorsAre4xx(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "no profile", err: fmt.Errorf("%w: site %q has no build profile", build.ErrInvalidSetup, "app")},
		{name: "non-static", err: fmt.Errorf("%w: site %q is not a static site", build.ErrInvalidSetup, "app")},
		{name: "missing project", err: fmt.Errorf("%w: project directory is not available", build.ErrInvalidSetup)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buildSvc := &fakeBuildService{err: tc.err}
			fx := newFixture(t, Config{Build: buildSvc})
			site := model.Site{ID: "site-1", Name: "app", Kind: model.SiteStatic, HostPath: "/home/projects/app"}
			fx.sites.mu.Lock()
			fx.sites.sites[site.Name] = site
			fx.sites.mu.Unlock()
			rec := fx.request(t, http.MethodPost, "/api/v1/sites/app/build", nil)
			if rec.Code < 400 || rec.Code >= 500 {
				t.Fatalf("code = %d body=%s, want 4xx", rec.Code, rec.Body.String())
			}
			var payload apiError
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Code != codeInvalidRequest {
				t.Fatalf("code = %q body=%s", payload.Code, rec.Body.String())
			}
		})
	}
}
