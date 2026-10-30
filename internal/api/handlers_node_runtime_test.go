package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/nodereleases"
)

// fakeNodeReleaseRuntimeService adds the narrow Node discovery surface to the
// existing RuntimeService so the handler can be tested without a network.
type fakeNodeReleaseRuntimeService struct {
	RuntimeService

	snapshot  nodereleases.Snapshot
	preflight nodereleases.Preflight

	listErr      error
	preflightErr error
	refreshErr   error

	listCalls      int
	preflightCalls int
	refreshCalls   int

	major   string
	all     bool
	offline bool
	version string
}

func (f *fakeNodeReleaseRuntimeService) NodeReleases(_ context.Context, major string, all, offline bool) (nodereleases.Snapshot, error) {
	f.listCalls++
	f.major, f.all, f.offline = major, all, offline
	return f.snapshot, f.listErr
}

func (f *fakeNodeReleaseRuntimeService) NodeRelease(_ context.Context, version string) (nodereleases.Preflight, error) {
	f.preflightCalls++
	f.version = version
	return f.preflight, f.preflightErr
}

func (f *fakeNodeReleaseRuntimeService) RefreshNodeReleases(context.Context) (nodereleases.Snapshot, error) {
	f.refreshCalls++
	return f.snapshot, f.refreshErr
}

func nodeReleaseTestRelease(version, npm, lts string, security bool) nodereleases.Release {
	return nodereleases.Release{
		Version:  version,
		Date:     "2026-10-01",
		LTS:      lts,
		Security: security,
		NPM:      npm,
		Modules:  "137",
		File:     "node-v" + version + "-linux-x64.tar.gz",
	}
}

func newNodeReleaseAPIFixture(t *testing.T, service *fakeNodeReleaseRuntimeService, cfg Config) *apiFixture {
	t.Helper()
	fx := newFixture(t, cfg)
	fx.server.runtimes = service
	return fx
}

func TestNodeReleasesDefaultsToLatestPerMajor(t *testing.T) {
	latest24 := nodeReleaseTestRelease("24.9.0", "11.0.0", "", false)
	latest22 := nodeReleaseTestRelease("22.20.0", "10.9.0", "Jod", true)
	snapshot := nodereleases.Snapshot{
		Majors: []nodereleases.Major{
			{Major: 24, Latest: latest24, Releases: []nodereleases.Release{latest24}},
			{Major: 22, Latest: latest22, Releases: []nodereleases.Release{latest22}},
		},
		All:      []nodereleases.Release{latest24, latest22},
		Source:   nodereleases.SourceCache,
		CachedAt: time.Date(2026, 10, 30, 6, 0, 0, 0, time.UTC),
		Stale:    true,
		Warning:  "could not refresh the node release index: network unavailable",
	}
	service := &fakeNodeReleaseRuntimeService{snapshot: snapshot}
	fx := newNodeReleaseAPIFixture(t, service, Config{})

	rec := httptest.NewRecorder()
	fx.server.handleListNodeReleases(rec, httptest.NewRequest(http.MethodGet, "/api/v1/runtimes/node/releases", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q", ct)
	}
	if cache := rec.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("cache-control = %q", cache)
	}
	if service.listCalls != 1 {
		t.Fatalf("NodeReleases calls = %d, want 1", service.listCalls)
	}
	if service.major != "" || service.all || service.offline {
		t.Fatalf("options = major %q all=%v offline=%v, want defaults", service.major, service.all, service.offline)
	}

	var got nodereleases.Snapshot
	decode(t, rec, &got)
	if got.Source != nodereleases.SourceCache || !got.Stale || got.Warning != snapshot.Warning {
		t.Fatalf("snapshot provenance = %+v", got)
	}
	if len(got.Majors) != 2 {
		t.Fatalf("majors = %+v", got.Majors)
	}
	for _, major := range got.Majors {
		if len(major.Releases) != 1 {
			t.Fatalf("default major %d releases = %+v, want only the latest patch", major.Major, major.Releases)
		}
		if major.Releases[0].Version != major.Latest.Version {
			t.Fatalf("major %d latest = %s releases[0] = %s", major.Major, major.Latest.Version, major.Releases[0].Version)
		}
	}
	if got.Majors[0].Major != 24 || got.Majors[1].Major != 22 {
		t.Fatalf("major order = %+v", got.Majors)
	}

	// A runtime service that predates discovery must not break existing routes;
	// the optional interface simply reports service unavailable.
	fx.server.runtimes = &fakeRuntimeService{}
	rec = httptest.NewRecorder()
	fx.server.handleListNodeReleases(rec, httptest.NewRequest(http.MethodGet, "/api/v1/runtimes/node/releases", nil))
	assertError(t, rec, http.StatusServiceUnavailable, codeServiceUnavailable)
}

