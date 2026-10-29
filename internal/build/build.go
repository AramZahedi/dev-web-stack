// Package build runs a static site's native Node build command, streams its
// combined output to a bounded build log, and validates or restores the output
// directory.
package build

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

const (
	// SnapshotSizeCap bounds the previous-output copy. Above it a failed build
	// reports that the previous output could not be restored.
	SnapshotSizeCap int64 = 2 << 30

	buildLogReadLimit = 1 << 20
)

// ErrRuntimeMissing reports that a site's selected Node build is not installed.
var ErrRuntimeMissing = errors.New("build: required Node runtime is not installed")

// ErrInvalidSetup reports a user-fixable build configuration problem such as a
// missing profile, a non-static site, or an invalid project/output path.
var ErrInvalidSetup = errors.New("build: invalid build setup")

// Runner executes one build command. The default uses /bin/sh -c.
type Runner interface {
	Run(ctx context.Context, dir string, env []string, name string, args []string, output io.Writer) error
}

// RuntimeLister lists installed runtime builds. *runtime.Manager satisfies it.
type RuntimeLister interface {
	List(ctx context.Context) ([]model.RuntimeBuild, error)
}

// Deps configures the build service.
type Deps struct {
	Store    *store.Store
	Runtimes RuntimeLister
	Paths    config.Paths
	Now      func() time.Time
	Runner   Runner
	EUID     func() int
}

// Result is one build attempt. A false Success is an operational result, not
// a setup error.
type Result struct {
	Site            string `json:"site"`
	Success         bool   `json:"success"`
	Command         string `json:"command"`
	DurationMS      int64  `json:"duration_ms"`
	LogPath         string `json:"log_path,omitempty"`
	OutputPath      string `json:"output_path,omitempty"`
	Restored        bool   `json:"restored"`
	SnapshotSkipped bool   `json:"snapshot_skipped"`
	Error           string `json:"error,omitempty"`
	RestoreNote     string `json:"restore_note,omitempty"`
}

// Logs is a bounded snapshot of the newest build log.
type Logs struct {
	Site    string `json:"site"`
	Path    string `json:"path,omitempty"`
	Content string `json:"content"`
}

// Service runs static builds as the installing user.
type Service struct {
	deps Deps
}

// NewService returns a build service.
func NewService(deps Deps) *Service {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Runner == nil {
		deps.Runner = execRunner{}
	}
	if deps.EUID == nil {
		deps.EUID = os.Geteuid
	}
	return &Service{deps: deps}
}

