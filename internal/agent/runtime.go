package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

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
		root:        root,
		statePath:   filepath.Join(root, "run", "sites.json"),
		ports:       NewPortAllocator(),
		logs:        newLogHub(filepath.Join(root, "logs")),
		sites:       make(map[string]*runtimeSite),
		runtimes:    make(map[string]agentproto.RuntimeResponse),
		termTickets: make(map[string]*terminalTicket),
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
	env, err := r.Environment(spec.Env)
	if err != nil {
		return agentproto.SitePrepareResponse{}, err
	}
	spec.Env = env
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
	r.sites[spec.ID] = &runtimeSite{spec: cloneAgentSiteSpec(spec), documentRoot: docroot, status: "stopped"}
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
	if spec.Env != nil {
		env := make(map[string]string, len(spec.Env))
		for key, value := range spec.Env {
			env[key] = value
		}
		spec.Env = env
	}
	return spec
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
