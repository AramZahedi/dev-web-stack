package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/tooling"
)

// fakeNodeDefaultToolService embeds ToolService for the methods the Node
// default handler never calls and adds the narrow NodeDefault surface.
type fakeNodeDefaultToolService struct {
	ToolService
	status tooling.NodeDefaultStatus
	err    error
	calls  int
}

func (f *fakeNodeDefaultToolService) NodeDefault(context.Context) (tooling.NodeDefaultStatus, error) {
	f.calls++
	return f.status, f.err
}

func TestNodeDefaultAPIStatus(t *testing.T) {
	const shimDir = "/data/devstack/tools/node-default/bin"
	build := model.RuntimeBuild{
		ID:      "node@22.11.0",
		Kind:    "node",
		Version: "22.11.0",
		Backend: model.BackendLocal,
		Path:    "/data/devstack/runtimes/node@22.11.0",
	}
	toolSvc := &fakeNodeDefaultToolService{status: tooling.NodeDefaultStatus{
		Configured:    "22",
		Build:         &build,
		ShimDir:       shimDir,
		ShimInstalled: true,
		Source:        tooling.NodeDefaultSourceSetting,
	}}
	fx := newFixture(t, Config{Tooling: toolSvc})

	rec := httptest.NewRecorder()
	fx.server.handleNodeDefault(rec, httptest.NewRequest(http.MethodGet, "/api/v1/node/default", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q", ct)
	}
	if cache := rec.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("cache-control = %q", cache)
	}

	var status tooling.NodeDefaultStatus
	decode(t, rec, &status)
	if status.Configured != "22" || status.Source != tooling.NodeDefaultSourceSetting {
		t.Fatalf("status = %+v", status)
	}
	if status.Build == nil || status.Build.ID != build.ID || status.Build.Version != build.Version || status.Build.Path != build.Path {
		t.Fatalf("build = %+v", status.Build)
	}
	if status.ShimDir != shimDir || !status.ShimInstalled {
		t.Fatalf("shim dir = %q installed = %v", status.ShimDir, status.ShimInstalled)
	}
	if toolSvc.calls != 1 {
		t.Fatalf("NodeDefault calls = %d, want 1", toolSvc.calls)
	}

	// The status JSON shape is part of the CLI contract: every documented
	// field is present even when it carries a zero value.
	var raw map[string]json.RawMessage
	decode(t, rec, &raw)
	for _, key := range []string{"configured", "build", "shim_dir", "shim_installed", "source"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("status JSON missing %q: %s", key, rec.Body.String())
		}
	}

	// Service failures go through the shared error envelope, exactly like the
	// PHP default handlers.
	toolSvc.err = errors.New("store unavailable")
	rec = httptest.NewRecorder()
	fx.server.handleNodeDefault(rec, httptest.NewRequest(http.MethodGet, "/api/v1/node/default", nil))
	assertError(t, rec, http.StatusInternalServerError, codeInternal)
}

func TestNodeDefaultAPIUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		service ToolService
	}{
		{name: "no tooling service", service: nil},
		{name: "tooling without node default", service: &fakeToolService{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(t, Config{Tooling: tc.service})
			rec := httptest.NewRecorder()
			fx.server.handleNodeDefault(rec, httptest.NewRequest(http.MethodGet, "/api/v1/node/default", nil))
			assertError(t, rec, http.StatusServiceUnavailable, codeServiceUnavailable)
		})
	}
}
