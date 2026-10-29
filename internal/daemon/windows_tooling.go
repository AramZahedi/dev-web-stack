package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
	"github.com/AramZahedi/dev-web-stack/internal/tooling"
)

const windowsNodeShimPrefix = "@echo off\r\nsetlocal DisableDelayedExpansion\r\nwsl.exe -d "

// windowsToolService manages the public Windows .cmd shims for the global Node
// default. It mirrors the helper's managed-tool conflict, backup, and ownership
// rules; only shims written by devstack are replaced or removed.
type windowsToolService struct {
	home   string
	distro string
	store  *store.Store
	now    func() time.Time
}

func newWindowsToolService(home, distro string, st *store.Store) *windowsToolService {
	return &windowsToolService{
		home:   filepath.Clean(strings.TrimSpace(home)),
		distro: strings.TrimSpace(distro),
		store:  st,
		now:    time.Now,
	}
}

// Status reports the shim state for the resolved Node default. A machine with
// no installed Node build is simply not managed; a configured build that is no
// longer installed is reported the same way, without falling back to another
// build.
func (s *windowsToolService) Status(ctx context.Context) (helperproto.ToolStatus, error) {
	if err := ctx.Err(); err != nil {
		return helperproto.ToolStatus{}, err
	}
	build, err := s.resolveStatus(ctx)
	if err != nil {
		return helperproto.ToolStatus{}, err
	}
	return s.status(build), nil
}

// Install writes node.cmd, npm.cmd, and npx.cmd for the resolved Node default.
// A foreign shim is backed up and replaced only when force is true.
func (s *windowsToolService) Install(ctx context.Context, force bool) (helperproto.ToolStatus, error) {
	if err := ctx.Err(); err != nil {
		return helperproto.ToolStatus{}, err
	}
	build, err := s.resolveInstall(ctx)
	if err != nil {
		return helperproto.ToolStatus{}, err
	}
	status := s.status(&build)
	if status.Conflict != "" && !force {
		return helperproto.ToolStatus{}, fmt.Errorf("%w: %s", helperproto.ErrToolConflict, status.Conflict)
	}

	binDir := tooling.WindowsNodeShimDir(config.NewPaths(s.home))
	backup := ""
	if status.Conflict != "" {
		for _, name := range windowsNodeShimNames() {
			shim := filepath.Join(binDir, name+".cmd")
			info, err := os.Lstat(shim)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return helperproto.ToolStatus{}, fmt.Errorf("daemon: inspect Node shim for backup: %w", err)
			}
			if info.Mode().IsRegular() {
				data, err := os.ReadFile(shim)
				if err != nil {
					return helperproto.ToolStatus{}, fmt.Errorf("daemon: read Node shim for backup: %w", err)
				}
				if windowsNodeShimOwned(data) {
					continue
				}
			}
			saved, err := s.backupShim(shim, name)
			if err != nil {
				return helperproto.ToolStatus{}, err
			}
			if backup == "" {
				backup = saved
			}
		}
	}

	if err := tooling.EnsureWindowsNodeShims(binDir, s.distro, build); err != nil {
		return helperproto.ToolStatus{}, err
	}
	installed := s.status(&build)
	installed.Backup = backup
	return installed, nil
}

// Remove deletes the managed Node shims. A foreign shim blocks removal and is
// never deleted.
func (s *windowsToolService) Remove(ctx context.Context) (helperproto.ToolStatus, error) {
	if err := ctx.Err(); err != nil {
		return helperproto.ToolStatus{}, err
	}
	binDir := tooling.WindowsNodeShimDir(config.NewPaths(s.home))
	for _, name := range windowsNodeShimNames() {
		shim := filepath.Join(binDir, name+".cmd")
		info, err := os.Lstat(shim)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return helperproto.ToolStatus{}, fmt.Errorf("daemon: inspect Node shim %s: %w", shim, err)
		}
		if !info.Mode().IsRegular() {
			return helperproto.ToolStatus{}, fmt.Errorf("%w: an unmanaged file occupies %s", helperproto.ErrToolConflict, shim)
		}
		data, err := os.ReadFile(shim)
		if err != nil {
			return helperproto.ToolStatus{}, fmt.Errorf("daemon: read Node shim %s: %w", shim, err)
		}
		if !windowsNodeShimOwned(data) {
			return helperproto.ToolStatus{}, fmt.Errorf("%w: an unmanaged file occupies %s", helperproto.ErrToolConflict, shim)
		}
	}
	for _, name := range windowsNodeShimNames() {
		if err := os.Remove(filepath.Join(binDir, name+".cmd")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return helperproto.ToolStatus{}, fmt.Errorf("daemon: remove Node shim: %w", err)
		}
	}
	return s.status(nil), nil
}

func windowsNodeShimNames() []string {
	return []string{"node", "npm", "npx"}
}

func windowsNodeShimOwned(content []byte) bool {
	return strings.HasPrefix(string(content), windowsNodeShimPrefix)
}

