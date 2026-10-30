package tooling

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

// SetNodeDefault selects an installed Node build as the machine-wide CLI
// default, writes the exact-build WSL shims, persists the setting, and asks the
// helper to install the system wrappers. It never downloads a build. An empty
// value clears the default, matching the PHP default command.
func (s *Service) SetNodeDefault(ctx context.Context, value string, force bool) (NodeDefaultStatus, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return s.ClearNodeDefault(ctx)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.deps.Store == nil {
		return NodeDefaultStatus{}, errors.New("tooling: no store configured")
	}
	builds, err := installedNodeBuilds(ctx, s.deps.Store)
	if err != nil {
		return NodeDefaultStatus{}, err
	}
	build, ok, err := resolveNodeDefaultSelection(builds, value)
	if err != nil {
		return NodeDefaultStatus{}, err
	}
	if !ok {
		return NodeDefaultStatus{}, fmt.Errorf("%w: %s", ErrNodeDefaultNotInstalled, value)
	}
	if err := s.preflight(ctx, helperproto.ToolNodeDefault, force); err != nil {
		return NodeDefaultStatus{}, err
	}

	previous, hadPrevious, err := nodeDefaultSetting(ctx, s.deps.Store)
	if err != nil {
		return NodeDefaultStatus{}, err
	}
	if err := EnsureNodeShims(s.deps.Paths, build); err != nil {
		return NodeDefaultStatus{}, err
	}
	// Persist before the helper install: the Windows helper resolves the .cmd
	// shim target from the store, while the Linux helper reads the exact-build
	// VERSION file written above. A helper failure rolls the setting back.
	if err := s.deps.Store.SetSetting(ctx, DefaultNodeSettingKey, value); err != nil {
		s.restoreNodeDefaultShims(ctx)
		return NodeDefaultStatus{}, fmt.Errorf("tooling: persist %s: %w", DefaultNodeSettingKey, err)
	}
	if err := s.helperInstall(ctx, helperproto.ToolNodeDefault, force); err != nil {
		s.restoreNodeDefault(ctx, previous, hadPrevious)
		return NodeDefaultStatus{}, err
	}
	return s.NodeDefault(ctx)
}

// ClearNodeDefault removes the persisted default. When any Node build remains
// installed, the documented fallback applies: the newest installed build is
// prepared again and its helper wrappers are installed or repaired. Only when
// no Node build is installed are the managed artifacts and wrappers removed.
func (s *Service) ClearNodeDefault(ctx context.Context) (NodeDefaultStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.deps.Store == nil {
		return NodeDefaultStatus{}, errors.New("tooling: no store configured")
	}
	builds, err := installedNodeBuilds(ctx, s.deps.Store)
	if err != nil {
		return NodeDefaultStatus{}, err
	}
	if len(builds) == 0 {
		if err := s.deps.Store.DeleteSetting(ctx, DefaultNodeSettingKey); err != nil {
			return NodeDefaultStatus{}, fmt.Errorf("tooling: clear %s: %w", DefaultNodeSettingKey, err)
		}
		if _, err := s.helperRemove(ctx, helperproto.ToolNodeDefault); err != nil && !errors.Is(err, helperproto.ErrNotImplemented) {
			return NodeDefaultStatus{}, err
		}
		if err := os.RemoveAll(NodeDefaultToolDir(s.deps.Paths)); err != nil {
			return NodeDefaultStatus{}, fmt.Errorf("tooling: remove Node default artifacts: %w", err)
		}
		return s.NodeDefault(ctx)
	}

	build := builds[0]
	if err := s.preflight(ctx, helperproto.ToolNodeDefault, false); err != nil {
		return NodeDefaultStatus{}, err
	}
	previous, hadPrevious, err := nodeDefaultSetting(ctx, s.deps.Store)
	if err != nil {
		return NodeDefaultStatus{}, err
	}
	if err := s.deps.Store.DeleteSetting(ctx, DefaultNodeSettingKey); err != nil {
		return NodeDefaultStatus{}, fmt.Errorf("tooling: clear %s: %w", DefaultNodeSettingKey, err)
	}
	// The helper's Remove deletes tools/node-default, so the fallback must write
	// the newest build's VERSION and WSL shims again before installing wrappers.
	if err := EnsureNodeShims(s.deps.Paths, build); err != nil {
		s.restoreNodeDefault(ctx, previous, hadPrevious)
		return NodeDefaultStatus{}, err
	}
	if err := s.helperInstall(ctx, helperproto.ToolNodeDefault, false); err != nil {
		s.restoreNodeDefault(ctx, previous, hadPrevious)
		return NodeDefaultStatus{}, err
	}
	return s.NodeDefault(ctx)
}

// installNodeDefaultLocked prepares the current global default and installs
// its system wrappers. It is shared by InstallTool(node-default) and never
// changes the persisted default, so installing a build cannot overwrite it.
func (s *Service) installNodeDefaultLocked(ctx context.Context, force bool) (helperproto.ToolStatus, error) {
	if s.deps.Store == nil {
		return helperproto.ToolStatus{}, errors.New("tooling: no store configured")
	}
	build, err := ResolveNodeDefault(ctx, s.deps.Store)
	if err != nil {
		return helperproto.ToolStatus{}, err
	}
	if err := s.preflight(ctx, helperproto.ToolNodeDefault, force); err != nil {
		return helperproto.ToolStatus{}, err
	}
	if err := EnsureNodeShims(s.deps.Paths, build); err != nil {
		return helperproto.ToolStatus{}, err
	}
	return s.helperInstallStatus(ctx, helperproto.ToolNodeDefault, force)
}

// nodeDefaultSetting returns the persisted value and whether it exists.
func nodeDefaultSetting(ctx context.Context, st *store.Store) (string, bool, error) {
	value, err := st.GetSetting(ctx, DefaultNodeSettingKey)
	if errors.Is(err, store.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("tooling: read %s: %w", DefaultNodeSettingKey, err)
	}
	return value, true, nil
}

// restoreNodeDefault restores the previous setting and prepares its shims after
// a failed mutation. It is best effort: the original failure is returned by
// the caller.
func (s *Service) restoreNodeDefault(ctx context.Context, previous string, hadPrevious bool) {
	if hadPrevious {
		_ = s.deps.Store.SetSetting(ctx, DefaultNodeSettingKey, previous)
	} else {
		_ = s.deps.Store.DeleteSetting(ctx, DefaultNodeSettingKey)
	}
	s.restoreNodeDefaultShims(ctx)
	// Repair helper wrappers that a partially completed install may have
	// repointed. A conflict here leaves the original error authoritative.
	if _, err := ResolveNodeDefault(ctx, s.deps.Store); err == nil {
		_ = s.helperInstall(ctx, helperproto.ToolNodeDefault, false)
	}
}

// restoreNodeDefaultShims rewrites the WSL shims for whichever default the
// store now resolves, or removes the managed directory when no build exists.
func (s *Service) restoreNodeDefaultShims(ctx context.Context) {
	build, err := ResolveNodeDefault(ctx, s.deps.Store)
	switch {
	case err == nil:
		_ = EnsureNodeShims(s.deps.Paths, build)
	case errors.Is(err, ErrNoNodeInstalled):
		_ = os.RemoveAll(NodeDefaultToolDir(s.deps.Paths))
	}
}
