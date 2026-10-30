package tooling

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

// ErrHelperUnavailable reports that the privileged helper is not configured.
var ErrHelperUnavailable = errors.New("tooling: the privileged helper is not available")

// Helper is the privileged tool-installation surface.
type Helper interface {
	ToolStatus(ctx context.Context) (helperproto.ToolStatusResponse, error)
	ToolInstall(ctx context.Context, id string, force bool) (helperproto.ToolStatus, error)
	ToolRemove(ctx context.Context, id string) (helperproto.ToolStatus, error)
}

// Deps configures the tooling service.
type Deps struct {
	Store          *store.Store
	Paths          config.Paths
	WSLDistro      string
	Helper         Helper
	HTTPClient     *http.Client
	ComposerSource *composerSource
	// ProvisionComposer is the WSL deployment seam. Tests inject a no-op so a
	// temporary Composer fixture can never overwrite /data/devstack.
	ProvisionComposer func(context.Context, string, composerSource, string) error
}

// Service owns the machine-wide PHP CLI default, global ini, Composer PHAR,
// and the system shim installation flow.
type Service struct {
	deps Deps
	mu   sync.Mutex
}

// NewService returns a tooling service.
func NewService(deps Deps) *Service {
	if deps.HTTPClient == nil {
		deps.HTTPClient = http.DefaultClient
	}
	return &Service{deps: deps}
}

// PHPDefaultStatus is the machine-wide CLI default state.
type PHPDefaultStatus struct {
	Configured    string              `json:"configured,omitempty"`
	Build         *model.RuntimeBuild `json:"build,omitempty"`
	GlobalIni     string              `json:"global_ini"`
	ShimDir       string              `json:"shim_dir"`
	ShimInstalled bool                `json:"shim_installed"`
}

// GlobalIni is the editable machine-wide CLI php.ini.
type GlobalIni struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	BuildID string `json:"build_id,omitempty"`
}

// PHPDefault returns the current default and local shim state without
// contacting the helper.
func (s *Service) PHPDefault(ctx context.Context) (PHPDefaultStatus, error) {
	if s.deps.Store == nil {
		return PHPDefaultStatus{}, errors.New("tooling: no store configured")
	}
	status := PHPDefaultStatus{
		GlobalIni: GlobalIniPath(s.deps.Paths),
		ShimDir:   ShimDir(s.deps.Paths),
	}
	if value, err := s.deps.Store.GetSetting(ctx, DefaultPHPSettingKey); err == nil {
		status.Configured = strings.TrimSpace(value)
	} else if !errors.Is(err, store.ErrNotFound) {
		return PHPDefaultStatus{}, fmt.Errorf("tooling: read %s: %w", DefaultPHPSettingKey, err)
	}
	build, err := ResolveDefault(ctx, s.deps.Store)
	switch {
	case err == nil:
		status.Build = &build
	case errors.Is(err, ErrNoPHPInstalled), errors.Is(err, ErrPHPDefaultNotInstalled):
	default:
		return PHPDefaultStatus{}, err
	}
	shimCheckPath := filepath.Join(ShimDir(s.deps.Paths), "php")
	if runtime.GOOS == "windows" {
		shimCheckPath = filepath.Join(s.deps.Paths.Home, "bin", "php.cmd")
	}
	if info, err := os.Stat(shimCheckPath); err == nil && info.Mode().IsRegular() {
		status.ShimInstalled = true
	}
	return status, nil
}

