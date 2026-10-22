package backend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/logs"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
	"github.com/AramZahedi/dev-web-stack/internal/supervisor"
)

const (
	staticReadyTimeout = 30 * time.Second
	nodeReadyTimeout   = 60 * time.Second

	// logTailLines is the snapshot size returned when Logs is not following.
	logTailLines = 5000
)

// defaultRestartPolicy follows the machine spec: back off for up to five
// failures in five minutes, then leave the site crashed until a manual start.
var defaultRestartPolicy = supervisor.RestartPolicy{
	MaxRestarts: 5,
	Window:      5 * time.Minute,
	Backoff: []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		30 * time.Second,
	},
}

// Local runs site processes on the host through the process supervisor.
type Local struct {
	cfg      config.Config
	store    *store.Store
	logs     *logs.Store
	runtimes RuntimeProvider
	selfBin  string

	supervisor *supervisor.Manager

	mu       sync.Mutex
	prepared map[string]preparedSite
}

var _ Backend = (*Local)(nil)

// preparedSite is the validated information needed to start one site. The
// spec is detached from the caller's maps and slices.
type preparedSite struct {
	spec         SiteSpec
	documentRoot string
	workingDir   string
	nodeRoot     string
}

// NewLocal returns a local backend rooted at cfg.Home. stores is retained for
// callers that need machine state; logStore receives process output. runtimes
// may be nil when only static sites are used. selfBin is the current devstack
// executable, used to launch the internal serve-static process.
func NewLocal(cfg config.Config, stores *store.Store, logStore *logs.Store, runtimes RuntimeProvider, selfBin string) (*Local, error) {
	if strings.TrimSpace(cfg.Home) == "" {
		return nil, errors.New("backend: home directory is not configured")
	}
	if stores == nil {
		return nil, errors.New("backend: store is not configured")
	}
	if logStore == nil {
		return nil, errors.New("backend: log store is not configured")
	}
	if strings.TrimSpace(selfBin) == "" {
		return nil, errors.New("backend: devstack binary path is not configured")
	}
	selfBin, err := filepath.Abs(selfBin)
	if err != nil {
		return nil, fmt.Errorf("backend: resolve devstack binary %q: %w", selfBin, err)
	}
	info, err := os.Stat(selfBin)
	if err != nil {
		return nil, fmt.Errorf("backend: stat devstack binary %s: %w", selfBin, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("backend: devstack binary %s is a directory", selfBin)
	}

	paths := config.NewPaths(cfg.Home)
	if err := paths.Ensure(); err != nil {
		return nil, fmt.Errorf("backend: prepare home %s: %w", cfg.Home, err)
	}

	logger := slog.Default().With("component", "backend")
	mgr := supervisor.NewManager(paths.Run, logger)
	if cfg.GracefulStopSeconds > 0 {
		mgr.StopGrace = time.Duration(cfg.GracefulStopSeconds) * time.Second
	}

	return &Local{
		cfg:        cfg,
		store:      stores,
		logs:       logStore,
		runtimes:   runtimes,
		selfBin:    selfBin,
		supervisor: mgr,
		prepared:   make(map[string]preparedSite),
	}, nil
}

// Prepare validates spec and records it for a later Start. Preparing a site
// again replaces the stored spec, so callers can change ports between runs.
func (l *Local) Prepare(ctx context.Context, spec SiteSpec) (PreparedSite, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return PreparedSite{}, err
	}
	if strings.TrimSpace(spec.ID) == "" {
		return PreparedSite{}, errors.New("backend: site id is required")
	}

	p := preparedSite{spec: cloneSiteSpec(spec)}
	switch spec.Kind {
	case string(model.SiteStatic):
		if spec.FrontController {
			return PreparedSite{}, fmt.Errorf("backend: site %q is static but FrontController is PHP-only", spec.ID)
		}
		docRoot, err := resolveDocumentRoot(spec)
		if err != nil {
			return PreparedSite{}, err
		}
		if err := validatePort(spec.HTTPPort, "HTTP port", spec.ID); err != nil {
			return PreparedSite{}, err
		}
		p.documentRoot = docRoot
		p.workingDir = docRoot

	case string(model.SiteNode):
		if spec.FrontController {
			return PreparedSite{}, fmt.Errorf("backend: site %q is Node but FrontController is PHP-only", spec.ID)
		}
		if strings.TrimSpace(spec.Command) == "" {
			return PreparedSite{}, fmt.Errorf("backend: node site %q requires a command", spec.ID)
		}
		if err := validatePort(spec.HTTPPort, "HTTP port", spec.ID); err != nil {
			return PreparedSite{}, err
		}
		dir, err := resolveWorkingDir(spec)
		if err != nil {
			return PreparedSite{}, err
		}
		p.workingDir = dir

		if spec.RuntimeVersion != "" {
			if l.runtimes == nil {
				return PreparedSite{}, fmt.Errorf("backend: node site %q needs runtime %s but no runtime provider is configured", spec.ID, spec.RuntimeVersion)
			}
			build, err := l.runtimes.Ensure(ctx, "node", spec.RuntimeVersion)
			if err != nil {
				return PreparedSite{}, fmt.Errorf("backend: ensure node %s for site %q: %w", spec.RuntimeVersion, spec.ID, err)
			}
			if strings.TrimSpace(build.Path) == "" {
				return PreparedSite{}, fmt.Errorf("backend: node %s for site %q has an empty install path", spec.RuntimeVersion, spec.ID)
			}
			p.nodeRoot = build.Path
		}

	case string(model.SitePHP):
		return PreparedSite{}, fmt.Errorf("backend: PHP sites are not supported before Plan 2 (site %q)", spec.ID)

	default:
		return PreparedSite{}, fmt.Errorf("backend: site %q has unknown kind %q", spec.ID, spec.Kind)
	}

	l.mu.Lock()
	l.prepared[spec.ID] = p
	l.mu.Unlock()

	return PreparedSite{
		BackendHost: "127.0.0.1",
		HTTPPort:    spec.HTTPPort,
		FastCGIPort: spec.FastCGIPort,
	}, nil
}

