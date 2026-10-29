package tooling

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

const (
	// DefaultNodeSettingKey is the store setting holding the machine-wide Node
	// CLI default. It is normally an exact build id and also accepts an exact
	// version, a major line, or a major.minor line for compatibility.
	DefaultNodeSettingKey = "node_default"

	// NodeDefaultSourceSetting and NodeDefaultSourceNewest describe where a
	// resolved Node default came from.
	NodeDefaultSourceSetting = "setting"
	NodeDefaultSourceNewest  = "newest"

	// NodeDefaultEnvironmentSource is the DEVSTACK_NODE_SOURCE value for a
	// runtime inherited from the global default.
	NodeDefaultEnvironmentSource = "global"
)

var (
	// ErrNoNodeInstalled reports that no managed Node build is installed.
	ErrNoNodeInstalled = errors.New("tooling: no Node build is installed")
	// ErrNodeDefaultNotInstalled reports that the requested Node default does
	// not name an installed build.
	ErrNodeDefaultNotInstalled = errors.New("tooling: the requested Node default is not installed")
	// ErrUnsupportedNodeDefault reports a malformed Node default value.
	ErrUnsupportedNodeDefault = errors.New("tooling: invalid Node default")
)

// NodeDefaultStatus is the machine-wide Node CLI default state.
type NodeDefaultStatus struct {
	Configured    string              `json:"configured,omitempty"`
	Build         *model.RuntimeBuild `json:"build,omitempty"`
	ShimDir       string              `json:"shim_dir"`
	ShimInstalled bool                `json:"shim_installed"`
	Source        string              `json:"source"`
}

// NodeDefaultToolDir returns the managed directory for the global Node
// default metadata and WSL shims.
func NodeDefaultToolDir(paths config.Paths) string {
	return filepath.Join(paths.Tools, "node-default")
}

// NodeDefaultShimDir returns the directory holding the WSL-visible global Node
// CLI shims.
func NodeDefaultShimDir(paths config.Paths) string {
	return filepath.Join(NodeDefaultToolDir(paths), "bin")
}

// NodeDefaultVersionPath returns the file recording the installed global Node
// default build.
func NodeDefaultVersionPath(paths config.Paths) string {
	return filepath.Join(NodeDefaultToolDir(paths), "VERSION")
}

// ResolveNodeDefault resolves the global Node default from the store. An explicit
// setting wins; without one the newest installed compatible build is used.
// The setting may be an exact build id, an exact version, a major line such as
// 22, or a major.minor line such as 22.11. Resolution never downloads and
// never falls back to a build that is not installed.
func ResolveNodeDefault(ctx context.Context, st *store.Store) (model.RuntimeBuild, error) {
	if st == nil {
		return model.RuntimeBuild{}, errors.New("tooling: no store configured")
	}
	builds, err := installedNodeBuilds(ctx, st)
	if err != nil {
		return model.RuntimeBuild{}, err
	}
	setting := ""
	if value, err := st.GetSetting(ctx, DefaultNodeSettingKey); err == nil {
		setting = strings.TrimSpace(value)
	} else if !errors.Is(err, store.ErrNotFound) {
		return model.RuntimeBuild{}, fmt.Errorf("tooling: read %s: %w", DefaultNodeSettingKey, err)
	}
	if setting == "" {
		if len(builds) == 0 {
			return model.RuntimeBuild{}, ErrNoNodeInstalled
		}
		return builds[0], nil
	}
	build, ok, err := resolveNodeDefaultSelection(builds, setting)
	if err != nil {
		return model.RuntimeBuild{}, err
	}
	if !ok {
		return model.RuntimeBuild{}, fmt.Errorf("%w: %s", ErrNodeDefaultNotInstalled, setting)
	}
	return build, nil
}