// SetPHPDefault resolves value, prepares the global ini and shims, installs
// the system link through the helper, and only then persists the setting.
func (s *Service) SetPHPDefault(ctx context.Context, value string, force bool) (PHPDefaultStatus, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return s.ClearPHPDefault(ctx)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.deps.Store == nil {
		return PHPDefaultStatus{}, errors.New("tooling: no store configured")
	}
	builds, err := installedPHPBuilds(ctx, s.deps.Store)
	if err != nil {
		return PHPDefaultStatus{}, err
	}
	build, ok, err := resolvePHPSelection(builds, value)
	if err != nil {
		return PHPDefaultStatus{}, err
	}
	if !ok {
		return PHPDefaultStatus{}, fmt.Errorf("%w: %s", ErrPHPDefaultNotInstalled, value)
	}
	if err := s.preflight(ctx, helperproto.ToolPHPDefault, force); err != nil {
		return PHPDefaultStatus{}, err
	}
	if err := EnsureGlobalIni(build, GlobalIniPath(s.deps.Paths)); err != nil {
		return PHPDefaultStatus{}, err
	}
	if _, err := EnsurePHPShims(build, s.deps.Paths, GlobalIniPath(s.deps.Paths)); err != nil {
		return PHPDefaultStatus{}, err
	}
	if err := s.helperInstall(ctx, helperproto.ToolPHPDefault, force); err != nil {
		return PHPDefaultStatus{}, err
	}
	if err := s.ensureWindowsToolShims(build); err != nil {
		return PHPDefaultStatus{}, err
	}
	if err := s.deps.Store.SetSetting(ctx, DefaultPHPSettingKey, value); err != nil {
		_, _ = s.helperRemove(ctx, helperproto.ToolPHPDefault)
		return PHPDefaultStatus{}, fmt.Errorf("tooling: persist %s: %w", DefaultPHPSettingKey, err)
	}
	return s.PHPDefault(ctx)
}

// ClearPHPDefault removes the persisted default while preserving the
// documented fallback: when any PHP build remains installed, the global shim
// is repointed at the newest installed build rather than system PHP. Only when
// no PHP build is installed is the managed shim removed.
func (s *Service) ClearPHPDefault(ctx context.Context) (PHPDefaultStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deps.Store == nil {
		return PHPDefaultStatus{}, errors.New("tooling: no store configured")
	}
	builds, err := installedPHPBuilds(ctx, s.deps.Store)
	if err != nil {
		return PHPDefaultStatus{}, err
	}
	if len(builds) == 0 {
		if err := s.deps.Store.DeleteSetting(ctx, DefaultPHPSettingKey); err != nil {
			return PHPDefaultStatus{}, fmt.Errorf("tooling: clear %s: %w", DefaultPHPSettingKey, err)
		}
		if _, err := s.helperRemove(ctx, helperproto.ToolPHPDefault); err != nil && !errors.Is(err, helperproto.ErrNotImplemented) {
			return PHPDefaultStatus{}, err
		}
		if runtime.GOOS == "windows" {
			_ = os.Remove(filepath.Join(s.deps.Paths.Home, "bin", "php.cmd"))
		}
		return s.PHPDefault(ctx)
	}

	build := builds[0]
	if err := s.preflight(ctx, helperproto.ToolPHPDefault, false); err != nil {
		return PHPDefaultStatus{}, err
	}
	if err := EnsureGlobalIni(build, GlobalIniPath(s.deps.Paths)); err != nil {
		return PHPDefaultStatus{}, err
	}
	if _, err := EnsurePHPShims(build, s.deps.Paths, GlobalIniPath(s.deps.Paths)); err != nil {
		return PHPDefaultStatus{}, err
	}
	if err := s.helperInstall(ctx, helperproto.ToolPHPDefault, false); err != nil {
		return PHPDefaultStatus{}, err
	}
	if err := s.ensureWindowsToolShims(build); err != nil {
		return PHPDefaultStatus{}, err
	}
	if err := s.deps.Store.DeleteSetting(ctx, DefaultPHPSettingKey); err != nil {
		return PHPDefaultStatus{}, fmt.Errorf("tooling: clear %s: %w", DefaultPHPSettingKey, err)
	}
	return s.PHPDefault(ctx)
}

// GlobalIni returns the current global ini.
func (s *Service) GlobalIni(ctx context.Context) (GlobalIni, error) {
	build, err := ResolveDefault(ctx, s.deps.Store)
	if err != nil {
		return GlobalIni{}, err
	}
	path := GlobalIniPath(s.deps.Paths)
	data, err := ReadGlobalIni(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := EnsureGlobalIni(build, path); err != nil {
			return GlobalIni{}, err
		}
		data, err = ReadGlobalIni(path)
	}
	if err != nil {
		return GlobalIni{}, err
	}
	return GlobalIni{Path: path, Content: string(data), BuildID: build.ID}, nil
}

