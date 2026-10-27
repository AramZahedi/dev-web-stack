//go:build !windows

package helper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/tooling"
)

// ToolManager installs fixed, catalog-free native tool wrappers into the
// system bin directory. Requests name a fixed tool id only; every path and
// command is derived from the helper's configured devstack root.
type ToolManager struct {
	home     string
	binDir   string
	allowUID int
	logger   *slog.Logger
	now      func() time.Time
	mu       sync.Mutex
}

// NewToolManager returns a manager rooted at a devstack home and a system bin
// directory. A missing bin directory is created on first install.
func NewToolManager(home, binDir string, allowUID int, logger *slog.Logger) (*ToolManager, error) {
	home = strings.TrimSpace(home)
	if home == "" {
		return nil, errors.New("helper: tool root is empty")
	}
	if !filepath.IsAbs(home) {
		return nil, fmt.Errorf("helper: tool root %q must be absolute", home)
	}
	if strings.TrimSpace(binDir) == "" {
		binDir = "/usr/local/bin"
	}
	if !filepath.IsAbs(binDir) {
		return nil, fmt.Errorf("helper: tool bin directory %q must be absolute", binDir)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ToolManager{
		home:     filepath.Clean(home),
		binDir:   filepath.Clean(binDir),
		allowUID: allowUID,
		logger:   logger,
		now:      time.Now,
	}, nil
}

// Status reports both managed tools.
func (m *ToolManager) Status(ctx context.Context) (helperproto.ToolStatusResponse, error) {
	if err := ctx.Err(); err != nil {
		return helperproto.ToolStatusResponse{}, err
	}
	return helperproto.ToolStatusResponse{Tools: []helperproto.ToolStatus{
		m.status(helperproto.ToolPHPDefault),
		m.status(helperproto.ToolComposer),
	}}, nil
}

func (m *ToolManager) status(id string) helperproto.ToolStatus {
	target, version := m.target(id)
	status := helperproto.ToolStatus{
		ID:          id,
		WrapperPath: m.wrapperPath(id),
		Target:      target,
	}
	if info, err := os.Lstat(target); err == nil && info.Mode().IsRegular() {
		status.Managed = true
		status.Version = version
	}
	primaryOK := false
	for _, wrapper := range m.wrapperPaths(id) {
		wrapperTarget := m.targetForWrapper(id, wrapper)
		if _, err := os.Lstat(wrapperTarget); errors.Is(err, os.ErrNotExist) {
			continue
		}
		info, err := os.Lstat(wrapper)
		switch {
		case errors.Is(err, os.ErrNotExist):
			continue
		case err != nil:
			status.Conflict = "cannot inspect the system wrapper: " + err.Error()
			return status
		case info.Mode()&os.ModeSymlink != 0:
			link, readErr := os.Readlink(wrapper)
			if readErr != nil {
				status.Conflict = "cannot read the system wrapper: " + readErr.Error()
				return status
			}
			if link != wrapperTarget {
				status.Conflict = fmt.Sprintf("%s points to %s", wrapper, link)
				return status
			}
			if wrapper == status.WrapperPath {
				primaryOK = true
			}
		default:
			status.Conflict = fmt.Sprintf("an unmanaged file occupies %s", wrapper)
			return status
		}
	}
	status.Installed = status.Managed && primaryOK
	if status.Installed {
		return status
	}
	if status.Managed {
		// Managed artifacts exist but the primary link was never installed.
		return status
	}
	return status
}

func (m *ToolManager) target(id string) (target, version string) {
	paths := config.NewPaths(m.home)
	switch id {
	case helperproto.ToolPHPDefault:
		return filepath.Join(tooling.ShimDir(paths), "php"), readFirstLine(filepath.Join(paths.Tools, "php-default", "VERSION"))
	case helperproto.ToolComposer:
		version := readFirstLine(filepath.Join(paths.Tools, "composer", "VERSION"))
		if version == "" {
			version = tooling.ComposerVersion
		}
		return tooling.ComposerWrapperPath(paths), version
	default:
		return "", ""
	}
}

func (m *ToolManager) wrapperPath(id string) string {
	paths := m.wrapperPaths(id)
	if len(paths) == 0 {
		return ""
	}
	return paths[0]
}

func (m *ToolManager) wrapperPaths(id string) []string {
	switch id {
	case helperproto.ToolPHPDefault:
		return []string{
			filepath.Join(m.binDir, "php"),
			filepath.Join(m.binDir, "php-config"),
			filepath.Join(m.binDir, "phpize"),
		}
	case helperproto.ToolComposer:
		return []string{filepath.Join(m.binDir, "composer")}
	default:
		return nil
	}
}

func (m *ToolManager) targetForWrapper(id, wrapper string) string {
	paths := config.NewPaths(m.home)
	switch id {
	case helperproto.ToolPHPDefault:
		return filepath.Join(tooling.ShimDir(paths), filepath.Base(wrapper))
	case helperproto.ToolComposer:
		return tooling.ComposerWrapperPath(paths)
	default:
		return ""
	}
}

func readFirstLine(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
}

// Install links one managed tool into the system bin directory. A foreign
// wrapper is backed up and replaced only when force is true.
func (m *ToolManager) Install(ctx context.Context, req helperproto.ToolInstallRequest) (helperproto.ToolStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return helperproto.ToolStatus{}, err
	}
	if err := validateToolID(req.ID); err != nil {
		return helperproto.ToolStatus{}, err
	}
	status := m.status(req.ID)
	if !status.Managed {
		return helperproto.ToolStatus{}, fmt.Errorf("%w: %s is not prepared; install it with devstack first", helperproto.ErrInvalidRequest, req.ID)
	}
	if status.Installed && status.Conflict == "" {
		return status, nil
	}
	if status.Conflict != "" && !req.Force {
		return helperproto.ToolStatus{}, fmt.Errorf("%w: %s", helperproto.ErrToolConflict, status.Conflict)
	}
	if err := os.MkdirAll(m.binDir, 0o755); err != nil {
		return helperproto.ToolStatus{}, fmt.Errorf("helper: create tool bin directory: %w", err)
	}

	for _, wrapper := range m.wrapperPaths(req.ID) {
		target := m.targetForWrapper(req.ID, wrapper)
		if info, err := os.Lstat(target); err != nil || !info.Mode().IsRegular() {
			continue
		}
		if link, err := os.Readlink(wrapper); err == nil && link == target {
			continue
		}
		if _, err := os.Lstat(wrapper); err == nil {
			backup, err := m.backup(wrapper, req.ID)
			if err != nil {
				return helperproto.ToolStatus{}, err
			}
			if status.Backup == "" {
				status.Backup = backup
			}
		}
		if err := installSymlink(target, wrapper); err != nil {
			return helperproto.ToolStatus{}, err
		}
	}
	m.logger.Info("helper: installed managed tool", "id", req.ID, "wrapper", status.WrapperPath, "target", status.Target)
	installed := m.status(req.ID)
	installed.Backup = status.Backup
	return installed, nil
}