// Build runs site.Build.Command with the selected Node runtime in the project
// directory. It snapshots and restores the previous output when practical.
func (s *Service) Build(ctx context.Context, site model.Site) (Result, error) {
	result := Result{Site: site.Name}
	if s.deps.Store == nil {
		return result, errors.New("build: no store configured")
	}
	if strings.TrimSpace(s.deps.Paths.Logs) == "" {
		return result, errors.New("build: logs directory is not configured")
	}
	if site.Build == nil {
		return result, fmt.Errorf("%w: site %q has no build profile", ErrInvalidSetup, site.Name)
	}
	if site.Kind != model.SiteStatic {
		return result, fmt.Errorf("%w: site %q is not a static site", ErrInvalidSetup, site.Name)
	}
	if s.deps.EUID() == 0 {
		return result, errors.New("build: refusing to run a build as root; run devstack as the installing user")
	}
	profile := site.Build
	result.Command = strings.TrimSpace(profile.Command)
	if result.Command == "" {
		return result, fmt.Errorf("%w: site %q has no build command", ErrInvalidSetup, site.Name)
	}
	if strings.TrimSpace(site.HostPath) == "" {
		return result, fmt.Errorf("%w: site %q has no project directory", ErrInvalidSetup, site.Name)
	}
	project, err := filepath.Abs(site.HostPath)
	if err != nil {
		return result, fmt.Errorf("build: resolve project directory: %w", err)
	}
	info, err := os.Stat(project)
	if err != nil || !info.IsDir() {
		return result, fmt.Errorf("%w: project directory %s is not available", ErrInvalidSetup, project)
	}
	output, err := OutputPath(site)
	if err != nil {
		return result, fmt.Errorf("%w: %v", ErrInvalidSetup, err)
	}
	result.OutputPath = output

	var builds []model.RuntimeBuild
	if s.deps.Runtimes != nil {
		builds, err = s.deps.Runtimes.List(ctx)
		if err != nil {
			return result, fmt.Errorf("build: list runtimes: %w", err)
		}
	} else {
		builds, err = s.deps.Store.ListRuntimes(ctx)
		if err != nil {
			return result, fmt.Errorf("build: list runtimes: %w", err)
		}
	}
	nodeBuild, err := resolveNodeBuild(builds, profile.NodeVersion)
	if err != nil {
		return result, err
	}
	nodeBin := filepath.Join(nodeBuild.Path, "bin")
	if info, err := os.Stat(filepath.Join(nodeBin, "node")); err != nil || !info.Mode().IsRegular() {
		return result, fmt.Errorf("%w: Node %s has no bin/node", ErrRuntimeMissing, nodeBuild.Version)
	}

	logDir := filepath.Join(s.logsRoot(), safeComponent(site.Name))
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return result, fmt.Errorf("build: create log directory: %w", err)
	}
	baseLog := filepath.Join(logDir, s.deps.Now().UTC().Format("20060102T150405.000000000Z"))
	logPath, logFile, err := createBuildLog(baseLog)
	if err != nil {
		return result, err
	}
	result.LogPath = logPath

	if err := ensureOutputParentContained(project, output); err != nil {
		_ = logFile.Close()
		return result, fmt.Errorf("%w: %v", ErrInvalidSetup, err)
	}
	snapshot, err := s.snapshot(output)
	if err != nil {
		_ = logFile.Close()
		return result, fmt.Errorf("%w: %v", ErrInvalidSetup, err)
	}
	defer func() {
		if snapshot.dir != "" {
			_ = os.RemoveAll(snapshot.dir)
		}
	}()
	result.SnapshotSkipped = snapshot.skipped

	env := buildEnvironment(nodeBin, site, s.deps.Paths.Home)
	started := s.deps.Now()
	runErr := s.deps.Runner.Run(ctx, project, env, "/bin/sh", []string{"-c", result.Command}, logFile)
	closeErr := logFile.Close()
	result.DurationMS = s.deps.Now().Sub(started).Milliseconds()

	failure := func(message string) (Result, error) {
		result.Success = false
		result.Error = message
		degraded := snapshot.skipped
		if snapshot.dir != "" {
			if err := restore(snapshot.dir, output); err != nil {
				result.RestoreNote = "previous output could not be restored: " + err.Error()
				degraded = true
			} else {
				result.Restored = true
			}
		} else if snapshot.skipped {
			result.RestoreNote = snapshot.note
			result.Error = message + "; previous output could not be restored because the snapshot was skipped"
		}
		if degraded {
			if result.RestoreNote == "" {
				result.RestoreNote = "previous output could not be restored"
			}
			s.markDegraded(ctx, site)
		}
		return result, nil
	}
	if closeErr != nil {
		return failure("build log could not be closed: " + closeErr.Error())
	}
	if runErr != nil {
		return failure("build command failed: " + runErr.Error())
	}
	exists, err := OutputExists(site)
	if err != nil {
		return failure("build output is invalid: " + err.Error())
	}
	if !exists {
		return failure(fmt.Sprintf("build output %s does not exist after a successful build", output))
	}
	result.Success = true
	return result, nil
}

// markDegraded records an observed degraded state after a failed build whose
// previous output could not be restored. Lookup/update failures are ignored:
// the build result and log already carry the actionable failure.
func (s *Service) markDegraded(ctx context.Context, site model.Site) {
	if s.deps.Store == nil || strings.TrimSpace(site.ID) == "" {
		return
	}
	stored, err := s.deps.Store.GetSite(ctx, site.ID)
	if err != nil {
		return
	}
	stored.Status = model.StatusDegraded
	stored.UpdatedAt = s.deps.Now()
	_ = s.deps.Store.UpdateSite(ctx, stored)
}

// Logs returns the newest build log, bounded to its last tail lines.
func (s *Service) Logs(_ context.Context, site model.Site, tail int) (Logs, error) {
	if tail < 0 {
		return Logs{}, errors.New("build: tail must not be negative")
	}
	dir := filepath.Join(s.logsRoot(), safeComponent(site.Name))
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return Logs{Site: site.Name}, nil
	}
	if err != nil {
		return Logs{}, fmt.Errorf("build: read log directory: %w", err)
	}
	latest := ""
	var latestTime time.Time
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return Logs{}, fmt.Errorf("build: inspect build log: %w", err)
		}
		if latest == "" || info.ModTime().After(latestTime) || (info.ModTime().Equal(latestTime) && entry.Name() > filepath.Base(latest)) {
			latest = filepath.Join(dir, entry.Name())
			latestTime = info.ModTime()
		}
	}
	if latest == "" {
		return Logs{Site: site.Name}, nil
	}
	path := latest
	data, err := readTail(path, buildLogReadLimit)
	if err != nil {
		return Logs{}, err
	}
	content := string(data)
	if tail > 0 {
		content = lastLines(content, tail)
	}
	return Logs{Site: site.Name, Path: path, Content: content}, nil
}

