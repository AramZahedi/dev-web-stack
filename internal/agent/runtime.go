package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

var ErrSiteNotFound = errors.New("agent: site not found")

type runtimeSite struct {
	spec           agentproto.SiteSpec
	documentRoot   string
	desiredRunning bool
	status         string
	server         *http.Server
	nodeCmd        *exec.Cmd
	nodePID        int
	nodeDone       chan struct{}
	activeGen      string
	phpGenerations map[string]*phpGeneration
}

// Runtime owns site state, backend ports, and process/server reconciliation in
// the WSL filesystem. It stores its state beneath /data/devstack.
type Runtime struct {
	root        string
	statePath   string
	ports       *PortAllocator
	logs        *logHub
	mu          sync.Mutex
	sites       map[string]*runtimeSite
	runtimes    map[string]agentproto.RuntimeResponse
	runtimeMu   sync.Mutex
	termMu      sync.Mutex
	termTickets map[string]*terminalTicket

	terminalIdleTimeout time.Duration
	terminalMaxDuration time.Duration
}

type persistedSite struct {
	Spec           agentproto.SiteSpec `json:"spec"`
	DocumentRoot   string              `json:"document_root"`
	DesiredRunning bool                `json:"desired_running"`
	ActiveGen      string              `json:"active_gen,omitempty"`
}

type persistedRuntime struct {
	Version int             `json:"version"`
	Sites   []persistedSite `json:"sites"`
}

// NewRuntime opens the canonical WSL runtime directories and restores desired
// site state. Reconcile restarts sites that were running before agent restart.
func NewRuntime(root string) (*Runtime, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("agent: runtime root must be absolute")
	}
	for _, entry := range []struct {
		dir  string
		mode os.FileMode
	}{{root, 0o755}, {filepath.Join(root, "run"), 0o700}, {filepath.Join(root, "logs"), 0o750}, {filepath.Join(root, "sites"), 0o750}} {
		dir := entry.dir
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("agent: create runtime directory %s: %w", dir, err)
		}
		if err := os.Chmod(dir, entry.mode); err != nil {
			return nil, fmt.Errorf("agent: protect runtime directory %s: %w", dir, err)
		}
	}
	runtime := &Runtime{
		root:                root,
		statePath:           filepath.Join(root, "run", "sites.json"),
		ports:               NewPortAllocator(),
		logs:                newLogHub(filepath.Join(root, "logs")),
		sites:               make(map[string]*runtimeSite),
		runtimes:            make(map[string]agentproto.RuntimeResponse),
		termTickets:         make(map[string]*terminalTicket),
		terminalIdleTimeout: 30 * time.Minute,
		terminalMaxDuration: 4 * time.Hour,
	}
	runtime.loadRuntimes()
	data, err := os.ReadFile(runtime.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return runtime, nil
	}
	if err != nil {
		return nil, fmt.Errorf("agent: read runtime state: %w", err)
	}
	var saved persistedRuntime
	if err := json.Unmarshal(data, &saved); err != nil || saved.Version != 1 {
		return nil, fmt.Errorf("agent: invalid runtime state at %s", runtime.statePath)
	}
	for _, item := range saved.Sites {
		if err := validateSiteID(item.Spec.ID); err != nil {
			return nil, fmt.Errorf("agent: invalid persisted site: %w", err)
		}
		if item.Spec.HTTPPort > 0 {
			if err := runtime.ports.Restore(item.Spec.ID, "http", item.Spec.HTTPPort); err != nil {
				item.Spec.HTTPPort = 0
			}
		}
		if item.Spec.FastCGIPort > 0 {
			if err := runtime.ports.Restore(item.Spec.ID, "fastcgi", item.Spec.FastCGIPort); err != nil {
				item.Spec.FastCGIPort = 0
			}
		}
		site := &runtimeSite{
			spec:           cloneAgentSiteSpec(item.Spec),
			documentRoot:   item.DocumentRoot,
			desiredRunning: item.DesiredRunning,
			status:         "stopped",
			activeGen:      item.ActiveGen,
			phpGenerations: make(map[string]*phpGeneration),
		}
		if item.Spec.Kind == "php" && item.Spec.FastCGIPort > 0 {
			gen := item.ActiveGen
			if gen == "" {
				gen = "default"
			}
			fpmPath := filepath.Join(root, "sites", item.Spec.ID, "fpm.conf")
			if gen != "default" {
				fpmPath = filepath.Join(root, "sites", item.Spec.ID, gen, "php-fpm.conf")
			}
			site.phpGenerations[gen] = &phpGeneration{
				spec:         cloneAgentSiteSpec(item.Spec),
				documentRoot: item.DocumentRoot,
				fpmPath:      fpmPath,
				port:         item.Spec.FastCGIPort,
			}
		}
		runtime.sites[item.Spec.ID] = site
	}
	return runtime, nil
}