func installSymlink(target, wrapper string) error {
	temp, err := os.CreateTemp(filepath.Dir(wrapper), "devstack-tool-*.tmp")
	if err != nil {
		return fmt.Errorf("helper: create temporary tool link: %w", err)
	}
	tempName := temp.Name()
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempName)
		return fmt.Errorf("helper: close temporary tool link: %w", err)
	}
	if err := os.Remove(tempName); err != nil {
		return fmt.Errorf("helper: remove temporary tool placeholder: %w", err)
	}
	if err := os.Symlink(target, tempName); err != nil {
		_ = os.Remove(tempName)
		return fmt.Errorf("helper: create tool link: %w", err)
	}
	if err := os.Rename(tempName, wrapper); err != nil {
		_ = os.Remove(tempName)
		return fmt.Errorf("helper: install tool link: %w", err)
	}
	return nil
}

// backup copies a foreign wrapper into <home>/backups/tools. Symlinks are
// preserved rather than followed.
func (m *ToolManager) backup(path, id string) (string, error) {
	dir := filepath.Join(m.home, "backups", "tools")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("helper: create tool backup directory: %w", err)
	}
	stamp := m.now().UTC().Format("20060102T150405.000000000")
	backup := filepath.Join(dir, id+"-"+stamp)
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("helper: inspect tool wrapper for backup: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(path)
		if err != nil {
			return "", fmt.Errorf("helper: read tool wrapper for backup: %w", err)
		}
		if err := os.Symlink(link, backup); err != nil {
			return "", fmt.Errorf("helper: back up tool symlink: %w", err)
		}
		return backup, nil
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %s is not a regular file or symlink", helperproto.ErrToolConflict, path)
	}
	source, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("helper: open tool wrapper for backup: %w", err)
	}
	defer func() { _ = source.Close() }()
	out, err := os.OpenFile(backup, os.O_CREATE|os.O_WRONLY|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return "", fmt.Errorf("helper: create tool backup: %w", err)
	}
	_, copyErr := io.Copy(out, source)
	closeErr := out.Close()
	if copyErr != nil {
		return "", fmt.Errorf("helper: copy tool backup: %w", copyErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("helper: close tool backup: %w", closeErr)
	}
	if err := os.Chmod(backup, info.Mode().Perm()); err != nil {
		return "", fmt.Errorf("helper: set tool backup mode: %w", err)
	}
	return backup, nil
}