// Start launches the prepared site and waits for its readiness probe. Starting
// an already running site returns the existing process information.
func (l *Local) Start(ctx context.Context, id string) (ProcessInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ProcessInfo{}, err
	}

	l.mu.Lock()
	p, ok := l.prepared[id]
	l.mu.Unlock()
	if !ok {
		return ProcessInfo{}, fmt.Errorf("backend: site %q is not prepared", id)
	}

	if st, known := l.supervisor.Status(id); known {
		switch st.State {
		case model.StatusRunning:
			return ProcessInfo{PID: st.PID, StartedAt: st.StartedAt}, nil
		case model.StatusStarting:
			return ProcessInfo{}, fmt.Errorf("backend: site %q is already starting", id)
		case model.StatusStopping:
			return ProcessInfo{}, fmt.Errorf("backend: site %q is stopping", id)
		}
	}

	procSpec, err := l.processSpec(p)
	if err != nil {
		return ProcessInfo{}, err
	}
	st, err := l.supervisor.Start(ctx, procSpec)
	if err != nil {
		return ProcessInfo{}, fmt.Errorf("backend: start site %q: %w", id, err)
	}
	return ProcessInfo{PID: st.PID, StartedAt: st.StartedAt}, nil
}

// Stop delegates termination to the supervisor. The supervisor always sends
// SIGTERM first and escalates to SIGKILL after the configured grace period;
// Plan 1 has no separate forced path, so graceful is retained for backends that
// can honour it.
func (l *Local) Stop(ctx context.Context, id string, graceful bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	_ = graceful
	if err := l.supervisor.Stop(ctx, id); err != nil {
		return fmt.Errorf("backend: stop site %q: %w", id, err)
	}
	return nil
}

// Status returns the supervisor's snapshot. Sites that have never been started
// are reported as stopped.
func (l *Local) Status(ctx context.Context, id string) (Status, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	st, ok := l.supervisor.Status(id)
	if !ok {
		return Status{State: model.StatusStopped}, nil
	}
	return Status{State: st.State, PID: st.PID, Message: st.LastError}, nil
}