// PrepareSite validates and stages a static or Node site without starting it.
func (r *Runtime) PrepareSite(ctx context.Context, spec agentproto.SiteSpec) (agentproto.SitePrepareResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return agentproto.SitePrepareResponse{}, err
	}
	if spec.Kind != "static" && spec.Kind != "node" {
		return agentproto.SitePrepareResponse{}, fmt.Errorf("agent: site runtime does not support kind %q", spec.Kind)
	}
	if err := validateSiteID(spec.ID); err != nil {
		return agentproto.SitePrepareResponse{}, err
	}
	if spec.FrontController {
		return agentproto.SitePrepareResponse{}, errors.New("agent: front controller is only supported for PHP sites")
	}
	var docroot string
	if spec.Kind == "node" {
		if strings.TrimSpace(spec.Command) == "" {
			return agentproto.SitePrepareResponse{}, fmt.Errorf("agent: node site %q requires a command", spec.ID)
		}
		if strings.TrimSpace(spec.RuntimeVersion) == "" {
			return agentproto.SitePrepareResponse{}, fmt.Errorf("agent: node site %q requires a runtime version", spec.ID)
		}
		if strings.TrimSpace(spec.BackendPath) == "" || !filepath.IsAbs(spec.BackendPath) {
			return agentproto.SitePrepareResponse{}, errors.New("agent: node site backend_path must be an absolute WSL path")
		}
	} else {
		var err error
		docroot, err = resolveStaticDocumentRoot(spec)
		if err != nil {
			return agentproto.SitePrepareResponse{}, err
		}
	}
	spec.Env = sanitizeSiteEnv(spec.Env)
	env, err := r.Environment(spec.Env)
	if err != nil {
		return agentproto.SitePrepareResponse{}, err
	}
	spec.Env = env
	if _, err := r.resolveDatabaseEnv(spec); err != nil {
		return agentproto.SitePrepareResponse{}, err
	}
	port, _, err := r.ports.Allocate(spec.ID, "http", spec.HTTPPort)
	if err != nil {
		return agentproto.SitePrepareResponse{}, err
	}
	spec.HTTPPort = port
	r.mu.Lock()
	if current := r.sites[spec.ID]; current != nil && (current.server != nil || current.nodeCmd != nil) {
		r.mu.Unlock()
		return agentproto.SitePrepareResponse{}, errors.New("agent: stop the site before changing its prepared configuration")
	}
	r.sites[spec.ID] = &runtimeSite{
		spec:         cloneAgentSiteSpec(spec),
		documentRoot: docroot,
		status:       "stopped",
	}
	if err := r.saveLocked(); err != nil {
		delete(r.sites, spec.ID)
		_ = r.ports.Release(spec.ID, 0)
		r.mu.Unlock()
		return agentproto.SitePrepareResponse{}, err
	}
	r.mu.Unlock()
	return agentproto.SitePrepareResponse{BackendHost: "127.0.0.1", HTTPPort: port, FastCGIPort: spec.FastCGIPort}, nil
}

