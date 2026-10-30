package client

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/nodereleases"
)

// nodeReleaseTestClient routes the client's canonical daemon URL to a local
// httptest server without a real socket or any network access.
func nodeReleaseTestClient(t *testing.T, handler http.Handler) *Client {
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

func nodeReleaseClientSnapshot() nodereleases.Snapshot {
	release := nodereleases.Release{
		Version:  "22.11.0",
		Date:     "2026-10-01",
		LTS:      "Jod",
		Security: true,
		NPM:      "10.9.0",
		Modules:  "137",
		File:     "node-v22.11.0-linux-x64.tar.gz",
	}
	return nodereleases.Snapshot{
		Majors:   []nodereleases.Major{{Major: 22, Latest: release, Releases: []nodereleases.Release{release}}},
		All:      []nodereleases.Release{release},
		Source:   nodereleases.SourceCache,
		CachedAt: time.Date(2026, 10, 30, 6, 0, 0, 0, time.UTC),
		Stale:    true,
		Warning:  "offline: cached node release index is older than 6h0m0s",
	}
}

func TestNodeReleaseClientListOptionsAndRefresh(t *testing.T) {
	type request struct {
		method string
		path   string
		query  url.Values
	}
	var requests []request
	snapshot := nodeReleaseClientSnapshot()
	c := nodeReleaseTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, request{method: r.Method, path: r.URL.Path, query: r.URL.Query()})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snapshot)
	}))

	defaults, err := c.NodeReleases(context.Background(), "", false, false)
	if err != nil {
		t.Fatalf("NodeReleases defaults: %v", err)
	}
	if defaults.Source != nodereleases.SourceCache || !defaults.Stale ||
		len(defaults.Majors) != 1 || defaults.Majors[0].Latest.Version != "22.11.0" {
		t.Fatalf("defaults = %+v", defaults)
	}

	filtered, err := c.NodeReleases(context.Background(), "22", true, true)
	if err != nil {
		t.Fatalf("NodeReleases filtered: %v", err)
	}
	if filtered.Warning != snapshot.Warning {
		t.Fatalf("warning = %q", filtered.Warning)
	}

	refreshed, err := c.RefreshNodeReleases(context.Background())
	if err != nil {
		t.Fatalf("RefreshNodeReleases: %v", err)
	}
	if refreshed.Source != nodereleases.SourceCache {
		t.Fatalf("refreshed = %+v", refreshed)
	}

	if len(requests) != 3 {
		t.Fatalf("requests = %+v", requests)
	}
	if requests[0].method != http.MethodGet ||
		requests[0].path != "/api/v1/runtimes/node/releases" ||
		len(requests[0].query) != 0 {
		t.Fatalf("default request = %+v", requests[0])
	}
	filteredQuery := requests[1].query
	if requests[1].method != http.MethodGet ||
		requests[1].path != "/api/v1/runtimes/node/releases" ||
		filteredQuery.Get("major") != "22" ||
		filteredQuery.Get("all") != "true" ||
		filteredQuery.Get("offline") != "true" {
		t.Fatalf("filtered request = %+v", requests[1])
	}
	if requests[2].method != http.MethodPost ||
		requests[2].path != "/api/v1/runtimes/node/refresh" ||
		len(requests[2].query) != 0 {
		t.Fatalf("refresh request = %+v", requests[2])
	}
}

func TestNodeReleaseClientPreflightPinnedMetadata(t *testing.T) {
	checksum := strings.Repeat("b", 64)
	preflight := nodereleases.Preflight{
		Entry: catalog.Entry{
			ID:      "node@24.9.0",
			Kind:    "node",
			Version: "24.9.0",
			Build:   "linux-x64",
			URL:     "https://nodejs.org/dist/v24.9.0/node-v24.9.0-linux-x64.tar.gz",
			SHA256:  checksum,
			Size:    29_876_543,
		},
		Release: nodereleases.Release{
			Version: "24.9.0",
			NPM:     "11.0.0",
			Modules: "137",
			File:    "node-v24.9.0-linux-x64.tar.gz",
		},
	}

	var path string
	c := nodeReleaseTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(preflight)
	}))

	got, err := c.NodeRelease(context.Background(), "24.9.0")
	if err != nil {
		t.Fatalf("NodeRelease: %v", err)
	}
	if path != "/api/v1/runtimes/node/releases/24.9.0" {
		t.Fatalf("path = %q", path)
	}
	if got.Entry.SHA256 != checksum || got.Entry.Size != 29_876_543 {
		t.Fatalf("entry metadata = %s/%d", got.Entry.SHA256, got.Entry.Size)
	}
	if got.Entry.ID != "node@24.9.0" || got.Entry.Build != "linux-x64" {
		t.Fatalf("entry = %+v", got.Entry)
	}
	if got.Release.Version != "24.9.0" || got.Release.NPM != "11.0.0" {
		t.Fatalf("release = %+v", got.Release)
	}
}

func TestNodeReleaseClientTypedErrors(t *testing.T) {
	c := nodeReleaseTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(Error{
			Code:    "NOT_FOUND",
			Message: "node release is not available",
		})
	}))

	_, err := c.NodeRelease(context.Background(), "99.0.0")
	if err == nil {
		t.Fatal("NodeRelease returned no error")
	}
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T, want *client.Error", err)
	}
	if apiErr.Code != "NOT_FOUND" || apiErr.Message != "node release is not available" {
		t.Fatalf("error = %+v", apiErr)
	}
}