// SaveGlobalIni validates and replaces the global ini. The Windows CLI shims
// are refreshed first so a wrapper created before the ini wiring existed starts
// loading the file without requiring a PHP-default change.
func (s *Service) SaveGlobalIni(ctx context.Context, content string) (GlobalIni, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	build, err := ResolveDefault(ctx, s.deps.Store)
	if err != nil {
		return GlobalIni{}, err
	}
	if err := s.ensureWindowsToolShims(build); err != nil {
		return GlobalIni{}, err
	}
	path := GlobalIniPath(s.deps.Paths)
	if err := WriteGlobalIni(build, path, []byte(content)); err != nil {
		return GlobalIni{}, err
	}
	return GlobalIni{Path: path, Content: content, BuildID: build.ID}, nil
}

// Tools returns the helper's managed tool status.
func (s *Service) Tools(ctx context.Context) ([]helperproto.ToolStatus, error) {
	if s.deps.Helper == nil {
		return nil, ErrHelperUnavailable
	}
	status, err := s.deps.Helper.ToolStatus(ctx)
	if err != nil {
		return nil, err
	}
	if status.Tools == nil {
		status.Tools = []helperproto.ToolStatus{}
	}
	return status.Tools, nil
}

// InstallTool prepares one managed tool and installs its system link.
func (s *Service) InstallTool(ctx context.Context, id string, force bool) (helperproto.ToolStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch id {
	case helperproto.ToolPHPDefault:
		return s.setPHPDefaultLocked(ctx, false, force)
	case helperproto.ToolNodeDefault:
		return s.installNodeDefaultLocked(ctx, force)
	case helperproto.ToolComposer:
		if err := s.preflight(ctx, id, force); err != nil {
			return helperproto.ToolStatus{}, err
		}
		if err := s.ensureGlobalPHP(ctx); err != nil {
			return helperproto.ToolStatus{}, err
		}
		phpBuild, err := ResolveDefault(ctx, s.deps.Store)
		if err != nil {
			return helperproto.ToolStatus{}, err
		}
		source := defaultComposerSource()
		if s.deps.ComposerSource != nil {
			source = *s.deps.ComposerSource
		}
		if _, err := installComposer(ctx, s.deps.HTTPClient, s.deps.Paths, source); err != nil {
			return helperproto.ToolStatus{}, err
		}
		if runtime.GOOS == "windows" {
			provision := s.deps.ProvisionComposer
			if provision == nil {
				provision = ProvisionWSLComposer
			}
			_ = provision(ctx, s.deps.WSLDistro, source, ComposerPHARPath(s.deps.Paths))
		}
		status, err := s.helperInstallStatus(ctx, id, force)
		if err != nil {
			return helperproto.ToolStatus{}, err
		}
		if err := s.ensureWindowsToolShims(phpBuild); err != nil {
			return helperproto.ToolStatus{}, err
		}
		return status, nil
	default:
		return helperproto.ToolStatus{}, fmt.Errorf("%w: %q", ErrUnsupportedTool, id)
	}
}

// setPHPDefaultLocked prepares the default shims without changing the selected
// value. It is shared by InstallTool(php-default).
func (s *Service) setPHPDefaultLocked(ctx context.Context, _ bool, force bool) (helperproto.ToolStatus, error) {
	build, err := ResolveDefault(ctx, s.deps.Store)
	if err != nil {
		return helperproto.ToolStatus{}, err
	}
	if err := s.preflight(ctx, helperproto.ToolPHPDefault, force); err != nil {
		return helperproto.ToolStatus{}, err
	}
	if err := EnsureGlobalIni(build, GlobalIniPath(s.deps.Paths)); err != nil {
		return helperproto.ToolStatus{}, err
	}
	if _, err := EnsurePHPShims(build, s.deps.Paths, GlobalIniPath(s.deps.Paths)); err != nil {
		return helperproto.ToolStatus{}, err
	}
	status, err := s.helperInstallStatus(ctx, helperproto.ToolPHPDefault, force)
	if err != nil {
		return helperproto.ToolStatus{}, err
	}
	if err := s.ensureWindowsToolShims(build); err != nil {
		return helperproto.ToolStatus{}, err
	}
	return status, nil
}