// Environment adds the managed CA bundle to site or terminal environments
// when provisioned, preserving caller values unrelated to certificate trust.
func (r *Runtime) Environment(base map[string]string) (map[string]string, error) {
	path := filepath.Join(r.root, "certs", "ca-bundle.crt")
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		env := make(map[string]string, len(base))
		for key, value := range base {
			env[key] = value
		}
		return env, nil
	} else if err != nil {
		return nil, fmt.Errorf("agent: inspect CA bundle: %w", err)
	}
	return CAEnvironment(r.root, base)
}

// SetTerminalLimits applies the agent-configured idle and absolute terminal
// session limits. Non-positive values keep the defaults. Call it during
// startup, before the server begins accepting terminal connections.
func (r *Runtime) SetTerminalLimits(idle, maxDuration time.Duration) {
	if idle > 0 {
		r.terminalIdleTimeout = idle
	}
	if maxDuration > 0 {
		r.terminalMaxDuration = maxDuration
	}
}

// Reconcile restarts every site whose persisted desired state is running.
func (r *Runtime) Reconcile(ctx context.Context) error {
	r.mu.Lock()
	ids := make([]string, 0, len(r.sites))
	for id, site := range r.sites {
		if site.desiredRunning {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	r.mu.Unlock()
	for _, id := range ids {
		if err := ctxErr(ctx); err != nil {
			return err
		}
		r.mu.Lock()
		site := r.sites[id]
		kind := ""
		gen := ""
		if site != nil {
			kind = site.spec.Kind
			gen = site.activeGen
		}
		r.mu.Unlock()

		if kind == "php" {
			if _, err := r.StartPHP(ctx, id, agentproto.PHPActionRequest{Generation: gen}); err != nil {
				r.logs.append(id, fmt.Sprintf("reconcile failed: %v", err))
				r.mu.Lock()
				if s := r.sites[id]; s != nil {
					s.status = "error"
				}
				r.mu.Unlock()
			}
		} else {
			if _, err := r.StartSite(ctx, id); err != nil {
				r.logs.append(id, fmt.Sprintf("reconcile failed: %v", err))
				r.mu.Lock()
				if s := r.sites[id]; s != nil {
					s.status = "error"
				}
				r.mu.Unlock()
			}
		}
	}
	return nil
}

func (r *Runtime) saveLocked() error {
	state := persistedRuntime{Version: 1}
	for _, site := range r.sites {
		state.Sites = append(state.Sites, persistedSite{
			Spec:           cloneAgentSiteSpec(site.spec),
			DocumentRoot:   site.documentRoot,
			DesiredRunning: site.desiredRunning,
			ActiveGen:      site.activeGen,
		})
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("agent: encode runtime state: %w", err)
	}
	if err := writePrivateFile(r.statePath, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("agent: save runtime state: %w", err)
	}
	return nil
}

func (r *Runtime) site(id string) (*runtimeSite, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	site := r.sites[id]
	if site == nil {
		return nil, fmt.Errorf("%w: %s", ErrSiteNotFound, id)
	}
	return site, nil
}

// ReleasePorts releases an allocation and forgets the prepared site when its
// HTTP listener port is released. A running site cannot lose its owned port.
func (r *Runtime) ReleasePorts(siteID string, port int) error {
	r.mu.Lock()
	site := r.sites[siteID]
	if site != nil && (site.server != nil || site.nodeCmd != nil) && (port == 0 || port == site.spec.HTTPPort) {
		r.mu.Unlock()
		return errors.New("agent: stop site before releasing its HTTP port")
	}
	if err := r.ports.Release(siteID, port); err != nil {
		r.mu.Unlock()
		return err
	}
	if site != nil && (port == 0 || port == site.spec.HTTPPort || port == site.spec.FastCGIPort) {
		delete(r.sites, siteID)
	}
	if site != nil {
		if err := r.saveLocked(); err != nil {
			r.mu.Unlock()
			return err
		}
	}
	r.mu.Unlock()
	return nil
}

func validateSiteID(id string) error {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) || strings.ContainsRune(id, 0) {
		return errors.New("agent: invalid site ID")
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r)) {
			return fmt.Errorf("agent: invalid site ID %q", id)
		}
	}
	return nil
}