func TestNodeReleasesMajorAllAndRefresh(t *testing.T) {
	release := nodeReleaseTestRelease("22.11.0", "10.9.0", "Jod", false)
	snapshot := nodereleases.Snapshot{
		Majors: []nodereleases.Major{{Major: 22, Latest: release, Releases: []nodereleases.Release{release}}},
		All:    []nodereleases.Release{release},
		Source: nodereleases.SourceLive,
	}
	service := &fakeNodeReleaseRuntimeService{snapshot: snapshot}
	fx := newNodeReleaseAPIFixture(t, service, Config{})

	rec := httptest.NewRecorder()
	fx.server.handleListNodeReleases(rec, httptest.NewRequest(
		http.MethodGet,
		"/api/v1/runtimes/node/releases?major=22&all=true&offline=true",
		nil,
	))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
	}
	if service.listCalls != 1 || service.major != "22" || !service.all || !service.offline {
		t.Fatalf("options = calls %d major %q all=%v offline=%v", service.listCalls, service.major, service.all, service.offline)
	}

	// A bare flag means enabled, matching the CLI's boolean flag style.
	rec = httptest.NewRecorder()
	fx.server.handleListNodeReleases(rec, httptest.NewRequest(
		http.MethodGet,
		"/api/v1/runtimes/node/releases?offline",
		nil,
	))
	if rec.Code != http.StatusOK {
		t.Fatalf("bare offline code = %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.offline || service.all {
		t.Fatalf("bare offline options = all=%v offline=%v", service.all, service.offline)
	}

	for _, target := range []string{
		"/api/v1/runtimes/node/releases?major=bad",
		"/api/v1/runtimes/node/releases?major=0",
		"/api/v1/runtimes/node/releases?major=-1",
		"/api/v1/runtimes/node/releases?all=maybe",
		"/api/v1/runtimes/node/releases?offline=maybe",
	} {
		rec = httptest.NewRecorder()
		fx.server.handleListNodeReleases(rec, httptest.NewRequest(http.MethodGet, target, nil))
		assertError(t, rec, http.StatusBadRequest, codeInvalidRequest)
	}
	if service.listCalls != 2 {
		t.Fatalf("invalid queries reached the service: calls = %d", service.listCalls)
	}

	// Refresh is a POST action; query flags on it are ignored because the
	// refreshed snapshot is always the default newest-per-major view.
	rec = httptest.NewRecorder()
	fx.server.handleRefreshNodeReleases(rec, httptest.NewRequest(
		http.MethodPost,
		"/api/v1/runtimes/node/refresh?all=true",
		nil,
	))
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh code = %d body=%s", rec.Code, rec.Body.String())
	}
	if service.refreshCalls != 1 {
		t.Fatalf("refresh calls = %d", service.refreshCalls)
	}
	var refreshed nodereleases.Snapshot
	decode(t, rec, &refreshed)
	if refreshed.Source != nodereleases.SourceLive || len(refreshed.Majors) != 1 {
		t.Fatalf("refresh response = %+v", refreshed)
	}

	// Warnings are part of the snapshot contract, but private cache paths are
	// redacted before they leave the daemon.
	service.snapshot.Warning = `could not cache the node release index: ` +
		`open C:\Users\Example User\.devstack\cache\node\index.json: access denied`
	rec = httptest.NewRecorder()
	fx.server.handleListNodeReleases(rec, httptest.NewRequest(http.MethodGet, "/api/v1/runtimes/node/releases", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("warning code = %d body=%s", rec.Code, rec.Body.String())
	}
	var warningSnapshot nodereleases.Snapshot
	decode(t, rec, &warningSnapshot)
	if strings.Contains(warningSnapshot.Warning, `cache\node`) ||
		strings.Contains(warningSnapshot.Warning, "Example User") {
		t.Fatalf("warning leaked the cache path: %q", warningSnapshot.Warning)
	}
	if !strings.Contains(warningSnapshot.Warning, "<cache>") {
		t.Fatalf("warning lost its actionable context: %q", warningSnapshot.Warning)
	}

	// Discovery failures are sanitized: no raw URLs, cache paths, or internal
	// transport details reach the client.
	service.refreshErr = errors.New(
		`nodereleases: GET https://nodejs.org/dist/index.json: ` +
			`open C:\Users\example\.devstack\cache\node\index.json: access denied`,
	)
	rec = httptest.NewRecorder()
	fx.server.handleRefreshNodeReleases(rec, httptest.NewRequest(http.MethodPost, "/api/v1/runtimes/node/refresh", nil))
	assertError(t, rec, http.StatusBadGateway, codeServiceUnavailable)
	if body := rec.Body.String(); strings.Contains(body, "nodejs.org") ||
		strings.Contains(body, "cache") || strings.Contains(body, "C:\\") {
		t.Fatalf("refresh error leaked internals: %s", body)
	}
}