// ResolveNodeDefaultStatus reports the configured value, resolved build, shim
// location, shim installation state, and the selection source. A configured
// default that is no longer installed is reported with a nil build rather than
// silently falling back to another version.
func ResolveNodeDefaultStatus(ctx context.Context, st *store.Store, paths config.Paths) (NodeDefaultStatus, error) {
	if st == nil {
		return NodeDefaultStatus{}, errors.New("tooling: no store configured")
	}
	status := NodeDefaultStatus{ShimDir: nodeDefaultShimStatusDir(paths)}
	if value, err := st.GetSetting(ctx, DefaultNodeSettingKey); err == nil {
		status.Configured = strings.TrimSpace(value)
	} else if !errors.Is(err, store.ErrNotFound) {
		return NodeDefaultStatus{}, fmt.Errorf("tooling: read %s: %w", DefaultNodeSettingKey, err)
	}

	build, err := ResolveNodeDefault(ctx, st)
	switch {
	case err == nil:
		status.Build = &build
		if status.Configured != "" {
			status.Source = NodeDefaultSourceSetting
		} else {
			status.Source = NodeDefaultSourceNewest
		}
	case errors.Is(err, ErrNoNodeInstalled), errors.Is(err, ErrNodeDefaultNotInstalled):
		if status.Configured != "" {
			status.Source = NodeDefaultSourceSetting
		}
	default:
		return NodeDefaultStatus{}, err
	}

	shimName := "node"
	if runtime.GOOS == "windows" {
		shimName = "node.cmd"
	}
	if info, err := os.Stat(filepath.Join(status.ShimDir, shimName)); err == nil && info.Mode().IsRegular() {
		status.ShimInstalled = true
	}
	return status, nil
}

// NodeDefault returns the current global Node default and shim state.
func (s *Service) NodeDefault(ctx context.Context) (NodeDefaultStatus, error) {
	if s.deps.Store == nil {
		return NodeDefaultStatus{}, errors.New("tooling: no store configured")
	}
	return ResolveNodeDefaultStatus(ctx, s.deps.Store, s.deps.Paths)
}

// NodeDefaultEnvironment returns the environment values identifying the
// resolved global Node default. A nil build yields no values.
func NodeDefaultEnvironment(build *model.RuntimeBuild) map[string]string {
	if build == nil || strings.TrimSpace(build.Version) == "" {
		return nil
	}
	return map[string]string{
		"DEVSTACK_NODE_VERSION": strings.TrimSpace(build.Version),
		"DEVSTACK_NODE_SOURCE":  NodeDefaultEnvironmentSource,
	}
}

// EnsureNodeShims writes node, npm, and npx wrappers for the exact installed
// build under the managed node-default shim directory. The wrappers execute
// the absolute build binary and never resolve back through PATH.
func EnsureNodeShims(paths config.Paths, build model.RuntimeBuild) error {
	if build.Kind != "" && build.Kind != "node" {
		return fmt.Errorf("tooling: build %q is not a Node build", build.ID)
	}
	dir := NodeDefaultShimDir(paths)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("tooling: create Node shim directory: %w", err)
	}
	for _, name := range []string{"node", "npm", "npx"} {
		binary, err := nodeToolBinary(build, name)
		if err != nil {
			return err
		}
		// npm and npx are launchers with a /usr/bin/env node shebang. Pin the
		// exact build's bin directory first so their interpreter never resolves
		// through an ambient PATH. The node shim gets the same PATH so child
		// processes also see the selected build.
		script := "#!/bin/sh\nexport PATH=" + shellQuote(path.Dir(binary)) + ":\"$PATH\"\nexec " + shellQuote(binary) + " \"$@\"\n"
		if err := writeScriptAtomic(filepath.Join(dir, name), script); err != nil {
			return err
		}
	}
	version := strings.TrimSpace(build.ID)
	if version == "" {
		version = "node@" + strings.TrimSpace(build.Version)
	}
	if err := writeAtomicFile(NodeDefaultVersionPath(paths), []byte(version+"\n"), 0o644); err != nil {
		return err
	}
	return nil
}