// Logs returns the site's recent log lines. Following streams new lines until
// the reader is closed; a non-following call returns a finite snapshot.
func (l *Local) Logs(ctx context.Context, id string, follow bool) (io.ReadCloser, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l.logs == nil {
		return nil, errors.New("backend: log store is not configured")
	}
	// Tail validates the site id and detects a closed store, which are the two
	// cases where Subscribe cannot create a live subscription.
	if _, err := l.logs.Tail(id, 0); err != nil {
		return nil, fmt.Errorf("backend: logs for site %q are unavailable: %w", id, err)
	}

	if !follow {
		lines, err := l.logs.Tail(id, logTailLines)
		if err != nil {
			return nil, fmt.Errorf("backend: logs for site %q are unavailable: %w", id, err)
		}
		var buf strings.Builder
		for _, line := range lines {
			buf.WriteString(formatLogLine(line))
		}
		return io.NopCloser(strings.NewReader(buf.String())), nil
	}

	ch, cancel := l.logs.Subscribe(id)
	pr, pw := io.Pipe()
	go func() {
		defer cancel()
		for line := range ch {
			if _, err := io.WriteString(pw, formatLogLine(line)); err != nil {
				_ = pw.CloseWithError(err)
				return
			}
		}
		_ = pw.Close()
	}()

	return &logStream{reader: pr, cancel: cancel}, nil
}

// Exec runs argv in the site's working directory with the site and caller
// environments. A non-zero exit status is a normal Result.
func (l *Local) Exec(ctx context.Context, id string, argv []string, env map[string]string) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return Result{}, errors.New("backend: exec requires a command")
	}

	l.mu.Lock()
	p, ok := l.prepared[id]
	l.mu.Unlock()
	if !ok {
		return Result{}, fmt.Errorf("backend: site %q is not prepared", id)
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = p.workingDir
	cmd.Env = mergedEnv(p.spec.Env, env)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	result := Result{}
	err := cmd.Run()
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	if err == nil {
		return result, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return result, fmt.Errorf("backend: exec in site %q: %w", id, err)
}

// EnsureRuntime delegates installation to the configured runtime provider.
func (l *Local) EnsureRuntime(ctx context.Context, kind, version string) (model.RuntimeBuild, error) {
	if l.runtimes == nil {
		return model.RuntimeBuild{}, errors.New("backend: runtime provider is not configured")
	}
	build, err := l.runtimes.Ensure(ctx, kind, version)
	if err != nil {
		return model.RuntimeBuild{}, fmt.Errorf("backend: ensure runtime %s %s: %w", kind, version, err)
	}
	return build, nil
}

// RemoveRuntime delegates deletion to the configured runtime provider.
func (l *Local) RemoveRuntime(ctx context.Context, buildID string) error {
	if l.runtimes == nil {
		return errors.New("backend: runtime provider is not configured")
	}
	if err := l.runtimes.Remove(ctx, buildID); err != nil {
		return fmt.Errorf("backend: remove runtime %q: %w", buildID, err)
	}
	return nil
}

// InstallCA writes the system roots plus the devstack root to
// pki/ca-bundle.crt with mode 0600. The replacement is atomic and idempotent.
func (l *Local) InstallCA(ctx context.Context, pem []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	system, err := readSystemRoots()
	if err != nil {
		return err
	}
	bundle := combineCA(system, pem)

	dir := filepath.Join(l.cfg.Home, "pki")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("backend: create %s: %w", dir, err)
	}
	path := filepath.Join(dir, "ca-bundle.crt")
	if err := writeFileAtomic(path, bundle, 0o600); err != nil {
		return fmt.Errorf("backend: install CA bundle: %w", err)
	}
	return nil
}

