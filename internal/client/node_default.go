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
