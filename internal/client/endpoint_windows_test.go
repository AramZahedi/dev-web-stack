//go:build windows

package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/daemon"
	"github.com/AramZahedi/dev-web-stack/internal/ipc"
)

// TestNewAcceptsNamedPipeWithoutFilesystemCheck proves the CLI does not try to
// os.Stat a named pipe. The first request, not client construction, reports a
// missing daemon on Windows.
func TestNewAcceptsNamedPipeWithoutFilesystemCheck(t *testing.T) {
	t.Setenv("DEVSTACK_HOME", t.TempDir())
	c, err := New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = c.Close()
}

func TestWindowsClientTransportsRequestsOverSIDCheckedNamedPipe(t *testing.T) {
	home := t.TempDir()
	endpoint := daemon.Endpoint(home)
	sid, err := ipc.CurrentSID()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := ipc.Listen(endpoint, ipc.WithAllowSID(sid))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sites" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]any{})
	}))
	server.Listener = listener
	server.Start()
	defer server.Close()

	client, err := New(home)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer client.Close()
	sites, err := client.ListSites(context.Background())
	if err != nil {
		t.Fatalf("ListSites over named pipe: %v", err)
	}
	if len(sites) != 0 {
		t.Fatalf("sites = %#v, want empty list", sites)
	}
}
