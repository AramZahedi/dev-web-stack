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

func TestNodeDefaultClientSetAndClear(t *testing.T) {
	type request struct {
		method string
		path   string
		body   map[string]any
	}
	var requests []request
	c := nodeDefaultTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		requests = append(requests, request{method: r.Method, path: r.URL.Path, body: body})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tooling.NodeDefaultStatus{
			Configured: "22.11.0",
			Build: &model.RuntimeBuild{
				ID:      "node@22.11.0",
				Kind:    "node",
				Version: "22.11.0",
				Path:    "/data/devstack/runtimes/node@22.11.0",
			},
			ShimDir:       "/data/devstack/tools/node-default/bin",
			ShimInstalled: true,
			Source:        tooling.NodeDefaultSourceSetting,
		})
	}))

	status, err := c.SetNodeDefault(context.Background(), "22.11.0", true)
	if err != nil {
		t.Fatalf("SetNodeDefault: %v", err)
	}
	if status.Build == nil || status.Build.ID != "node@22.11.0" {
		t.Fatalf("status = %+v", status)
	}
	clearStatus, err := c.ClearNodeDefault(context.Background())
	if err != nil {
		t.Fatalf("ClearNodeDefault: %v", err)
	}
	if clearStatus.Configured != "22.11.0" {
		t.Fatalf("clear status = %+v", clearStatus)
	}

	if len(requests) != 2 {
		t.Fatalf("requests = %+v", requests)
	}
	if requests[0].method != http.MethodPut || requests[0].path != "/api/v1/node/default" {
		t.Fatalf("set request = %+v", requests[0])
	}
	if requests[0].body["value"] != "22.11.0" || requests[0].body["force"] != true {
		t.Fatalf("set body = %+v", requests[0].body)
	}
	if requests[1].method != http.MethodPut || requests[1].path != "/api/v1/node/default" {
		t.Fatalf("clear request = %+v", requests[1])
	}
	if requests[1].body["clear"] != true {
		t.Fatalf("clear body = %+v", requests[1].body)
	}
}
