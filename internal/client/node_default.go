package client

import (
	"context"
	"net/http"

	"github.com/AramZahedi/dev-web-stack/internal/tooling"
)

// NodeDefault returns the machine-wide Node CLI default.
func (c *Client) NodeDefault(ctx context.Context) (tooling.NodeDefaultStatus, error) {
	var status tooling.NodeDefaultStatus
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/node/default", nil, &status)
	return status, err
}

// SetNodeDefault sets the machine-wide Node CLI default.
func (c *Client) SetNodeDefault(ctx context.Context, value string, force bool) (tooling.NodeDefaultStatus, error) {
	var status tooling.NodeDefaultStatus
	err := c.doJSON(ctx, http.MethodPut, "/api/v1/node/default", map[string]any{
		"value": value,
		"force": force,
	}, &status)
	return status, err
}

// ClearNodeDefault removes the machine-wide Node CLI default.
func (c *Client) ClearNodeDefault(ctx context.Context) (tooling.NodeDefaultStatus, error) {
	var status tooling.NodeDefaultStatus
	err := c.doJSON(ctx, http.MethodPut, "/api/v1/node/default", map[string]any{"clear": true}, &status)
	return status, err
}