// createBuildLog opens a unique log for one build.
func createBuildLog(base string) (string, *os.File, error) {
	for attempt := 0; attempt < 100; attempt++ {
		path := base + ".log"
		if attempt > 0 {
			path = fmt.Sprintf("%s-%d.log", base, attempt)
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", nil, fmt.Errorf("build: create build log: %w", err)
		}
		return path, file, nil
	}
	return "", nil, errors.New("build: could not allocate a unique build log")
}

func (s *Service) logsRoot() string {
	return filepath.Join(s.deps.Paths.Logs, "builds")
}

// OutputPath validates the relative build output and returns its absolute path
// inside the project.
func OutputPath(site model.Site) (string, error) {
	if site.Build == nil {
		return "", fmt.Errorf("build: site %q has no build profile", site.Name)
	}
	output := strings.TrimSpace(site.Build.Output)
	if output == "" {
		return "", fmt.Errorf("build: site %q has no build output directory", site.Name)
	}
	if strings.ContainsRune(output, 0) || filepath.IsAbs(output) || strings.HasPrefix(output, "/") || strings.HasPrefix(output, "\\") {
		return "", fmt.Errorf("build: output %q must be a relative path", output)
	}
	clean := filepath.Clean(output)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("build: output %q escapes the project", output)
	}
	project, err := filepath.Abs(site.HostPath)
	if err != nil {
		return "", fmt.Errorf("build: resolve project directory: %w", err)
	}
	target := filepath.Join(project, clean)
	rel, err := filepath.Rel(project, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("build: output %q escapes the project", output)
	}
	return target, nil
}

// ensureOutputParentContained rejects an output whose nearest existing parent
// resolves outside the project through a symlink.
func ensureOutputParentContained(project, output string) error {
	resolvedProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		return fmt.Errorf("build: resolve project directory: %w", err)
	}
	parent := filepath.Dir(output)
	for {
		if _, err := os.Lstat(parent); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("build: inspect output parent: %w", err)
		}
		next := filepath.Dir(parent)
		if next == parent {
			break
		}
		parent = next
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return fmt.Errorf("build: resolve output parent: %w", err)
	}
	rel, err := filepath.Rel(resolvedProject, resolvedParent)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("build: output parent resolves outside the project")
	}
	return nil
}

// OutputExists reports whether the configured output exists as a directory
// inside the project and is not a symlink escape.
func OutputExists(site model.Site) (bool, error) {
	output, err := OutputPath(site)
	if err != nil {
		return false, err
	}
	info, err := os.Lstat(output)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("build: inspect output: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("build: output %s is a symlink", output)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("build: output %s is not a directory", output)
	}
	hostAbs, err := filepath.Abs(site.HostPath)
	if err != nil {
		return false, fmt.Errorf("build: resolve project directory: %w", err)
	}
	project, err := filepath.EvalSymlinks(hostAbs)
	if err != nil {
		return false, fmt.Errorf("build: resolve project directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(output)
	if err != nil {
		return false, fmt.Errorf("build: resolve output: %w", err)
	}
	rel, err := filepath.Rel(project, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false, fmt.Errorf("build: output %s resolves outside the project", output)
	}
	return true, nil
}

func resolveNodeBuild(builds []model.RuntimeBuild, requested string) (model.RuntimeBuild, error) {
	requested = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(requested), "node@"))
	if requested == "" {
		return model.RuntimeBuild{}, fmt.Errorf("%w: Node version is empty", ErrRuntimeMissing)
	}
	var best model.RuntimeBuild
	found := false
	for _, build := range builds {
		if build.Kind != "node" || (build.Backend != "" && build.Backend != model.BackendLocal) {
			continue
		}
		match := build.Version == requested ||
			(strings.Count(requested, ".") == 1 && strings.HasPrefix(build.Version, requested+".")) ||
			(!strings.Contains(requested, ".") && strings.HasPrefix(build.Version, requested+"."))
		if !match {
			continue
		}
		if !found || compareVersions(build.Version, best.Version) > 0 {
			best, found = build, true
		}
	}
	if !found {
		return model.RuntimeBuild{}, fmt.Errorf("%w: Node %s", ErrRuntimeMissing, requested)
	}
	return best, nil
}

func compareVersions(a, b string) int {
	ap, bp := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(ap) || i < len(bp); i++ {
		var av, bv int
		if i < len(ap) {
			_, _ = fmt.Sscanf(ap[i], "%d", &av)
		}
		if i < len(bp) {
			_, _ = fmt.Sscanf(bp[i], "%d", &bv)
		}
		switch {
		case av < bv:
			return -1
		case av > bv:
			return 1
		}
	}
	return 0
}

