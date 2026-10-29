package agent_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

func TestAgentFSListing(t *testing.T) {
	server, identity := newTestAgent(t)
	r := startAgentTestServer(t, server, identity, 1)
	defer r.close()

	// Create test directories and files in a temp directory
	testDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(testDir, "site-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(testDir, "site-b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(testDir, "ignored-file.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodGet, r.url+agentproto.PathFS+"?path="+url.QueryEscape(testDir), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(agentproto.HeaderAuth, agentproto.BearerPrefix+testToken)
	req.Header.Set(agentproto.HeaderProtocol, agentproto.ProtocolVersion)

	resp, err := r.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
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