// Remove removes one managed system wrapper and its managed artifacts. A
// foreign wrapper is never deleted.
func (m *ToolManager) Remove(ctx context.Context, id string) (helperproto.ToolStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return helperproto.ToolStatus{}, err
	}
	if err := validateToolID(id); err != nil {
		return helperproto.ToolStatus{}, err
	}
	status := m.status(id)
	if status.Conflict != "" {
		return helperproto.ToolStatus{}, fmt.Errorf("%w: %s: %s", helperproto.ErrToolConflict, status.WrapperPath, status.Conflict)
	}
	// Remove the home artifacts first. If this fails, leave the system links in
	// place so the tool is still usable (and the failure is recoverable).
	paths := config.NewPaths(m.home)
	switch id {
	case helperproto.ToolPHPDefault:
		if err := os.RemoveAll(filepath.Join(paths.Tools, "php-default")); err != nil {
			return helperproto.ToolStatus{}, fmt.Errorf("helper: remove php-default shims: %w", err)
		}
	case helperproto.ToolComposer:
		for _, path := range []string{
			tooling.ComposerWrapperPath(paths),
			tooling.ComposerPHARPath(paths),
			filepath.Join(paths.Tools, "composer", "VERSION"),
		} {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return helperproto.ToolStatus{}, fmt.Errorf("helper: remove composer artifact: %w", err)
			}
		}
	}
	for _, wrapper := range m.wrapperPaths(id) {
		target := m.targetForWrapper(id, wrapper)
		link, err := os.Readlink(wrapper)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return helperproto.ToolStatus{}, fmt.Errorf("helper: inspect tool link: %w", err)
		}
		if link != target {
			return helperproto.ToolStatus{}, fmt.Errorf("%w: %s points to %s", helperproto.ErrToolConflict, wrapper, link)
		}
		if err := os.Remove(wrapper); err != nil && !errors.Is(err, os.ErrNotExist) {
			return helperproto.ToolStatus{}, fmt.Errorf("helper: remove tool link: %w", err)
		}
	}
	m.logger.Info("helper: removed managed tool", "id", id)
	return m.status(id), nil
}

func validateToolID(id string) error {
	switch id {
	case helperproto.ToolPHPDefault, helperproto.ToolComposer:
		return nil
	default:
		return fmt.Errorf("%w: tool id %q (want %s or %s)",
			helperproto.ErrInvalidRequest, id, helperproto.ToolPHPDefault, helperproto.ToolComposer)
	}
}