// Health verifies that the machine home and run directories exist and are
// writable, and that the binary used for static sites is configured.
func (l *Local) Health(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(l.cfg.Home) == "" {
		return errors.New("backend: home directory is not configured")
	}
	for _, dir := range []string{l.cfg.Home, filepath.Join(l.cfg.Home, "run")} {
		info, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("backend: health check %s: %w", dir, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("backend: health check %s is not a directory", dir)
		}
		probe, err := os.CreateTemp(dir, ".health-*")
		if err != nil {
			return fmt.Errorf("backend: health check %s is not writable: %w", dir, err)
		}
		name := probe.Name()
		if err := probe.Close(); err != nil {
			_ = os.Remove(name)
			return fmt.Errorf("backend: health check %s: %w", dir, err)
		}
		if err := os.Remove(name); err != nil {
			return fmt.Errorf("backend: health check %s: %w", dir, err)
		}
	}
	if strings.TrimSpace(l.selfBin) == "" {
		return errors.New("backend: devstack binary path is not configured")
	}
	return nil
}

// processSpec converts a prepared site into a supervisor specification.
func (l *Local) processSpec(p preparedSite) (supervisor.ProcessSpec, error) {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(p.spec.HTTPPort))
	stdout := l.logs.Writer(p.spec.ID, "app")
	stderr := l.logs.Writer(p.spec.ID, "app")

	switch p.spec.Kind {
	case string(model.SiteStatic):
		argv := []string{
			l.selfBin,
			"serve-static",
			"--root", p.documentRoot,
			"--addr", addr,
		}
		if p.spec.SPAFallback {
			argv = append(argv, "--spa")
		}
		return supervisor.ProcessSpec{
			ID:      p.spec.ID,
			Argv:    argv,
			Dir:     p.documentRoot,
			Ready:   supervisor.ReadyCheck{TCPAddr: addr, Timeout: staticReadyTimeout},
			Restart: defaultRestartPolicy,
			Stdout:  stdout,
			Stderr:  stderr,
		}, nil

	case string(model.SiteNode):
		env := nodeEnvironment(p.spec.HTTPPort, p.nodeRoot, l.caBundlePath(), p.spec.Env)
		return supervisor.ProcessSpec{
			ID:      p.spec.ID,
			Argv:    []string{"/bin/sh", "-lc", p.spec.Command},
			Env:     env,
			Dir:     p.workingDir,
			Ready:   supervisor.ReadyCheck{TCPAddr: addr, Timeout: nodeReadyTimeout},
			Restart: defaultRestartPolicy,
			Stdout:  stdout,
			Stderr:  stderr,
		}, nil
	}
	return supervisor.ProcessSpec{}, fmt.Errorf("backend: site %q has unsupported kind %q", p.spec.ID, p.spec.Kind)
}

func (l *Local) caBundlePath() string {
	return filepath.Join(l.cfg.Home, "pki", "ca-bundle.crt")
}

