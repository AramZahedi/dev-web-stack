//go:build !windows

package daemon

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

const defaultToolBinDir = "/usr/local/bin"

type toolStatusProvider interface {
	ToolStatus(ctx context.Context) (helperproto.ToolStatusResponse, error)
}

// resolveToolBinDir derives the system bin directory from the helper's managed
// PHP wrapper. An unavailable helper keeps the documented /usr/local/bin
// default rather than failing daemon startup.
func resolveToolBinDir(ctx context.Context, provider toolStatusProvider) string {
	if provider == nil {
		return defaultToolBinDir
	}
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	status, err := provider.ToolStatus(checkCtx)
	if err != nil {
		return defaultToolBinDir
	}
	for _, tool := range status.Tools {
		if tool.ID != helperproto.ToolPHPDefault {
			continue
		}
		if wrapper := strings.TrimSpace(tool.WrapperPath); wrapper != "" {
			return filepath.Dir(wrapper)
		}
	}
	return defaultToolBinDir
}
