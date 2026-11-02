package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/pathmode"
)

type fakeWSLFSBrowser struct {
	requestedPath string
	listing       agentproto.FSListing
	err           error
	mkdirPath     string
	mkdirName     string
	mkdirListing  agentproto.FSListing
	mkdirErr      error
}

func (f *fakeWSLFSBrowser) BrowseFS(ctx context.Context, path string) (agentproto.FSListing, error) {
	f.requestedPath = path
	return f.listing, f.err
}

func (f *fakeWSLFSBrowser) MkdirFS(ctx context.Context, path, name string) (agentproto.FSListing, error) {
	f.mkdirPath = path
	f.mkdirName = name
	return f.mkdirListing, f.mkdirErr
}

func TestHandleFSWSLRejectsTraversalBeforeBrowsing(t *testing.T) {
	mockBrowser := &fakeWSLFSBrowser{}
	server := &Server{cfg: Config{UserHome: t.TempDir(), WSLFS: mockBrowser}}

	for _, path := range []string{
		"../../etc",
		"/data/devstack/projects/../../etc",
		"data/devstack/projects",
		"/data/devstack/projects\x00",
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/fs?mode=wsl&path="+url.QueryEscape(path), nil)
		rec := httptest.NewRecorder()
		server.handleFS(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("path %q: code = %d, want 400; body = %s", path, rec.Code, rec.Body.String())
		}
	}
	if mockBrowser.requestedPath != "" {
		t.Fatalf("unsafe path reached the agent: %q", mockBrowser.requestedPath)
	}
}