// resolveDocumentRoot returns the absolute directory served by a static site.
func resolveDocumentRoot(spec SiteSpec) (string, error) {
	project := strings.TrimSpace(spec.BackendPath)
	if project == "" {
		project = strings.TrimSpace(spec.HostPath)
	}
	documentRoot := strings.TrimSpace(spec.DocumentRoot)

	var resolved string
	switch {
	case documentRoot == "":
		if project == "" {
			return "", fmt.Errorf("backend: static site %q requires a project path or document root", spec.ID)
		}
		resolved = project
	case filepath.IsAbs(documentRoot):
		resolved = documentRoot
	default:
		if !filepath.IsLocal(documentRoot) {
			return "", fmt.Errorf("backend: static site %q document root %q escapes the project", spec.ID, documentRoot)
		}
		if project == "" {
			return "", fmt.Errorf("backend: static site %q has a relative document root but no project path", spec.ID)
		}
		resolved = filepath.Join(project, documentRoot)
	}

	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("backend: resolve document root %q: %w", resolved, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("backend: static site %q document root %s: %w", spec.ID, abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("backend: static site %q document root %s is not a directory", spec.ID, abs)
	}
	return abs, nil
}

// resolveWorkingDir validates the Node project directory, when one is given.
func resolveWorkingDir(spec SiteSpec) (string, error) {
	dir := strings.TrimSpace(spec.BackendPath)
	if dir == "" {
		dir = strings.TrimSpace(spec.HostPath)
	}
	if dir == "" {
		return "", nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("backend: resolve site %q path %q: %w", spec.ID, dir, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("backend: node site %q path %s: %w", spec.ID, abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("backend: node site %q path %s is not a directory", spec.ID, abs)
	}
	return abs, nil
}

func validatePort(port int, label, id string) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("backend: site %q %s %d is not between 1 and 65535", id, label, port)
	}
	return nil
}

// cloneSiteSpec detaches the caller's environment map from the stored spec.
func cloneSiteSpec(spec SiteSpec) SiteSpec {
	if spec.Env != nil {
		env := make(map[string]string, len(spec.Env))
		for key, value := range spec.Env {
			env[key] = value
		}
		spec.Env = env
	}
	return spec
}

// nodeEnvironment builds the process environment for a Node site. PORT and
// NODE_EXTRA_CA_CERTS always describe the allocated port and devstack bundle;
// the runtime's bin directory precedes the inherited or site-supplied PATH.
func nodeEnvironment(port int, nodeRoot, caBundle string, siteEnv map[string]string) map[string]string {
	env := make(map[string]string, len(siteEnv)+3)
	pathValue := os.Getenv("PATH")
	if sitePath, ok := siteEnv["PATH"]; ok && sitePath != "" {
		pathValue = sitePath
	}
	for key, value := range siteEnv {
		env[key] = value
	}
	if nodeRoot != "" {
		binDir := filepath.Join(nodeRoot, "bin")
		if pathValue == "" {
			env["PATH"] = binDir
		} else {
			env["PATH"] = binDir + string(os.PathListSeparator) + pathValue
		}
	}
	env["PORT"] = strconv.Itoa(port)
	env["NODE_EXTRA_CA_CERTS"] = caBundle
	return env
}

// mergedEnv layers the site environment and per-call environment over the
// daemon's environment.
func mergedEnv(siteEnv, callEnv map[string]string) []string {
	values := make(map[string]string, len(os.Environ())+len(siteEnv)+len(callEnv))
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		values[key] = value
	}
	for key, value := range siteEnv {
		values[key] = value
	}
	for key, value := range callEnv {
		values[key] = value
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+values[key])
	}
	return env
}

// logStream unsubscribes when the reader is closed.
type logStream struct {
	reader *io.PipeReader
	cancel func()
	once   sync.Once
	err    error
}

func (s *logStream) Read(p []byte) (int, error) {
	return s.reader.Read(p)
}

func (s *logStream) Close() error {
	s.once.Do(func() {
		s.cancel()
		s.err = s.reader.Close()
	})
	return s.err
}

// formatLogLine renders a stream line as <RFC3339> <source> <text>\n.
func formatLogLine(line logs.Line) string {
	return line.Time.UTC().Format(time.RFC3339) + " " + line.Source + " " + line.Text + "\n"
}

// systemRootFiles are tried in order when assembling the CA bundle.
var systemRootFiles = []string{
	"/etc/ssl/certs/ca-certificates.crt",
	"/etc/pki/tls/certs/ca-bundle.crt",
	"/etc/ssl/cert.pem",
}

func readSystemRoots() ([]byte, error) {
	for _, path := range systemRootFiles {
		data, err := os.ReadFile(path)
		if err != nil || len(bytes.TrimSpace(data)) == 0 {
			continue
		}
		return data, nil
	}
	return nil, fmt.Errorf("backend: no system CA bundle found (tried %s)", strings.Join(systemRootFiles, ", "))
}

// combineCA joins the system and devstack roots into one normalized bundle.
func combineCA(system, extra []byte) []byte {
	system = bytes.TrimSpace(system)
	extra = bytes.TrimSpace(extra)

	bundle := make([]byte, 0, len(system)+len(extra)+2)
	bundle = append(bundle, system...)
	if len(extra) > 0 {
		if len(system) > 0 {
			bundle = append(bundle, '\n')
		}
		bundle = append(bundle, extra...)
	}
	bundle = append(bundle, '\n')
	return bundle
}

// writeFileAtomic writes data to path through a temporary file in the same
// directory, then renames it into place.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create temporary file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set mode on %s: %w", tmpName, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	tmpName = ""
	return nil
}
