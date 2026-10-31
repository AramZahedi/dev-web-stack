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
	body := fmt.Sprintf(`{"path":%q,"name":"app"}`, projects)
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

func TestHandleFSMkdirRejectsTraversalAndOutsideHome(t *testing.T) {
	home := t.TempDir()
	projects := filepath.Join(home, "projects")
	if err := os.MkdirAll(projects, 0o755); err != nil {
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
	body := fmt.Sprintf(`{"path":%q,"name":"app"}`, outside)
	rec := httptest.NewRecorder()
	server.handleFSMkdir(rec, httptest.NewRequest(http.MethodPost, "/api/v1/fs/mkdir", strings.NewReader(body)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("outside code = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}
}
