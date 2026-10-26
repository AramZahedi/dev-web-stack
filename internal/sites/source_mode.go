package sites

import (
	"errors"

	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/pathmode"
)

var (
	ErrSiteMustBeStopped     = errors.New("sites: source mode can only change while the site is stopped")
	ErrSourceCopyUnavailable = errors.New("sites: verified source copy is unavailable")
	ErrSourceCopyFailed      = errors.New("sites: verified source copy failed")
	ErrSourceRemovalBlocked  = errors.New("sites: source removal is unavailable until the agent supports a staged finalize operation")
)

// DefaultSourceMode selects the platform-specific source mode for a new site.
func DefaultSourceMode(kind model.SiteKind, platform string) model.SourceMode {
	return pathmode.DefaultSourceMode(kind, platform)
}