func buildEnvironment(nodeBin string, site model.Site, home string) []string {
	basePath := os.Getenv("PATH")
	if site.Build != nil {
		if configured := strings.TrimSpace(site.Build.Env["PATH"]); configured != "" {
			basePath = configured
		}
	}
	values := map[string]string{}
	values["PATH"] = nodeBin
	if basePath != "" {
		values["PATH"] += string(os.PathListSeparator) + basePath
	}
	values["DEVSTACK_SITE"] = site.Name
	values["DEVSTACK_HOME"] = home
	values["DEVSTACK_PROJECT"] = site.HostPath
	if site.Build != nil {
		for key, value := range site.Build.Env {
			if key == "PATH" {
				continue
			}
			values[key] = value
		}
	}
	return mergeEnvironment(os.Environ(), values)
}

// mergeEnvironment replaces existing keys and appends new keys in sorted
// order.
func mergeEnvironment(base []string, values map[string]string) []string {
	env := make([]string, 0, len(base)+len(values))
	seen := make(map[string]struct{}, len(base))
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			env = append(env, entry)
			continue
		}
		if _, replace := values[key]; replace {
			if _, already := seen[key]; already {
				continue
			}
			env = append(env, key+"="+values[key])
			seen[key] = struct{}{}
			continue
		}
		env = append(env, entry)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		if _, ok := seen[key]; ok {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+values[key])
	}
	return env
}

// SafeComponent maps a site name to a directory component that cannot escape
// the build-log root.
func SafeComponent(value string) string { return safeComponent(value) }

func safeComponent(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." {
		return "site"
	}
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteByte('-')
		}
	}
	return builder.String()
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, dir string, env []string, name string, args []string, output io.Writer) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = dir
	command.Env = env
	command.Stdout = output
	command.Stderr = output
	return command.Run()
}

type outputSnapshot struct {
	dir     string
	skipped bool
	note    string
}

// snapshot copies the existing output below logs/builds when it fits under the
// size cap.
func (s *Service) snapshot(output string) (outputSnapshot, error) {
	info, err := os.Lstat(output)
	if errors.Is(err, os.ErrNotExist) {
		return outputSnapshot{}, nil
	}
	if err != nil {
		return outputSnapshot{}, fmt.Errorf("build: inspect previous output: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return outputSnapshot{}, fmt.Errorf("build: output %s is a symlink", output)
	}
	size, err := pathSize(output)
	if err != nil {
		return outputSnapshot{skipped: true, note: "previous output could not be measured: " + err.Error()}, nil
	}
	if size > SnapshotSizeCap {
		return outputSnapshot{skipped: true, note: "previous output exceeds the 2 GiB snapshot cap"}, nil
	}
	dir, err := os.MkdirTemp(s.logsRoot(), "snapshot-")
	if err != nil {
		return outputSnapshot{skipped: true, note: "could not create a snapshot directory: " + err.Error()}, nil
	}
	copyTarget := filepath.Join(dir, "output")
	if err := copyPath(output, copyTarget); err != nil {
		_ = os.RemoveAll(dir)
		return outputSnapshot{skipped: true, note: "could not snapshot the previous output: " + err.Error()}, nil
	}
	return outputSnapshot{dir: dir}, nil
}

func restore(snapshotDir, output string) error {
	if err := os.RemoveAll(output); err != nil {
		return fmt.Errorf("remove partial output: %w", err)
	}
	if err := copyPath(filepath.Join(snapshotDir, "output"), output); err != nil {
		return fmt.Errorf("restore snapshot: %w", err)
	}
	return nil
}

func pathSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			total += info.Size()
			if total > SnapshotSizeCap {
				return errSnapshotLimit
			}
		}
		return nil
	})
	if errors.Is(err, errSnapshotLimit) {
		return SnapshotSizeCap + 1, nil
	}
	return total, err
}

var errSnapshotLimit = errors.New("snapshot limit reached")

func copyPath(source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		link, err := os.Readlink(source)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.Symlink(link, target)
	case info.IsDir():
		if err := os.MkdirAll(target, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyPath(filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name())); err != nil {
				return err
			}
		}
		return os.Chmod(target, info.Mode().Perm())
	case info.Mode().IsRegular():
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := os.Open(source)
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_EXCL, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		return os.Chmod(target, info.Mode().Perm())
	default:
		return fmt.Errorf("unsupported file type at %s", source)
	}
}

func readTail(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("build: open log: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("build: stat log: %w", err)
	}
	offset := int64(0)
	if info.Size() > limit {
		offset = info.Size() - limit
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("build: seek log: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit))
	if err != nil {
		return nil, fmt.Errorf("build: read log: %w", err)
	}
	return data, nil
}

func lastLines(content string, n int) string {
	lines := strings.SplitAfter(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if n <= 0 || n >= len(lines) {
		return content
	}
	return strings.Join(lines[len(lines)-n:], "")
}
