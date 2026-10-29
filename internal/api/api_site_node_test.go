package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
)

// nodeToolchainSiteService supplies the resolver surface without changing the
// shared fake used by other API tests.
type nodeToolchainSiteService struct {
	*fakeSiteService
	summary model.NodeToolchain
}

func (s *nodeToolchainSiteService) ResolveNodeToolchain(context.Context, model.Site) (model.NodeToolchain, error) {
	return s.summary, nil
}

func TestSiteAPIReturnsNodeToolchainSummary(t *testing.T) {
	summary := model.NodeToolchain{
		Version:   "22.11.0",
		BuildID:   "node@22.11.0",
		Source:    model.NodeSourceGlobal,
		Installed: true,
	}
	fake := &nodeToolchainSiteService{
		fakeSiteService: &fakeSiteService{sites: map[string]model.Site{
			"s1": {
				ID: "s1", Name: "api", Kind: model.SiteNode,
				NodeVersion: "22.11.0",
			},
		}},
		summary: summary,
	}
	server := &Server{sites: fake}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sites/s1", nil)
	req.SetPathValue("ref", "s1")
	rec := httptest.NewRecorder()
	server.handleGetSite(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get site status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var fetched model.Site
	if err := json.Unmarshal(rec.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("decode site: %v", err)
	}
	if fetched.NodeToolchain == nil {
		t.Fatalf("get site omitted node_toolchain: %s", rec.Body.String())
	}
	if fetched.NodeVersion != "22.11.0" ||
		fetched.NodeToolchain.Version != summary.Version ||
		fetched.NodeToolchain.BuildID != summary.BuildID ||
		fetched.NodeToolchain.Source != summary.Source ||
		!fetched.NodeToolchain.Installed {
		t.Fatalf("node toolchain summary = %+v", fetched.NodeToolchain)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/sites", nil)
	rec = httptest.NewRecorder()
	server.handleListSites(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list sites status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var listed []model.Site
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode site list: %v", err)
	}
	if len(listed) != 1 || listed[0].NodeToolchain == nil ||
		listed[0].NodeToolchain.Version != summary.Version {
		t.Fatalf("listed sites = %+v", listed)
	}
}

// nodeUpdateRecorder captures the decoded update request while satisfying the
// rest of the API's site surface through the shared fake.
type nodeUpdateRecorder struct {
	*fakeSiteService
	update sites.UpdateRequest
	calls  int
}

func (r *nodeUpdateRecorder) Update(_ context.Context, _ string, req sites.UpdateRequest) (model.Site, error) {
	r.update = req
	r.calls++
	return model.Site{ID: "s1", Name: "api", Kind: model.SiteNode}, nil
}

func TestSiteAPIDistinguishesOmittedNodeVersionFromNull(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     string
		wantSet  bool
		wantPin  string
		wantCode int
	}{
		{
			name:     "omitted",
			body:     `{}`,
			wantSet:  false,
			wantCode: http.StatusOK,
		},
		{
			name:     "explicit null",
			body:     `{"node_version":null}`,
			wantSet:  true,
			wantPin:  "",
			wantCode: http.StatusOK,
		},
		{
			name:     "exact version",
			body:     `{"node_version":"22.11.0"}`,
			wantSet:  true,
			wantPin:  "22.11.0",
			wantCode: http.StatusOK,
		},
		{
			name:     "wrong type",
			body:     `{"node_version":22}`,
			wantCode: http.StatusBadRequest,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &nodeUpdateRecorder{fakeSiteService: &fakeSiteService{}}
			server := &Server{sites: recorder}
			req := httptest.NewRequest(http.MethodPatch, "/api/v1/sites/s1", strings.NewReader(tc.body))
			req.SetPathValue("ref", "s1")
			rec := httptest.NewRecorder()
			server.handleUpdateSite(rec, req)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if tc.wantCode != http.StatusOK {
				if recorder.calls != 0 {
					t.Fatalf("invalid payload reached the service: %+v", recorder.update)
				}
				return
			}
			if recorder.calls != 1 {
				t.Fatalf("update calls = %d", recorder.calls)
			}
			if (recorder.update.NodeVersion != nil) != tc.wantSet {
				t.Fatalf("node_version set = %v, want %v", recorder.update.NodeVersion != nil, tc.wantSet)
			}
			if tc.wantSet && *recorder.update.NodeVersion != tc.wantPin {
				t.Fatalf("node_version = %q, want %q", *recorder.update.NodeVersion, tc.wantPin)
			}
		})
	}
}
