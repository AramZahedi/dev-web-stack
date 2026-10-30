package client

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/nodereleases"
)

// NodeReleases returns discovered Node releases. An empty major keeps every
// major; all expands older patches and offline forbids network requests.
func (c *Client) NodeReleases(ctx context.Context, major string, all, offline bool) (nodereleases.Snapshot, error) {
	query := url.Values{}
	if major = strings.TrimSpace(major); major != "" {
		query.Set("major", major)
	}
	if all {
		query.Set("all", "true")
	}
	if offline {
		query.Set("offline", "true")
	}
	path := "/api/v1/runtimes/node/releases"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}

	var snapshot nodereleases.Snapshot
	err := c.doJSON(ctx, http.MethodGet, path, nil, &snapshot)
	return snapshot, err
}

// NodeRelease returns the fully pinned preflight metadata for one selector.
// The daemon resolves major, major.minor, exact versions, and node@ IDs.
func (c *Client) NodeRelease(ctx context.Context, version string) (nodereleases.Preflight, error) {
	selector := url.PathEscape(strings.TrimSpace(version))
	var preflight nodereleases.Preflight
	err := c.doJSON(ctx, http.MethodGet,
		"/api/v1/runtimes/node/releases/"+selector, nil, &preflight)
	return preflight, err
}

// RefreshNodeReleases forces one release index refresh and returns the new
// default newest-per-major snapshot.
func (c *Client) RefreshNodeReleases(ctx context.Context) (nodereleases.Snapshot, error) {
	var snapshot nodereleases.Snapshot
	err := c.doJSON(ctx, http.MethodPost, "/api/v1/runtimes/node/refresh", nil, &snapshot)
	return snapshot, err
}
