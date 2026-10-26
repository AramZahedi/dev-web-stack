package sites

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/pathmode"
)

// SourceCopier is the authenticated agent boundary used for source mode moves.
type SourceCopier interface {
	CopySource(context.Context, agentproto.SourceCopyRequest) (agentproto.SourceCopyResponse, error)
}

// ChangeSourceMode performs a verified agent-side copy before changing the site
// row. Source deletion is intentionally fail-closed until the agent protocol
// can stage the deletion after the row update.
func (s *Service) ChangeSourceMode(ctx context.Context, ref string, mode model.SourceMode, destination string, removeSource bool) (model.Site, error) {
	ctx = nonNilContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.deps.Store == nil {
		return model.Site{}, errors.New("sites: store is not configured")
	}
	if s.deps.SourceCopier == nil {
		return model.Site{}, ErrSourceCopyUnavailable
	}
	site, err := s.resolveLocked(ctx, ref)
	if err != nil {
		return model.Site{}, err
	}
	if site.Status != model.StatusStopped || site.DesiredState != model.DesiredStopped {
		return model.Site{}, ErrSiteMustBeStopped
	}
	if removeSource {
		return model.Site{}, ErrSourceRemovalBlocked
	}
	if mode == site.SourceMode {
		return model.Site{}, fmt.Errorf("sites: source mode is already %q", mode)
	}
	if err := pathmode.ValidateSourceMode(mode, runtime.GOOS); err != nil {
		return model.Site{}, fmt.Errorf("sites: %w", err)
	}
	hostPath, backendPath, err := pathmode.TranslateSourceForDistro(mode, destination, s.deps.Config.WSLDistro)
	if err != nil {
		return model.Site{}, fmt.Errorf("sites: destination: %w", err)
	}
	if err := pathmode.ValidateCreatedPath(hostPath); err != nil {
		return model.Site{}, fmt.Errorf("sites: destination: %w", err)
	}
	source := site.BackendPath
	if source == "" {
		_, source, err = pathmode.TranslateSourceForDistro(site.SourceMode, site.HostPath, s.deps.Config.WSLDistro)
		if err != nil {
			return model.Site{}, fmt.Errorf("sites: source path: %w", err)
		}
	}
	result, err := s.deps.SourceCopier.CopySource(ctx, agentproto.SourceCopyRequest{
		Mode:        string(mode),
		Source:      source,
		Destination: backendPath,
	})
	if err != nil {
		return model.Site{}, fmt.Errorf("%w: %v", ErrSourceCopyFailed, err)
	}
	if err := validateSourceCopyResult(result); err != nil {
		return model.Site{}, fmt.Errorf("%w: %v", ErrSourceCopyFailed, err)
	}

	site.SourceMode = mode
	site.HostPath = hostPath
	site.BackendPath = backendPath
	if err := s.deps.Store.UpdateSite(ctx, site); err != nil {
		return model.Site{}, fmt.Errorf("sites: persist source mode after verified copy: %w", err)
	}
	return site, nil
}

func validateSourceCopyResult(result agentproto.SourceCopyResponse) error {
	if result.Files < 0 || result.Bytes < 0 {
		return errors.New("agent returned negative file or byte counts")
	}
	hash, err := hex.DecodeString(strings.TrimSpace(result.SHA256))
	if err != nil || len(hash) != 32 {
		return errors.New("agent returned an invalid SHA-256 manifest")
	}
	return nil
}