// RemoveTool removes a managed system link and its managed artifacts.
func (s *Service) RemoveTool(ctx context.Context, id string) (helperproto.ToolStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch id {
	case helperproto.ToolPHPDefault, helperproto.ToolComposer, helperproto.ToolNodeDefault:
	default:
		return helperproto.ToolStatus{}, fmt.Errorf("%w: %q", ErrUnsupportedTool, id)
	}
	status, err := s.helperRemove(ctx, id)
	if err != nil && !errors.Is(err, helperproto.ErrNotImplemented) {
		return helperproto.ToolStatus{}, err
	}
	if id == helperproto.ToolNodeDefault {
		// The Linux helper removes the managed directory itself; the Windows
		// helper only owns the .cmd shims, so clean the WSL shims and VERSION
		// here for both platforms.
		if err := os.RemoveAll(NodeDefaultToolDir(s.deps.Paths)); err != nil {
			return helperproto.ToolStatus{}, fmt.Errorf("tooling: remove Node default artifacts: %w", err)
		}
	}
	return status, nil
}

// ensureGlobalPHP prepares the newest installed PHP build's global shims when
// no default has been selected. This matches the documented Composer fallback
// without silently downloading a runtime.
func (s *Service) ensureGlobalPHP(ctx context.Context) error {
	if info, err := os.Stat(filepath.Join(ShimDir(s.deps.Paths), "php")); err == nil && info.Mode().IsRegular() {
		return nil
	}
	builds, err := installedPHPBuilds(ctx, s.deps.Store)
	if err != nil {
		return err
	}
	if len(builds) == 0 {
		return nil
	}
	build := builds[0]
	if err := EnsureGlobalIni(build, GlobalIniPath(s.deps.Paths)); err != nil {
		return err
	}
	if _, err := EnsurePHPShims(build, s.deps.Paths, GlobalIniPath(s.deps.Paths)); err != nil {
		return err
	}
	return s.ensureWindowsToolShims(build)
}

func (s *Service) ensureWindowsToolShims(build model.RuntimeBuild) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	if !strings.HasPrefix(strings.TrimSpace(build.Path), "/") {
		// Local test/development builds are not WSL paths; keep the legacy
		// ambient wrapper instead of emitting an unusable absolute path.
		return EnsureGlobalWindowsToolShims(s.deps.Paths.Home, s.deps.WSLDistro)
	}
	return EnsureGlobalWindowsToolShimsForBuild(s.deps.Paths.Home, s.deps.WSLDistro, build.Path)
}

// preflight rejects a foreign system wrapper before any artifact is written.
func (s *Service) preflight(ctx context.Context, id string, force bool) error {
	if s.deps.Helper == nil {
		return ErrHelperUnavailable
	}
	if force {
		return nil
	}
	statuses, err := s.deps.Helper.ToolStatus(ctx)
	if err != nil {
		return err
	}
	for _, status := range statuses.Tools {
		if status.ID == id && status.Conflict != "" {
			return fmt.Errorf("%w: %s: %s (rerun with --force to back it up)", helperproto.ErrToolConflict, status.WrapperPath, status.Conflict)
		}
	}
	return nil
}

func (s *Service) helperInstall(ctx context.Context, id string, force bool) error {
	if s.deps.Helper == nil {
		return ErrHelperUnavailable
	}
	_, err := s.deps.Helper.ToolInstall(ctx, id, force)
	return err
}

func (s *Service) helperInstallStatus(ctx context.Context, id string, force bool) (helperproto.ToolStatus, error) {
	if s.deps.Helper == nil {
		return helperproto.ToolStatus{}, ErrHelperUnavailable
	}
	return s.deps.Helper.ToolInstall(ctx, id, force)
}

func (s *Service) helperRemove(ctx context.Context, id string) (helperproto.ToolStatus, error) {
	if s.deps.Helper == nil {
		return helperproto.ToolStatus{}, ErrHelperUnavailable
	}
	return s.deps.Helper.ToolRemove(ctx, id)
}
