package client

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/tooling"
)

// nodeDefaultTestClient returns a Client whose transport reaches an httptest
// server while the request URL keeps the daemon's canonical base URL. The
// existing socket-based fake lives in a !windows file, so this test owns its
// transport and runs on every platform.
func nodeDefaultTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	address := strings.TrimPrefix(server.URL, "http://")
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, address)
		},
	}
	t.Cleanup(transport.CloseIdleConnections)
	return &Client{http: &http.Client{Transport: transport}, timeout: 5 * time.Second}
}

func TestNodeDefaultClientGetsStatus(t *testing.T) {
	const shimDir = "/data/devstack/tools/node-default/bin"
	build := model.RuntimeBuild{
		ID:      "node@22.11.0",
		Kind:    "node",
		Version: "22.11.0",
		Backend: model.BackendLocal,
		Path:    "/data/devstack/runtimes/node@22.11.0",
	}
	var method, path string
	c := nodeDefaultTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tooling.NodeDefaultStatus{
			Configured:    "22",
			Build:         &build,
			ShimDir:       shimDir,
			ShimInstalled: true,
			Source:        tooling.NodeDefaultSourceSetting,
		})
	}))

	status, err := c.NodeDefault(context.Background())
	if err != nil {
		t.Fatalf("NodeDefault: %v", err)
	}
	if method != http.MethodGet || path != "/api/v1/node/default" {
		t.Fatalf("request = %s %s, want GET /api/v1/node/default", method, path)
	}
	if status.Configured != "22" || status.Source != tooling.NodeDefaultSourceSetting {
		t.Fatalf("status = %+v", status)
	}
	if status.Build == nil || status.Build.ID != build.ID || status.Build.Version != build.Version {
		t.Fatalf("build = %+v", status.Build)
	}
	if status.ShimDir != shimDir || !status.ShimInstalled {
		t.Fatalf("shim dir = %q installed = %v", status.ShimDir, status.ShimInstalled)
	}
}