func (s *windowsToolService) status(build *model.RuntimeBuild) helperproto.ToolStatus {
	binDir := tooling.WindowsNodeShimDir(config.NewPaths(s.home))
	status := helperproto.ToolStatus{
		ID:          helperproto.ToolNodeDefault,
		WrapperPath: filepath.Join(binDir, "node.cmd"),
	}
	if build != nil {
		status.Managed = true
		status.Version = build.ID
		status.Target = path.Join(strings.TrimSpace(build.Path), "bin", "node")
	}

	current := build != nil
	root := ""
	if build != nil {
		root = path.Clean(strings.TrimSpace(build.Path))
	}
	for _, name := range windowsNodeShimNames() {
		shim := filepath.Join(binDir, name+".cmd")
		info, err := os.Lstat(shim)
		switch {
		case errors.Is(err, os.ErrNotExist):
			current = false
			continue
		case err != nil:
			status.Conflict = "cannot inspect the Node shim: " + err.Error()
			return status
		case !info.Mode().IsRegular():
			status.Conflict = fmt.Sprintf("an unmanaged file occupies %s", shim)
			return status
		}
		data, err := os.ReadFile(shim)
		if err != nil {
			status.Conflict = "cannot read the Node shim: " + err.Error()
			return status
		}
		if !windowsNodeShimOwned(data) {
			status.Conflict = fmt.Sprintf("an unmanaged file occupies %s", shim)
			return status
		}
		if build != nil && !windowsNodeShimCurrent(data, root, name) {
			current = false
		}
	}
	status.Installed = current
	return status
}

// windowsNodeShimCurrent reports whether a devstack-owned shim executes the
// exact build currently registered as the default.
func windowsNodeShimCurrent(content []byte, root, name string) bool {
	text := string(content)
	binary := path.Join(root, "bin", name)
	return strings.Contains(text, "export PATH="+windowsPosixQuote(path.Dir(binary))+":") &&
		strings.Contains(text, "exec "+windowsPosixQuote(binary)+" ")
}

func windowsPosixQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func (s *windowsToolService) resolveStatus(ctx context.Context) (*model.RuntimeBuild, error) {
	if s.store == nil {
		return nil, errors.New("daemon: no store configured")
	}
	build, err := tooling.ResolveNodeDefault(ctx, s.store)
	if errors.Is(err, tooling.ErrNoNodeInstalled) || errors.Is(err, tooling.ErrNodeDefaultNotInstalled) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("daemon: resolve the Node default: %w", err)
	}
	if err := validateWindowsNodeBuild(build); err != nil {
		return nil, err
	}
	return &build, nil
}

func (s *windowsToolService) resolveInstall(ctx context.Context) (model.RuntimeBuild, error) {
	if s.store == nil {
		return model.RuntimeBuild{}, errors.New("daemon: no store configured")
	}
	build, err := tooling.ResolveNodeDefault(ctx, s.store)
	if err != nil {
		return model.RuntimeBuild{}, fmt.Errorf("daemon: resolve the Node default: %w", err)
	}
	if err := validateWindowsNodeBuild(build); err != nil {
		return model.RuntimeBuild{}, err
	}
	return build, nil
}

// validateWindowsNodeBuild accepts only an exact registered Node build whose
// POSIX path has the <runtimes>/node@<version> shape.
func validateWindowsNodeBuild(build model.RuntimeBuild) error {
	id := strings.TrimSpace(build.ID)
	parsed, err := catalog.ParseID(id)
	if err != nil || parsed.Kind != "node" || id != catalog.FormatID("node", parsed.Version, 0) {
		return fmt.Errorf("daemon: Node default build id %q is not an exact node@<version> id", id)
	}
	if kind := strings.TrimSpace(build.Kind); kind != "" && kind != "node" {
		return fmt.Errorf("daemon: build %q is not a Node build", id)
	}
	if version := strings.TrimSpace(build.Version); version != parsed.Version {
		return fmt.Errorf("daemon: Node default build %q has version %q, want %q", id, version, parsed.Version)
	}
	root := path.Clean(strings.TrimSpace(build.Path))
	if strings.ContainsAny(root, "\x00\r\n") || !path.IsAbs(root) {
		return fmt.Errorf("daemon: Node default build path %q is not an absolute POSIX path", build.Path)
	}
	if path.Base(root) != id || path.Base(path.Dir(root)) != "runtimes" {
		return fmt.Errorf("daemon: Node default build path %q is not <home>/runtimes/%s", build.Path, id)
	}
	return nil
}

func (s *windowsToolService) backupShim(shim, name string) (string, error) {
	dir := filepath.Join(s.home, "backups", "tools")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("daemon: create tool backup directory: %w", err)
	}
	stamp := s.now().UTC().Format("20060102T150405.000000000")
	backup := filepath.Join(dir, helperproto.ToolNodeDefault+"-"+name+"-"+stamp+".cmd")
	info, err := os.Lstat(shim)
	if err != nil {
		return "", fmt.Errorf("daemon: inspect Node shim for backup: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(shim)
		if err != nil {
			return "", fmt.Errorf("daemon: read Node shim link for backup: %w", err)
		}
		if err := os.Symlink(link, backup); err != nil {
			return "", fmt.Errorf("daemon: back up Node shim link: %w", err)
		}
		return backup, nil
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %s is not a regular file or symlink", helperproto.ErrToolConflict, shim)
	}
	data, err := os.ReadFile(shim)
	if err != nil {
		return "", fmt.Errorf("daemon: read Node shim for backup: %w", err)
	}
	out, err := os.OpenFile(backup, os.O_CREATE|os.O_WRONLY|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return "", fmt.Errorf("daemon: create Node shim backup: %w", err)
	}
	if _, err := out.Write(data); err != nil {
		_ = out.Close()
		return "", fmt.Errorf("daemon: write Node shim backup: %w", err)
	}
	if err := out.Close(); err != nil {
		return "", fmt.Errorf("daemon: close Node shim backup: %w", err)
	}
	return backup, nil
}
