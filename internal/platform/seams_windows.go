//go:build windows

package platform

import (
	"context"
)

// Windows collectors, builders, and terminal sessions are agent-proxy stubs
// until the WSL agent transport lands.
type linuxCollector struct{}

func (linuxCollector) Collect(context.Context) (any, error) {
	return nil, ErrUnsupported
}

func runShell(context.Context, string, string, map[string]string) (BuildResult, error) {
	return BuildResult{}, ErrUnsupported
}