// nodeRuntimePathLocked resolves a configured Node version line or exact
// version to an installed runtime build. The caller must hold r.mu.
func (r *Runtime) nodeRuntimePathLocked(version string) (string, error) {
	version = strings.TrimPrefix(strings.TrimSpace(version), "node@")
	if version == "" {
		return "", errors.New("agent: node site requires a runtime version")
	}
	type candidate struct {
		version string
		path    string
	}
	var candidates []candidate
	for id, registered := range r.runtimes {
		if registered.Kind != "node" {
			continue
		}
		if registered.Version != version && !strings.HasPrefix(registered.Version, version+".") && id != "node@"+version {
			continue
		}
		installPath := strings.TrimSpace(registered.Path)
		if installPath == "" {
			installPath = filepath.Join(r.root, "runtimes", id)
		}
		candidates = append(candidates, candidate{version: registered.Version, path: installPath})
	}
	if len(candidates) == 0 {
		// Runtime state can be missing after an upgrade even though the build
		// directory is intact. Fall back to the canonical runtime directory.
		entries, _ := os.ReadDir(filepath.Join(r.root, "runtimes"))
		for _, entry := range entries {
			name := entry.Name()
			if !entry.IsDir() || !strings.HasPrefix(name, "node@") {
				continue
			}
			found := strings.TrimPrefix(name, "node@")
			if found != version && !strings.HasPrefix(found, version+".") {
				continue
			}
			candidates = append(candidates, candidate{version: found, path: filepath.Join(r.root, "runtimes", name)})
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].version > candidates[j].version })
	for _, candidate := range candidates {
		if err := validateRuntimePath(r.root, candidate.path); err != nil {
			continue
		}
		info, err := os.Stat(filepath.Join(candidate.path, "bin", "node"))
		if err == nil && !info.IsDir() {
			return candidate.path, nil
		}
	}
	return "", fmt.Errorf("agent: no installed Node runtime matches version %q", version)
}

// sitePathEnv prepends the site's PHP and Node runtime bins to PATH so
// terminal and exec sessions resolve the exact builds the site serves with.
// The managed distro ships an empty /etc/environment, so an unset site PATH
// falls back to the agent's inherited PATH.
func (r *Runtime) sitePathEnv(spec agentproto.SiteSpec, existing string) string {
	if strings.TrimSpace(existing) == "" {
		existing = os.Getenv("PATH")
	}
	var dirs []string
	addExecutableDir := func(dir, marker string) {
		if strings.TrimSpace(dir) == "" {
			return
		}
		info, err := os.Stat(filepath.Join(dir, marker))
		if err != nil || info.IsDir() {
			return
		}
		dirs = append(dirs, dir)
	}
	if spec.RuntimeVersion != "" {
		r.mu.Lock()
		nodeRoot, err := r.nodeRuntimePathLocked(spec.RuntimeVersion)
		r.mu.Unlock()
		if err == nil {
			addExecutableDir(filepath.Join(nodeRoot, "bin"), "node")
		}
	}
	if spec.PHPBuild != "" {
		addExecutableDir(filepath.Join(r.root, "runtimes", spec.PHPBuild, "bin"), "php")
	}
	addExecutableDir(filepath.Join(r.root, "tools", "php-default", "bin"), "php")

	seen := make(map[string]bool, len(dirs))
	list := make([]string, 0, len(dirs)+1)
	for _, dir := range dirs {
		if !seen[dir] {
			seen[dir] = true
			list = append(list, dir)
		}
	}
	if strings.TrimSpace(existing) != "" {
		list = append(list, existing)
	}
	return strings.Join(list, ":")
}