// installedNodeBuilds returns installed Node builds, newest first.
func installedNodeBuilds(ctx context.Context, st *store.Store) ([]model.RuntimeBuild, error) {
	builds, err := st.ListRuntimes(ctx)
	if err != nil {
		return nil, fmt.Errorf("tooling: list runtimes: %w", err)
	}
	nodes := make([]model.RuntimeBuild, 0, len(builds))
	for _, build := range builds {
		if build.Kind == "node" && isCompatibleBackend(build.Backend) {
			nodes = append(nodes, build)
		}
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		if cmp := compareVersions(nodes[i].Version, nodes[j].Version); cmp != 0 {
			return cmp > 0
		}
		return buildRevision(nodes[i].ID) > buildRevision(nodes[j].ID)
	})
	return nodes, nil
}

// resolveNodeDefaultSelection resolves an exact build id, exact version, major
// line, or major.minor line against builds, which are newest first.
func resolveNodeDefaultSelection(builds []model.RuntimeBuild, value string) (model.RuntimeBuild, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return model.RuntimeBuild{}, false, nil
	}
	if strings.HasPrefix(value, "node@") {
		parsed, err := catalog.ParseID(value)
		if err != nil || parsed.Kind != "node" {
			return model.RuntimeBuild{}, false, fmt.Errorf("%w: %q (want an installed build id such as node@22.11.0)", ErrUnsupportedNodeDefault, value)
		}
		id := catalog.FormatID("node", parsed.Version, 0)
		for _, build := range builds {
			if build.ID == id {
				return build, true, nil
			}
		}
		return model.RuntimeBuild{}, false, nil
	}
	if parsed, err := catalog.ParseID("node@" + value); err == nil {
		id := catalog.FormatID("node", parsed.Version, 0)
		for _, build := range builds {
			if build.ID == id || build.Version == parsed.Version {
				return build, true, nil
			}
		}
		return model.RuntimeBuild{}, false, nil
	}
	if isLine(value) {
		for _, build := range builds {
			if catalog.LineOf(build.Version) == value {
				return build, true, nil
			}
		}
		return model.RuntimeBuild{}, false, nil
	}
	if major, err := strconv.Atoi(value); err == nil && major >= 0 {
		prefix := strconv.Itoa(major) + "."
		for _, build := range builds {
			if strings.HasPrefix(build.Version, prefix) {
				return build, true, nil
			}
		}
		return model.RuntimeBuild{}, false, nil
	}
	return model.RuntimeBuild{}, false, fmt.Errorf("%w: %q (want an installed build id such as node@22.11.0 or a version line such as 22)", ErrUnsupportedNodeDefault, value)
}

// nodeDefaultShimStatusDir returns the directory whose node shim the status
// reports. Windows uses the public command shim directory; other platforms use
// the WSL-visible managed shim directory.
func nodeDefaultShimStatusDir(paths config.Paths) string {
	if runtime.GOOS == "windows" {
		return WindowsNodeShimDir(paths)
	}
	return NodeDefaultShimDir(paths)
}

// nodeToolBinary returns the exact build binary path for name. POSIX build
// paths stay POSIX even when this code runs on Windows because managed Node
// builds live in the WSL distro.
func nodeToolBinary(build model.RuntimeBuild, name string) (string, error) {
	root := strings.TrimSpace(build.Path)
	if root == "" {
		return "", errors.New("tooling: Node build path is empty")
	}
	if strings.ContainsAny(root, "\x00\r\n") {
		return "", fmt.Errorf("tooling: Node build path %q contains invalid characters", root)
	}
	if strings.HasPrefix(root, "/") {
		return path.Join(root, "bin", name), nil
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("tooling: Node build path %q is not absolute", root)
	}
	return filepath.ToSlash(filepath.Join(root, "bin", name)), nil
}