func TestHandleFSWithWSLMode(t *testing.T) {
	mockBrowser := &fakeWSLFSBrowser{
		listing: agentproto.FSListing{
			Path: "/data/devstack/projects",
			Entries: []agentproto.FSEntry{
				{Name: "site-one", Path: "/data/devstack/projects/site-one"},
				{Name: "site-two", Path: "/data/devstack/projects/site-two"},
			},
		},
	}

	home := t.TempDir()
	server := &Server{
		cfg: Config{
			UserHome: home,
			WSLFS:    mockBrowser,
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fs?mode=wsl&path=/data/devstack/projects", nil)
	rec := httptest.NewRecorder()
	server.handleFS(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if mockBrowser.requestedPath != "/data/devstack/projects" {
		t.Fatalf("requestedPath = %q, want /data/devstack/projects", mockBrowser.requestedPath)
	}
}

func TestHandleFSMkdirWSL(t *testing.T) {
	mockBrowser := &fakeWSLFSBrowser{
		mkdirListing: agentproto.FSListing{Path: "/data/devstack/projects/app", Entries: []agentproto.FSEntry{}},
	}
	server := &Server{cfg: Config{UserHome: t.TempDir(), WSLFS: mockBrowser}}
	body := `{"mode":"wsl","path":"/data/devstack/projects","name":"app"}`
	rec := httptest.NewRecorder()
	server.handleFSMkdir(rec, httptest.NewRequest(http.MethodPost, "/api/v1/fs/mkdir", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if mockBrowser.mkdirPath != "/data/devstack/projects" || mockBrowser.mkdirName != "app" {
		t.Fatalf("mkdir call = %q/%q", mockBrowser.mkdirPath, mockBrowser.mkdirName)
	}
}

func TestHandleFSWSLConfinedToProjectsRoot(t *testing.T) {
	mockBrowser := &fakeWSLFSBrowser{
		listing: agentproto.FSListing{Path: pathmode.WSLProjectsRoot, Entries: []agentproto.FSEntry{}},
	}
	server := &Server{cfg: Config{UserHome: t.TempDir(), WSLFS: mockBrowser}}

	for _, path := range []string{
		"/",
		"/etc",
		"/data/devstack",
		"/data/devstack/projects-old",
		"/data/other",
	} {
		mockBrowser.requestedPath = ""
		req := httptest.NewRequest(http.MethodGet, "/api/v1/fs?mode=wsl&path="+url.QueryEscape(path), nil)
		rec := httptest.NewRecorder()
		server.handleFS(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("path %q: code = %d, want 403; body = %s", path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"FORBIDDEN"`) {
			t.Fatalf("path %q: missing FORBIDDEN code: %s", path, rec.Body.String())
		}
		if mockBrowser.requestedPath != "" {
			t.Fatalf("outside path %q reached the agent as %q", path, mockBrowser.requestedPath)
		}
	}

	for _, path := range []string{pathmode.WSLProjectsRoot, pathmode.WSLProjectsRoot + "/site-one"} {
		mockBrowser.requestedPath = ""
		req := httptest.NewRequest(http.MethodGet, "/api/v1/fs?mode=wsl&path="+url.QueryEscape(path), nil)
		rec := httptest.NewRecorder()
		server.handleFS(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("path %q: code = %d, want 200; body = %s", path, rec.Code, rec.Body.String())
		}
		if mockBrowser.requestedPath != path {
			t.Fatalf("path %q: agent got %q", path, mockBrowser.requestedPath)
		}
	}

	// An omitted WSL path starts at the managed projects root.
	mockBrowser.requestedPath = ""
	rec := httptest.NewRecorder()
	server.handleFS(rec, httptest.NewRequest(http.MethodGet, "/api/v1/fs?mode=wsl", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("default WSL browse code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if mockBrowser.requestedPath != pathmode.WSLProjectsRoot {
		t.Fatalf("default WSL browse path = %q, want %q", mockBrowser.requestedPath, pathmode.WSLProjectsRoot)
	}
}

func TestHandleFSMkdirWSLConfinedToProjectsRoot(t *testing.T) {
	for _, path := range []string{"/", "/etc", "/data/devstack", "/data/devstack/projects-old"} {
		mockBrowser := &fakeWSLFSBrowser{}
		server := &Server{cfg: Config{UserHome: t.TempDir(), WSLFS: mockBrowser}}
		body := fmt.Sprintf(`{"mode":"wsl","path":%q,"name":"app"}`, path)
		rec := httptest.NewRecorder()
		server.handleFSMkdir(rec, httptest.NewRequest(http.MethodPost, "/api/v1/fs/mkdir", strings.NewReader(body)))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("path %q: code = %d, want 403; body = %s", path, rec.Code, rec.Body.String())
		}
		if mockBrowser.mkdirPath != "" || mockBrowser.mkdirName != "" {
			t.Fatalf("outside path %q reached the agent as %q/%q", path, mockBrowser.mkdirPath, mockBrowser.mkdirName)
		}
	}

	for _, path := range []string{pathmode.WSLProjectsRoot, pathmode.WSLProjectsRoot + "/child"} {
		mockBrowser := &fakeWSLFSBrowser{
			mkdirListing: agentproto.FSListing{Path: path + "/app", Entries: []agentproto.FSEntry{}},
		}
		server := &Server{cfg: Config{UserHome: t.TempDir(), WSLFS: mockBrowser}}
		body := fmt.Sprintf(`{"mode":"wsl","path":%q,"name":"app"}`, path)
		rec := httptest.NewRecorder()
		server.handleFSMkdir(rec, httptest.NewRequest(http.MethodPost, "/api/v1/fs/mkdir", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("path %q: code = %d, want 200; body = %s", path, rec.Code, rec.Body.String())
		}
		if mockBrowser.mkdirPath != path || mockBrowser.mkdirName != "app" {
			t.Fatalf("path %q: mkdir call = %q/%q", path, mockBrowser.mkdirPath, mockBrowser.mkdirName)
		}
	}

	// An omitted WSL parent creates below the managed projects root.
	mockBrowser := &fakeWSLFSBrowser{
		mkdirListing: agentproto.FSListing{Path: pathmode.WSLProjectsRoot + "/app", Entries: []agentproto.FSEntry{}},
	}
	server := &Server{cfg: Config{UserHome: t.TempDir(), WSLFS: mockBrowser}}
	rec := httptest.NewRecorder()
	server.handleFSMkdir(rec, httptest.NewRequest(http.MethodPost, "/api/v1/fs/mkdir", strings.NewReader(`{"mode":"wsl","name":"app"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("default WSL mkdir code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if mockBrowser.mkdirPath != pathmode.WSLProjectsRoot {
		t.Fatalf("default WSL mkdir path = %q, want %q", mockBrowser.mkdirPath, pathmode.WSLProjectsRoot)
	}
}

func TestHandleFSMkdirWSLConflict(t *testing.T) {
	mockBrowser := &fakeWSLFSBrowser{mkdirErr: agentproto.ErrConflict}
	server := &Server{cfg: Config{UserHome: t.TempDir(), WSLFS: mockBrowser}}
	body := `{"mode":"wsl","path":"/data/devstack/projects","name":"app"}`
	rec := httptest.NewRecorder()
	server.handleFSMkdir(rec, httptest.NewRequest(http.MethodPost, "/api/v1/fs/mkdir", strings.NewReader(body)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("code = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFSMkdirLocalCreatesAndRejectsDuplicates(t *testing.T) {
	home := t.TempDir()
	projects := filepath.Join(home, "projects")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	server := &Server{cfg: Config{UserHome: home}}
	// An omitted path creates below the effective projects root.
	body := `{"name":"app"}`
	rec := httptest.NewRecorder()
	server.handleFSMkdir(rec, httptest.NewRequest(http.MethodPost, "/api/v1/fs/mkdir", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if info, err := os.Stat(filepath.Join(projects, "app")); err != nil || !info.IsDir() {
		t.Fatalf("created directory = %v, %v", info, err)
	}
	rec = httptest.NewRecorder()
	server.handleFSMkdir(rec, httptest.NewRequest(http.MethodPost, "/api/v1/fs/mkdir", strings.NewReader(body)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate code = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFSMkdirRejectsTraversalAndOutsideRoot(t *testing.T) {
	home := t.TempDir()
	projects := filepath.Join(home, "projects")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, "projects-old"), 0o755); err != nil {
		t.Fatal(err)
	}
	server := &Server{cfg: Config{UserHome: home}}
	for _, body := range []string{
		fmt.Sprintf(`{"path":%q,"name":"../escape"}`, projects),
		fmt.Sprintf(`{"path":%q,"name":"."}`, projects),
		fmt.Sprintf(`{"path":%q,"name":"CON"}`, projects),
	} {
		rec := httptest.NewRecorder()
		server.handleFSMkdir(rec, httptest.NewRequest(http.MethodPost, "/api/v1/fs/mkdir", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: code = %d, want 400", body, rec.Code)
		}
	}
	outside := t.TempDir()
	for _, path := range []string{home, filepath.Join(home, "projects-old"), outside, filepath.Join(outside, "missing")} {
		body := fmt.Sprintf(`{"path":%q,"name":"app"}`, path)
		rec := httptest.NewRecorder()
		server.handleFSMkdir(rec, httptest.NewRequest(http.MethodPost, "/api/v1/fs/mkdir", strings.NewReader(body)))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("path %q: code = %d, want 403; body = %s", path, rec.Code, rec.Body.String())
		}
	}

	traversal := projects + string(filepath.Separator) + ".." + string(filepath.Separator) + "projects-old"
	rec := httptest.NewRecorder()
	server.handleFSMkdir(rec, httptest.NewRequest(http.MethodPost, "/api/v1/fs/mkdir",
		strings.NewReader(fmt.Sprintf(`{"path":%q,"name":"app"}`, traversal))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("traversal code = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}

	// A symlinked parent below the root that resolves outside is forbidden.
	if err := os.Symlink(outside, filepath.Join(projects, "escape")); err == nil {
		body := fmt.Sprintf(`{"path":%q,"name":"app"}`, filepath.Join(projects, "escape"))
		rec := httptest.NewRecorder()
		server.handleFSMkdir(rec, httptest.NewRequest(http.MethodPost, "/api/v1/fs/mkdir", strings.NewReader(body)))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("symlink escape code = %d, want 403; body = %s", rec.Code, rec.Body.String())
		}
	}
}
