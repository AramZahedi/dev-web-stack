package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

type fakeWSLFSBrowser struct {
	requestedPath string
	listing       agentproto.FSListing
	err           error
}

func (f *fakeWSLFSBrowser) BrowseFS(ctx context.Context, path string) (agentproto.FSListing, error) {
	f.requestedPath = path
	return f.listing, f.err
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