func cloneAgentSiteSpec(spec agentproto.SiteSpec) agentproto.SiteSpec {
	spec.Env = sanitizeSiteEnv(spec.Env)
	return spec
}

// sanitizeSiteEnv removes reserved database keys from caller-supplied site
// env. Link values come only from DatabaseLinks and the agent secret tree; a
// daemon that still sends resolved DB_* values must not get them persisted to
// sites.json or rendered into php-fpm.conf.
func sanitizeSiteEnv(env map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	clean := make(map[string]string, len(env))
	for key, value := range env {
		upper := strings.ToUpper(key)
		if upper == "DATABASE_URL" || strings.HasPrefix(upper, "DB_") {
			continue
		}
		clean[key] = value
	}
	if len(clean) == 0 {
		return nil
	}
	return clean
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

var (
	agentDatabaseUserIDPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	agentExistingUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
)

// resolveDatabaseEnv turns the site's non-secret database links into the
// DB_*/DATABASE_URL values. Secrets are read from the agent-owned tree and are
// kept in memory only.
func (r *Runtime) resolveDatabaseEnv(spec agentproto.SiteSpec) (map[string]string, error) {
	env := make(map[string]string)
	for index, link := range spec.DatabaseLinks {
		if strings.TrimSpace(link.Database) == "" {
			continue
		}
		if err := validateAgentDatabaseName(link.Database); err != nil {
			return nil, fmt.Errorf("agent: invalid database link %q: %w", link.Database, err)
		}
		serviceID := canonicalAgentServiceID(link.ServiceID)
		if !validAgentDatabaseService(serviceID) {
			return nil, fmt.Errorf("agent: invalid database service %q", link.ServiceID)
		}
		expectedEngine := agentEngineForService(serviceID)
		engine := strings.TrimSpace(link.Engine)
		if engine == "" {
			engine = expectedEngine
		}
		if expectedEngine == "" || engine != expectedEngine {
			return nil, fmt.Errorf("agent: link engine %q does not match service %q", engine, serviceID)
		}
		if !agentExistingUsernamePattern.MatchString(link.Username) {
			return nil, fmt.Errorf("agent: invalid database username %q", link.Username)
		}
		port := link.Port
		if port <= 0 || port > 65535 {
			port = agentDefaultDatabasePort(engine)
		}
		prefix := "DB_"
		if !link.Primary {
			slug := agentDatabaseSlug(link.Database)
			if slug == "" {
				slug = "LINK" + strconv.Itoa(index+1)
			}
			prefix = "DB_" + slug + "_"
		}
		env[prefix+"CONNECTION"] = engine
		env[prefix+"HOST"] = "127.0.0.1"
		env[prefix+"PORT"] = strconv.Itoa(port)
		env[prefix+"DATABASE"] = link.Database
		env[prefix+"USERNAME"] = link.Username
		if link.PasswordManaged {
			if !agentDatabaseUserIDPattern.MatchString(link.UserID) {
				return nil, fmt.Errorf("agent: invalid database user ID for site %q", spec.ID)
			}
			secret, err := r.readAgentDatabaseSecret(serviceID, link.UserID)
			if err != nil {
				return nil, fmt.Errorf("agent: resolve database secret for site %q: %w", spec.ID, err)
			}
			env[prefix+"PASSWORD"] = secret
			connectionURL := agentDatabaseConnectionURL(engine, link.Username, secret, port, link.Database)
			if link.Primary {
				env["DATABASE_URL"] = connectionURL
			} else {
				env[prefix+"URL"] = connectionURL
			}
		} else {
			delete(env, prefix+"PASSWORD")
			delete(env, prefix+"URL")
			if link.Primary {
				delete(env, "DATABASE_URL")
			}
		}
	}
	return env, nil
}

// databaseEnvFor returns the in-memory resolved environment, or resolves the
// persisted links on demand after an agent restart. It never mutates the site,
// so callers that do not hold r.mu are safe.
func (r *Runtime) databaseEnvFor(spec agentproto.SiteSpec) (map[string]string, error) {
	if len(spec.DatabaseLinks) == 0 {
		return nil, nil
	}
	// Resolve on every start/terminal/build so a password rotation applies on
	// the explicit restart the dashboard prompts for. Callers pass a snapshot
	// taken under r.mu.
	return r.resolveDatabaseEnv(spec)
}

func environmentPairs(values map[string]string) []string {
	pairs := make([]string, 0, len(values))
	for key, value := range values {
		pairs = append(pairs, key+"="+value)
	}
	sort.Strings(pairs)
	return pairs
}

func (r *Runtime) readAgentDatabaseSecret(serviceID, userID string) (string, error) {
	if !agentDatabaseUserIDPattern.MatchString(userID) {
		return "", errors.New("agent: invalid database user ID")
	}
	serviceID = canonicalAgentServiceID(serviceID)
	if !validAgentDatabaseService(serviceID) {
		return "", errors.New("agent: invalid database service")
	}
	servicesRoot := filepath.Join(r.root, "services")
	serviceRoot := filepath.Join(servicesRoot, serviceID)
	secretsDir := filepath.Join(serviceRoot, "secrets")
	dir := filepath.Join(secretsDir, "database-users")
	for _, entry := range []struct {
		path string
		mode os.FileMode
	}{
		{servicesRoot, 0},
		{serviceRoot, 0},
		{secretsDir, 0o700},
		{dir, 0o700},
	} {
		info, err := os.Lstat(entry.path)
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("agent: unsafe database secret directory")
		}
		if entry.mode != 0 && runtime.GOOS != "windows" && info.Mode().Perm() != entry.mode {
			return "", fmt.Errorf("agent: database secret directory %s has mode %04o", entry.path, info.Mode().Perm())
		}
	}
	path := filepath.Join(dir, userID)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("agent: database secret is not a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		return "", fmt.Errorf("agent: database secret %s has mode %04o", userID, info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value := strings.TrimRight(string(data), "\r\n")
	if value == "" {
		return "", errors.New("agent: database secret is empty")
	}
	return value, nil
}

func canonicalAgentServiceID(id string) string {
	id = strings.TrimSpace(id)
	if id == "postgresql" {
		return "postgres"
	}
	return id
}

func validAgentDatabaseService(id string) bool {
	switch id {
	case "mysql", "mariadb", "postgres", "redis":
		return true
	default:
		return false
	}
}

func agentEngineForService(serviceID string) string {
	switch canonicalAgentServiceID(serviceID) {
	case "mysql":
		return "mysql"
	case "mariadb":
		return "mariadb"
	case "postgres":
		return "postgres"
	case "redis":
		return "redis"
	default:
		return ""
	}
}

func agentDefaultDatabasePort(engine string) int {
	switch engine {
	case "mysql", "mariadb":
		return 3306
	case "postgres":
		return 5432
	case "redis":
		return 6379
	default:
		return 0
	}
}

func agentDatabaseSlug(database string) string {
	var builder strings.Builder
	for _, r := range strings.ToUpper(database) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	return strings.Trim(builder.String(), "_")
}

func agentDatabaseConnectionURL(engine, username, password string, port int, database string) string {
	scheme := "mysql"
	switch engine {
	case "postgres":
		scheme = "postgresql"
	case "redis":
		scheme = "redis"
	}
	user := url.UserPassword(username, password)
	return scheme + "://" + user.String() + "@" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) + "/" + url.PathEscape(database)
}

func validateAgentDatabaseName(name string) error {
	if len(name) == 0 || len(name) > 63 {
		return errors.New("agent: invalid database name")
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return errors.New("agent: invalid database name")
		}
	}
	return nil
}