func TestNodeReleasePreflightReturnsChecksumAndSize(t *testing.T) {
	checksum := strings.Repeat("a", 64)
	release := nodeReleaseTestRelease("22.11.0", "10.9.0", "Jod", true)
	release.File = "node-v22.11.0-linux-x64.tar.gz"
	preflight := nodereleases.Preflight{
		Entry: catalog.Entry{
			ID:      "node@22.11.0",
			Kind:    "node",
			Version: "22.11.0",
			Build:   "linux-x64",
			URL:     "https://nodejs.org/dist/v22.11.0/node-v22.11.0-linux-x64.tar.gz",
			SHA256:  checksum,
			Size:    31_234_567,
		},
		Release: release,
	}
	service := &fakeNodeReleaseRuntimeService{preflight: preflight}
	fx := newNodeReleaseAPIFixture(t, service, Config{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/runtimes/node/releases/22.11.0", nil)
	req.SetPathValue("version", "22.11.0")
	rec := httptest.NewRecorder()
	fx.server.handleGetNodeRelease(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
	}
	if service.preflightCalls != 1 || service.version != "22.11.0" {
		t.Fatalf("preflight calls = %d version = %q", service.preflightCalls, service.version)
	}

	var got nodereleases.Preflight
	decode(t, rec, &got)
	if got.Entry.SHA256 != checksum {
		t.Fatalf("checksum = %q, want %q", got.Entry.SHA256, checksum)
	}
	if got.Entry.Size != 31_234_567 {
		t.Fatalf("size = %d", got.Entry.Size)
	}
	if got.Entry.ID != "node@22.11.0" || got.Entry.Build != "linux-x64" ||
		got.Entry.URL != preflight.Entry.URL {
		t.Fatalf("entry = %+v", got.Entry)
	}
	if got.Release.Version != release.Version ||
		got.Release.NPM != release.NPM ||
		got.Release.LTS != release.LTS ||
		got.Release.Security != release.Security ||
		got.Release.File != release.File {
		t.Fatalf("release = %+v, want %+v", got.Release, release)
	}

	// Unknown selections are a typed not-found, not an internal error.
	service.preflightErr = fmt.Errorf("nodereleases: no release matches %q: %w", "99.0.0", nodereleases.ErrReleaseNotFound)
	req = httptest.NewRequest(http.MethodGet, "/api/v1/runtimes/node/releases/99.0.0", nil)
	req.SetPathValue("version", "99.0.0")
	rec = httptest.NewRecorder()
	fx.server.handleGetNodeRelease(rec, req)
	assertError(t, rec, http.StatusNotFound, codeNotFound)

	// Preflight metadata failures are sanitized upstream errors.
	service.preflightErr = errors.New(
		"nodereleases: HEAD https://nodejs.org/dist/v22.11.0/node-v22.11.0-linux-x64.tar.gz: " +
			"read C:\\Users\\example\\.devstack\\cache\\node\\sizes\\22.11.0.json: timeout",
	)
	req = httptest.NewRequest(http.MethodGet, "/api/v1/runtimes/node/releases/22.11.0", nil)
	req.SetPathValue("version", "22.11.0")
	rec = httptest.NewRecorder()
	fx.server.handleGetNodeRelease(rec, req)
	assertError(t, rec, http.StatusBadGateway, codeServiceUnavailable)
	body := rec.Body.String()
	if strings.Contains(body, "nodejs.org") || strings.Contains(body, "cache") || strings.Contains(body, "C:\\") {
		t.Fatalf("preflight error leaked internals: %s", body)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/runtimes/node/releases/", nil)
	req.SetPathValue("version", "  ")
	rec = httptest.NewRecorder()
	fx.server.handleGetNodeRelease(rec, req)
	assertError(t, rec, http.StatusBadRequest, codeInvalidRequest)
}

func TestNodeReleaseEndpointsRequireAuth(t *testing.T) {
	service := &fakeNodeReleaseRuntimeService{snapshot: nodereleases.Snapshot{Source: nodereleases.SourceEmbedded}}
	fx := newNodeReleaseAPIFixture(t, service, Config{AuthToken: "required-token"})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/runtimes/node/releases", fx.server.handleListNodeReleases)
	mux.HandleFunc("GET /api/v1/runtimes/node/releases/{version}", fx.server.handleGetNodeRelease)
	mux.HandleFunc("POST /api/v1/runtimes/node/refresh", fx.server.handleRefreshNodeReleases)
	handler := fx.server.authenticate(mux)

	request := func(method, target, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://127.0.0.1"+target, nil)
		req.RemoteAddr = "127.0.0.1:54321"
		if token != "" {
			req.Header.Set(authTokenHeader, token)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	for _, target := range []string{
		"/api/v1/runtimes/node/releases",
		"/api/v1/runtimes/node/releases/22.11.0",
		"/api/v1/runtimes/node/refresh",
	} {
		method := http.MethodGet
		if strings.HasSuffix(target, "/refresh") {
			method = http.MethodPost
		}
		assertError(t, request(method, target, ""), http.StatusForbidden, codeForbidden)
	}
	if service.listCalls != 0 || service.preflightCalls != 0 || service.refreshCalls != 0 {
		t.Fatalf("unauthenticated requests reached discovery: list=%d preflight=%d refresh=%d",
			service.listCalls, service.preflightCalls, service.refreshCalls)
	}

	rec := request(http.MethodGet, "/api/v1/runtimes/node/releases", "required-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated list code = %d body=%s", rec.Code, rec.Body.String())
	}

	// Mutations still need the dashboard session and CSRF token after the
	// install-token gate passes.
	assertError(t, request(http.MethodPost, "/api/v1/runtimes/node/refresh", "required-token"),
		http.StatusForbidden, codeForbidden)
	if service.refreshCalls != 0 {
		t.Fatalf("refresh ran without CSRF: calls = %d", service.refreshCalls)
	}

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/v1/runtimes/node/refresh", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set(authTokenHeader, "required-token")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: fx.session})
	req.Header.Set(csrfHeader, fx.server.csrfFor(fx.session))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated refresh code = %d body=%s", rec.Code, rec.Body.String())
	}
	if service.refreshCalls != 1 {
		t.Fatalf("authenticated refresh calls = %d", service.refreshCalls)
	}
}

func assertNoNodeReleaseFetchLeak(t *testing.T, label, text string) {
	t.Helper()
	for _, detail := range []string{
		"https://",
		"http://",
		"nodejs.org",
		"dist/index.json",
		"dial tcp",
		"104.20.28.92",
		"connectex",
		"connection attempt failed",
		"nodereleases:",
	} {
		if strings.Contains(text, detail) {
			t.Fatalf("%s leaked %q: %s", label, detail, text)
		}
	}
}

func TestNodeReleaseWarningsAndLogsSanitizeFetchDetails(t *testing.T) {
	const (
		indexURL  = "https://nodejs.org/dist/index.json"
		transport = "dial tcp 104.20.28.92:443: connectex: A connection attempt failed because the connected party did not properly respond"
	)
	warning := "could not refresh the node release index: nodereleases: GET " + indexURL + ": " + transport

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	service := &fakeNodeReleaseRuntimeService{snapshot: nodereleases.Snapshot{
		Source:  nodereleases.SourceEmbedded,
		Stale:   true,
		Warning: warning,
	}}
	fx := newNodeReleaseAPIFixture(t, service, Config{Logger: logger})

	rec := httptest.NewRecorder()
	fx.server.handleListNodeReleases(rec, httptest.NewRequest(http.MethodGet, "/api/v1/runtimes/node/releases", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list code = %d body=%s", rec.Code, rec.Body.String())
	}
	var snapshot nodereleases.Snapshot
	decode(t, rec, &snapshot)
	if snapshot.Warning != "could not refresh the node release index" {
		t.Fatalf("warning = %q", snapshot.Warning)
	}
	assertNoNodeReleaseFetchLeak(t, "warning", snapshot.Warning)

	// Error logs use the same sanitizer, so transport diagnostics cannot leak
	// through the daemon log even though the response is already generic.
	service.refreshErr = errors.New(warning)
	rec = httptest.NewRecorder()
	fx.server.handleRefreshNodeReleases(rec, httptest.NewRequest(http.MethodPost, "/api/v1/runtimes/node/refresh", nil))
	assertError(t, rec, http.StatusBadGateway, codeServiceUnavailable)
	assertNoNodeReleaseFetchLeak(t, "refresh error response", rec.Body.String())

	logOutput := logs.String()
	if !strings.Contains(logOutput, "node release request failed") {
		t.Fatalf("expected a discovery failure log entry, got %q", logOutput)
	}
	assertNoNodeReleaseFetchLeak(t, "logs", logOutput)
}
