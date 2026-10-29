package agent_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

func TestAgentFSListing(t *testing.T) {
	root := t.TempDir()
	server, identity := newTestAgentAt(t, root)
	r := startAgentTestServer(t, server, identity, 1)
	defer r.close()

	// Create test directories and files under the managed projects root.
	testDir := filepath.Join(root, "projects")
	if err := os.MkdirAll(filepath.Join(testDir, "site-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(testDir, "site-b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(testDir, "ignored-file.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := agentFSGet(t, r, testDir)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var listing agentproto.FSListing
	if err := json.NewDecoder(resp.Body).Decode(&listing); err != nil {
		t.Fatal(err)
	}

	if len(listing.Entries) != 2 {
		t.Fatalf("expected 2 directory entries, got %d", len(listing.Entries))
	}
	if listing.Entries[0].Name != "site-a" || listing.Entries[1].Name != "site-b" {
		t.Fatalf("unexpected entries: %+v", listing.Entries)
	}
}

func TestAgentFSConfinesBrowseToProjects(t *testing.T) {
	root := t.TempDir()
	server, identity := newTestAgentAt(t, root)
	r := startAgentTestServer(t, server, identity, 1)
	defer r.close()

	projects := filepath.Join(root, "projects")
	for _, dir := range []string{
		filepath.Join(projects, "site-a"),
		filepath.Join(root, "secrets"),
		filepath.Join(root, "system"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		name string
		path string
		want int
	}{
		{name: "projects root", path: projects, want: http.StatusOK},
		{name: "project child", path: filepath.Join(projects, "site-a"), want: http.StatusOK},
		{name: "secrets sibling", path: filepath.Join(root, "secrets"), want: http.StatusForbidden},
		{name: "system sibling", path: filepath.Join(root, "system"), want: http.StatusForbidden},
		{name: "absolute outside", path: "/etc", want: outsidePathStatus()},
		{name: "parent traversal", path: filepath.Join(projects, "..", "secrets"), want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := agentFSGet(t, r, tc.path)
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func outsidePathStatus() int {
	if runtime.GOOS == "windows" {
		// "/etc" is drive-rooted but not absolute on Windows, so it is
		// rejected by the absolute-path check before containment.
		return http.StatusBadRequest
	}
	return http.StatusForbidden
}

func agentFSGet(t *testing.T, r testAgentRun, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, r.url+agentproto.PathFS+"?path="+url.QueryEscape(path), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(agentproto.HeaderAuth, agentproto.BearerPrefix+testToken)
	req.Header.Set(agentproto.HeaderProtocol, agentproto.ProtocolVersion)
	resp, err := r.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
