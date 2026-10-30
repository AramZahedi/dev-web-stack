package agent

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	runtimemgr "github.com/AramZahedi/dev-web-stack/internal/runtime"
)

type phpGeneration struct {
	spec         agentproto.SiteSpec
	documentRoot string
	fpmPath      string
	port         int
	process      *fpmProcess
}

// runtimeBinaryPath returns the relative path of the command a runtime build
// must provide. The agent refuses to register a build whose binary is missing.
func runtimeBinaryPath(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "php":
		return "bin/php"
	case "node":
		return "bin/node"
	default:
		return ""
	}
}

// CheckDebian12PHPCompatibility verifies that a PHP build contains required
// binaries and passes execution checks for Debian 12 / WSL compatibility.
func CheckDebian12PHPCompatibility(ctx context.Context, buildPath string) error {
	if strings.TrimSpace(buildPath) == "" {
		return errors.New("agent: php compatibility gate: build path is empty")
	}
	phpBin := filepath.Join(buildPath, "bin", "php")
	fpmBin := filepath.Join(buildPath, "sbin", "php-fpm")

	for _, bin := range []string{phpBin, fpmBin} {
		info, err := os.Stat(bin)
		if err != nil {
			return fmt.Errorf("agent: php compatibility gate: binary %s missing: %w", bin, err)
		}
		if info.IsDir() {
			return fmt.Errorf("agent: php compatibility gate: binary %s is a directory", bin)
		}
	}

	if runtime.GOOS == "windows" {
		return nil
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, phpBin, "-v")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("agent: php compatibility gate: php -v failed: %w (output: %s)", err, string(out))
	}

	cmdFPM := exec.CommandContext(timeoutCtx, fpmBin, "-v")
	if out, err := cmdFPM.CombinedOutput(); err != nil {
		return fmt.Errorf("agent: php compatibility gate: php-fpm -v failed: %w (output: %s)", err, string(out))
	}

	return nil
}

var runtimeArtifactChecksum = regexp.MustCompile(`^[0-9a-f]{64}$`)

const (
	runtimeArtifactHost     = "nodejs.org"
	runtimeArtifactNodeKind = "node"
	runtimeArtifactBuild    = "linux-x64"
)

// runtimeArtifactEntry validates a daemon-supplied pinned artifact and converts
// it into the catalog entry shape the installer already understands. The agent
// never trusts the daemon for any of these properties: the downloader re-hashes
// the archive and checks its size before extraction.
func runtimeArtifactEntry(artifact agentproto.RuntimeArtifact) (catalog.Entry, error) {
	if strings.TrimSpace(artifact.ID) == "" ||
		strings.TrimSpace(artifact.Kind) == "" ||
		strings.TrimSpace(artifact.Version) == "" ||
		strings.TrimSpace(artifact.URL) == "" ||
		strings.TrimSpace(artifact.SHA256) == "" {
		return catalog.Entry{}, errors.New("agent: runtime artifact is missing required fields")
	}
	if artifact.Size <= 0 {
		return catalog.Entry{}, fmt.Errorf("agent: runtime artifact size %d must be positive", artifact.Size)
	}
	if artifact.Size > maxRuntimeArchiveBytes {
		return catalog.Entry{}, fmt.Errorf("agent: runtime artifact size %d exceeds the %d byte archive limit", artifact.Size, maxRuntimeArchiveBytes)
	}

	kind := strings.ToLower(strings.TrimSpace(artifact.Kind))
	if kind != runtimeArtifactNodeKind {
		return catalog.Entry{}, fmt.Errorf("agent: runtime artifact kind %q is unsupported", artifact.Kind)
	}

	id := strings.TrimSpace(artifact.ID)
	if strings.Contains(id, "..") || strings.ContainsAny(id, `/\`) {
		return catalog.Entry{}, fmt.Errorf("agent: runtime artifact id %q contains a path element", id)
	}
	parsed, err := catalog.ParseID(id)
	if err != nil {
		return catalog.Entry{}, fmt.Errorf("agent: invalid runtime artifact id: %w", err)
	}
	version := strings.TrimSpace(artifact.Version)
	// ParseID rejects revisions on node ids and non-semver versions, so matching
	// the parsed id against kind and version is the complete check here.
	if parsed.Kind != kind || parsed.Version != version || parsed.Revision != 0 {
		return catalog.Entry{}, fmt.Errorf("agent: runtime artifact id %q does not match kind %q and version %q", id, kind, artifact.Version)
	}

	build := strings.TrimSpace(artifact.Build)
	if build == "" {
		build = runtimeArtifactBuild
	}
	if build != runtimeArtifactBuild {
		return catalog.Entry{}, fmt.Errorf("agent: runtime artifact build %q is unsupported (want %s)", artifact.Build, runtimeArtifactBuild)
	}

	if !runtimeArtifactChecksum.MatchString(artifact.SHA256) {
		return catalog.Entry{}, fmt.Errorf("agent: runtime artifact sha256 %q must be 64 lowercase hex characters", artifact.SHA256)
	}

	rawURL := strings.TrimSpace(artifact.URL)
	if strings.ContainsRune(rawURL, 0) {
		return catalog.Entry{}, errors.New("agent: runtime artifact url contains NUL")
	}
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return catalog.Entry{}, fmt.Errorf("agent: invalid runtime artifact url: %w", err)
	}
	if parsedURL.Scheme != "https" {
		return catalog.Entry{}, fmt.Errorf("agent: runtime artifact url %q must use https", rawURL)
	}
	if parsedURL.User != nil {
		return catalog.Entry{}, fmt.Errorf("agent: runtime artifact url %q must not contain user info", rawURL)
	}
	if !strings.EqualFold(parsedURL.Hostname(), runtimeArtifactHost) {
		return catalog.Entry{}, fmt.Errorf("agent: runtime artifact url host %q must be %s", parsedURL.Hostname(), runtimeArtifactHost)
	}

	return catalog.Entry{
		ID:      id,
		Kind:    kind,
		Version: version,
		Build:   build,
		URL:     rawURL,
		SHA256:  artifact.SHA256,
		Size:    artifact.Size,
	}, nil
}

// downloadRuntimeArchive is a test seam for the verified download/extraction
// step, mirroring removeRuntimePath below.
var downloadRuntimeArchive = downloadAndExtractArchive

// InstallRuntime downloads/registers an installed runtime build from the catalog
// or from a daemon-supplied pinned artifact.
func (r *Runtime) InstallRuntime(ctx context.Context, req agentproto.RuntimeRequest) (agentproto.RuntimeResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return agentproto.RuntimeResponse{}, err
	}

	var match *catalog.Entry
	if req.Artifact != nil {
		entry, err := runtimeArtifactEntry(*req.Artifact)
		if err != nil {
			return agentproto.RuntimeResponse{}, err
		}
		match = &entry
	} else {
		cat, err := catalog.Load()
		if err != nil {
			return agentproto.RuntimeResponse{}, fmt.Errorf("agent: load catalog: %w", err)
		}

		buildID := strings.TrimSpace(req.BuildID)
		if buildID != "" {
			for i := range cat.Entries {
				if cat.Entries[i].ID == buildID {
					match = &cat.Entries[i]
					break
				}
			}
		} else if req.Kind != "" && req.Version != "" {
			for i := range cat.Entries {
				if cat.Entries[i].Kind == req.Kind && (cat.Entries[i].Version == req.Version || catalog.LineOf(cat.Entries[i].Version) == req.Version) {
					match = &cat.Entries[i]
					break
				}
			}
		}

		if match == nil {
			return agentproto.RuntimeResponse{}, fmt.Errorf("agent: runtime not found in catalog: id=%q kind=%q version=%q", req.BuildID, req.Kind, req.Version)
		}
	}

	targetDir := filepath.Join(r.root, "runtimes", match.ID)
	if filepath.Base(targetDir) != match.ID || strings.ContainsAny(match.ID, `/\`) {
		return agentproto.RuntimeResponse{}, fmt.Errorf("agent: invalid catalog runtime ID %q", match.ID)
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return agentproto.RuntimeResponse{}, fmt.Errorf("agent: stage runtime directory: %w", err)
	}

	// Serialise installs so two requests for one build cannot download and
	// extract into the same destination concurrently.
	r.runtimeMu.Lock()
	defer r.runtimeMu.Unlock()

	// Every downloadable kind is staged the same way: verify the pinned
	// archive, extract to a private staging tree, then activate. PHP then gets
	// its Debian 12 runnable-binary gate.
	binaryRel := runtimeBinaryPath(match.Kind)
	if binaryRel == "" {
		return agentproto.RuntimeResponse{}, fmt.Errorf("agent: runtime %q has unsupported kind %q", match.ID, match.Kind)
	}
	binaryPath := filepath.Join(targetDir, filepath.FromSlash(binaryRel))
	if runtime.GOOS != "windows" && match.URL != "" && os.Getenv("DEVSTACK_NO_DOWNLOAD") == "" {
		if _, err := os.Stat(binaryPath); errors.Is(err, os.ErrNotExist) {
			// A failed download or checksum mismatch must fail the install.
			// The previous code discarded this error and returned a successful
			// response for a runtime that does not exist.
			if err := downloadRuntimeArchive(ctx, match.URL, match.SHA256, match.Size, targetDir); err != nil {
				return agentproto.RuntimeResponse{}, fmt.Errorf("agent: install runtime %q: %w", match.ID, err)
			}
		}
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(binaryPath)
		if err != nil {
			return agentproto.RuntimeResponse{}, fmt.Errorf("agent: install runtime %q: %s is missing: %w", match.ID, binaryRel, err)
		}
		if info.IsDir() || info.Mode().Perm()&0o111 == 0 {
			return agentproto.RuntimeResponse{}, fmt.Errorf("agent: install runtime %q: %s is not executable", match.ID, binaryRel)
		}
	}
	if match.Kind == "php" {
		if err := runtimemgr.EnsurePHPBaseIni(targetDir); err != nil {
			return agentproto.RuntimeResponse{}, fmt.Errorf("agent: install runtime %q: write PHP base config: %w", match.ID, err)
		}
		if _, err := os.Stat(filepath.Join(targetDir, "bin", "php")); err == nil {
			if err := CheckDebian12PHPCompatibility(ctx, targetDir); err != nil {
				return agentproto.RuntimeResponse{}, err
			}
		}
	}

	resp := agentproto.RuntimeResponse{
		ID:       match.ID,
		Kind:     match.Kind,
		Version:  match.Version,
		Backend:  "wsl",
		Path:     targetDir,
		Checksum: match.SHA256,
	}

	r.mu.Lock()
	if r.runtimes == nil {
		r.runtimes = make(map[string]agentproto.RuntimeResponse)
	}
	r.runtimes[match.ID] = resp
	_ = r.saveRuntimesLocked()
	r.mu.Unlock()

	return resp, nil
}

// RemoveRuntime deletes a registered runtime. The request ID is used only as a
// map key; the path recorded at install time is the only path ever removed.
// Unknown IDs are an idempotent no-op.
// removeRuntimePath is a test seam for the two-phase runtime delete.
var removeRuntimePath = os.RemoveAll

func (r *Runtime) RemoveRuntime(ctx context.Context, req agentproto.RuntimeRequest) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	id := strings.TrimSpace(req.BuildID)
	if id == "" {
		return errors.New("agent: build id is required")
	}

	// Serialise with InstallRuntime so a removal cannot delete a build while
	// its archive is being extracted into the same directory.
	r.runtimeMu.Lock()
	defer r.runtimeMu.Unlock()

	r.mu.Lock()
	registered, ok := r.runtimes[id]
	if !ok {
		r.mu.Unlock()
		return nil
	}
	targetDir := strings.TrimSpace(registered.Path)
	if targetDir == "" {
		if _, err := catalog.ParseID(id); err != nil {
			r.mu.Unlock()
			return fmt.Errorf("agent: remove runtime %q: %w", id, err)
		}
		targetDir = filepath.Join(r.root, "runtimes", id)
	}
	if err := validateRuntimePath(r.root, targetDir); err != nil {
		r.mu.Unlock()
		return fmt.Errorf("agent: remove runtime %q: %w", id, err)
	}
	// Delete the files before forgetting the row; a failed delete must remain
	// retryable instead of leaving an unreachable directory.
	if err := removeRuntimePath(targetDir); err != nil {
		r.mu.Unlock()
		return fmt.Errorf("agent: remove runtime %q files: %w", id, err)
	}
	delete(r.runtimes, id)
	_ = r.saveRuntimesLocked()
	r.mu.Unlock()
	return nil
}

// validateRuntimePath confines a registered runtime path to <root>/runtimes.
func validateRuntimePath(root, target string) error {
	if root == "" || target == "" {
		return errors.New("agent: runtime path is empty")
	}
	if !filepath.IsAbs(target) {
		return fmt.Errorf("agent: runtime path %q is not absolute", target)
	}
	base, err := filepath.Abs(filepath.Join(root, "runtimes"))
	if err != nil {
		return fmt.Errorf("agent: resolve runtime root: %w", err)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("agent: resolve runtime path: %w", err)
	}
	rel, err := filepath.Rel(base, absTarget)
	if err != nil {
		return fmt.Errorf("agent: compare runtime path: %w", err)
	}
	if rel == "." || rel == ".." || filepath.IsAbs(rel) ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("agent: runtime path %q escapes %s", absTarget, base)
	}
	return nil
}

// ListRuntimes lists all installed runtimes.
func (r *Runtime) ListRuntimes(ctx context.Context) (agentproto.RuntimeListResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return agentproto.RuntimeListResponse{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	list := make([]agentproto.RuntimeResponse, 0, len(r.runtimes))
	for _, rt := range r.runtimes {
		list = append(list, rt)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].ID < list[j].ID
	})
	return agentproto.RuntimeListResponse{Runtimes: list}, nil
}

// PreparePHP stages a candidate PHP generation and generates per-site FPM configuration in WSL.
func (r *Runtime) PreparePHP(ctx context.Context, id string, req agentproto.PHPPrepareRequest) (agentproto.SitePrepareResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return agentproto.SitePrepareResponse{}, err
	}
	if err := validateSiteID(id); err != nil {
		return agentproto.SitePrepareResponse{}, err
	}
	if req.Site.Kind != "" && req.Site.Kind != "php" {
		return agentproto.SitePrepareResponse{}, fmt.Errorf("agent: PHP runtime does not support kind %q", req.Site.Kind)
	}
	req.Site.ID = id
	req.Site.Kind = "php"

	gen := strings.TrimSpace(req.Generation)
	if gen == "" {
		gen = "default"
	}
	if strings.ContainsAny(gen, `/\`) || strings.Contains(gen, "..") {
		return agentproto.SitePrepareResponse{}, fmt.Errorf("agent: invalid generation name %q", gen)
	}

	docroot, err := resolvePHPDocumentRoot(req.Site)
	if err != nil {
		return agentproto.SitePrepareResponse{}, err
	}

	req.Site.Env, err = r.Environment(req.Site.Env)
	if err != nil {
		return agentproto.SitePrepareResponse{}, err
	}

	port, _, err := r.ports.Allocate(id, "fastcgi", req.Site.FastCGIPort)
	if err != nil {
		return agentproto.SitePrepareResponse{}, err
	}
	req.Site.FastCGIPort = port

	fpmBytes, fpmPath, err := generateFPMConfig(r.root, req.Site, docroot)
	if err != nil {
		_ = r.ports.Release(id, port)
		return agentproto.SitePrepareResponse{}, err
	}

	siteDir := filepath.Join(r.root, "sites", id)
	if err := os.MkdirAll(siteDir, 0o750); err != nil {
		_ = r.ports.Release(id, port)
		return agentproto.SitePrepareResponse{}, fmt.Errorf("agent: create site directory: %w", err)
	}

	if err := writePrivateFile(fpmPath, fpmBytes, 0o600); err != nil {
		_ = r.ports.Release(id, port)
		return agentproto.SitePrepareResponse{}, fmt.Errorf("agent: write fpm.conf: %w", err)
	}

	if gen != "default" {
		genDir := filepath.Join(siteDir, gen)
		if err := os.MkdirAll(genDir, 0o750); err != nil {
			_ = r.ports.Release(id, port)
			return agentproto.SitePrepareResponse{}, fmt.Errorf("agent: create generation directory: %w", err)
		}
		if err := writePrivateFile(filepath.Join(genDir, "php-fpm.conf"), fpmBytes, 0o600); err != nil {
			_ = r.ports.Release(id, port)
			return agentproto.SitePrepareResponse{}, fmt.Errorf("agent: write generation php-fpm.conf: %w", err)
		}
	}

	r.mu.Lock()
	site := r.sites[id]
	if site == nil {
		site = &runtimeSite{
			spec:           cloneAgentSiteSpec(req.Site),
			documentRoot:   docroot,
			status:         "stopped",
			phpGenerations: make(map[string]*phpGeneration),
		}
		r.sites[id] = site
	} else {
		site.spec = cloneAgentSiteSpec(req.Site)
		site.documentRoot = docroot
		if site.phpGenerations == nil {
			site.phpGenerations = make(map[string]*phpGeneration)
		}
	}

	var liveProcess *fpmProcess
	if existing := site.phpGenerations[gen]; existing != nil && existing.port == port &&
		existing.process != nil && existing.process.running() {
		// A re-prepare of the active generation (for example devstack php --site)
		// must keep the live master attached. Replacing the generation object
		// would orphan the process and make the site report stopped.
		liveProcess = existing.process
	}
	site.phpGenerations[gen] = &phpGeneration{
		spec:         cloneAgentSiteSpec(req.Site),
		documentRoot: docroot,
		fpmPath:      fpmPath,
		port:         port,
		process:      liveProcess,
	}

	if err := r.saveLocked(); err != nil {
		r.mu.Unlock()
		return agentproto.SitePrepareResponse{}, err
	}
	r.mu.Unlock()

	return agentproto.SitePrepareResponse{
		BackendHost: "127.0.0.1",
		FastCGIPort: port,
		HTTPPort:    req.Site.HTTPPort,
	}, nil
}

// StartPHP starts or swaps the PHP-FPM process for a generation.
func (r *Runtime) StartPHP(ctx context.Context, id string, req agentproto.PHPActionRequest) (agentproto.SiteActionResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return agentproto.SiteActionResponse{}, err
	}
	gen := strings.TrimSpace(req.Generation)
	if gen == "" {
		gen = "default"
	}

	r.mu.Lock()
	site := r.sites[id]
	if site == nil {
		r.mu.Unlock()
		return agentproto.SiteActionResponse{}, fmt.Errorf("%w: %s", ErrSiteNotFound, id)
	}
	targetGen := site.phpGenerations[gen]
	if targetGen == nil {
		fpmPath := filepath.Join(r.root, "sites", id, "fpm.conf")
		if gen != "default" {
			fpmPath = filepath.Join(r.root, "sites", id, gen, "php-fpm.conf")
		}
		if _, err := os.Stat(fpmPath); err == nil && site.spec.FastCGIPort > 0 {
			targetGen = &phpGeneration{
				spec:         cloneAgentSiteSpec(site.spec),
				documentRoot: site.documentRoot,
				fpmPath:      fpmPath,
				port:         site.spec.FastCGIPort,
			}
			if site.phpGenerations == nil {
				site.phpGenerations = make(map[string]*phpGeneration)
			}
			site.phpGenerations[gen] = targetGen
		} else {
			r.mu.Unlock()
			return agentproto.SiteActionResponse{}, fmt.Errorf("agent: generation %q not prepared for site %q", gen, id)
		}
	}
	oldGenName := site.activeGen
	var oldProcess *fpmProcess
	if oldGenName != "" && site.phpGenerations[oldGenName] != nil {
		oldProcess = site.phpGenerations[oldGenName].process
	}
	r.mu.Unlock()

	// If swapping on the same port, stop the old process first so the new one can bind.
	if oldProcess != nil && oldProcess.port == targetGen.port {
		oldProcess.stop()
		oldProcess = nil
	}

	// Launch target FPM process
	proc, err := r.startFPM(ctx, id, gen, targetGen.fpmPath, targetGen.spec.PHPIniPath, targetGen.spec.PHPBuildPath, targetGen.port)
	if err != nil {
		return agentproto.SiteActionResponse{}, err
	}

	// Stop old generation if on a different port
	if oldProcess != nil && oldProcess != proc {
		oldProcess.stop()
	}

	r.mu.Lock()
	targetGen.process = proc
	site.activeGen = gen
	site.status = "running"
	site.desiredRunning = true
	_ = r.saveLocked()
	r.mu.Unlock()

	r.logs.append(id, fmt.Sprintf("started PHP generation %q (PID %d on FastCGI port %d)", gen, proc.pid, targetGen.port))

	return agentproto.SiteActionResponse{
		Status: "running",
		PID:    proc.pid,
	}, nil
}

// StopPHP stops the requested PHP-FPM generation. An explicit generation that
// was never prepared is a no-op: a drain after a swap must never stop the newly
// published master. An empty generation stops every generation for the site.
// Only stopping the active generation can change the site's desired state.
func (r *Runtime) StopPHP(ctx context.Context, id string, req agentproto.PHPActionRequest) (agentproto.SiteActionResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return agentproto.SiteActionResponse{}, err
	}

	r.mu.Lock()
	site := r.sites[id]
	if site == nil {
		r.mu.Unlock()
		return agentproto.SiteActionResponse{}, fmt.Errorf("%w: %s", ErrSiteNotFound, id)
	}
	var processesToStop []*fpmProcess
	gen := strings.TrimSpace(req.Generation)
	if gen != "" {
		target := site.phpGenerations[gen]
		if target == nil {
			status := activePHPStatusLocked(site)
			r.mu.Unlock()
			return agentproto.SiteActionResponse{Status: status}, nil
		}
		if proc := target.process; proc != nil {
			processesToStop = append(processesToStop, proc)
			target.process = nil
		}
		if site.activeGen == gen {
			if next := nextRunningPHPGenerationLocked(site); next != "" {
				site.activeGen = next
				site.status = "running"
			} else {
				site.status = "stopped"
				site.desiredRunning = false
			}
		}
	} else {
		for _, g := range site.phpGenerations {
			if g.process != nil {
				processesToStop = append(processesToStop, g.process)
				g.process = nil
			}
		}
		site.status = "stopped"
		site.desiredRunning = false
	}
	_ = r.saveLocked()
	status := activePHPStatusLocked(site)
	r.mu.Unlock()

	for _, p := range processesToStop {
		p.stop()
	}

	if len(processesToStop) > 0 {
		r.logs.append(id, "stopped PHP-FPM processes")
	}
	return agentproto.SiteActionResponse{Status: status}, nil
}

// activePHPStatusLocked reports the state of the generation that backs the
// site. The caller must hold r.mu.
func activePHPStatusLocked(site *runtimeSite) string {
	gen := site.activeGen
	if gen == "" {
		gen = "default"
	}
	if target := site.phpGenerations[gen]; target != nil && target.process != nil && target.process.running() {
		return "running"
	}
	return "stopped"
}

// nextRunningPHPGenerationLocked returns another running generation to promote
// after the active one stops. The caller must hold r.mu.
func nextRunningPHPGenerationLocked(site *runtimeSite) string {
	names := make([]string, 0, len(site.phpGenerations))
	for name, gen := range site.phpGenerations {
		if name == site.activeGen || gen.process == nil || !gen.process.running() {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return names[len(names)-1]
}

func resolvePHPDocumentRoot(spec agentproto.SiteSpec) (string, error) {
	project := strings.TrimSpace(spec.BackendPath)
	if project == "" || !filepath.IsAbs(project) {
		return "", errors.New("agent: invalid project root: backend path must be an absolute WSL path")
	}
	project, err := filepath.Abs(project)
	if err != nil {
		return "", fmt.Errorf("agent: invalid project root: %w", err)
	}
	if err := rejectSymlinkComponents(project); err != nil {
		return "", fmt.Errorf("agent: unsafe project root: %w", err)
	}
	// Reuse the same containment rules as static sites. The previous
	// implementation only rejected a symlink as the final component, so a
	// document root reached through a symlinked intermediate directory could
	// escape the project.
	return resolveDocumentRoot(project, spec.DocumentRoot)
}

func (r *Runtime) saveRuntimesLocked() error {
	path := filepath.Join(r.root, "run", "runtimes.json")
	data, err := json.Marshal(r.runtimes)
	if err != nil {
		return err
	}
	return writePrivateFile(path, append(data, '\n'), 0o600)
}

func (r *Runtime) loadRuntimes() {
	path := filepath.Join(r.root, "run", "runtimes.json")
	data, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(data, &r.runtimes)
	}
	if r.runtimes == nil {
		r.runtimes = make(map[string]agentproto.RuntimeResponse)
	}
}

func downloadAndExtractArchive(ctx context.Context, url, expectedSHA256 string, expectedSize int64, destDir string) error {
	expected := strings.ToLower(strings.TrimSpace(expectedSHA256))
	if len(expected) != 64 {
		return errors.New("agent: runtime archive checksum is missing or malformed")
	}
	if _, err := hex.DecodeString(expected); err != nil {
		return fmt.Errorf("agent: runtime archive checksum is invalid: %w", err)
	}
	if expectedSize <= 0 || expectedSize > maxRuntimeArchiveBytes {
		return fmt.Errorf("agent: runtime archive size %d is outside the supported range", expectedSize)
	}
	if strings.TrimSpace(destDir) == "" {
		return errors.New("agent: runtime destination is empty")
	}

	curlBin, err := exec.LookPath("curl")
	if err != nil {
		return err
	}
	tarBin, err := exec.LookPath("tar")
	if err != nil {
		return err
	}

	workDir, err := os.MkdirTemp("", "devstack-runtime-")
	if err != nil {
		return fmt.Errorf("agent: create runtime download directory: %w", err)
	}
	defer os.RemoveAll(workDir)
	if err := os.Chmod(workDir, 0o700); err != nil {
		return fmt.Errorf("agent: secure runtime download directory: %w", err)
	}
	archivePath := filepath.Join(workDir, "archive.tar.gz")
	archive, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("agent: create runtime archive: %w", err)
	}
	if err := archive.Close(); err != nil {
		return fmt.Errorf("agent: close runtime archive: %w", err)
	}

	cmdCurl := exec.CommandContext(ctx, curlBin,
		"--http1.1", "-fsSL", "--retry", "2", "--retry-delay", "1",
		"--connect-timeout", "20", "--max-time", "900",
		"-o", archivePath, url)
	if out, err := cmdCurl.CombinedOutput(); err != nil {
		return fmt.Errorf("curl: %w (output: %s)", err, string(out))
	}

	// The pinned size is checked before hashing so a truncated or substituted
	// archive fails even if the daemon's checksum was also wrong.
	info, err := os.Stat(archivePath)
	if err != nil {
		return fmt.Errorf("agent: stat runtime archive: %w", err)
	}
	if info.Size() != expectedSize {
		return fmt.Errorf("agent: runtime archive size mismatch: got %d bytes, want %d", info.Size(), expectedSize)
	}

	// Stream the archive through SHA-256. Reading the whole file into memory
	// would allocate the archive size (hundreds of MiB for a PHP runtime) in
	// the root agent.
	sum, err := hashFileSHA256(archivePath)
	if err != nil {
		return fmt.Errorf("agent: read runtime archive for checksum: %w", err)
	}
	if !strings.EqualFold(sum, expected) {
		return fmt.Errorf("checksum mismatch: got %s, want %s", sum, expected)
	}

	// Reject traversal and unsafe link members before tar touches the disk.
	if err := validateRuntimeArchive(archivePath); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(destDir), 0o755); err != nil {
		return fmt.Errorf("agent: create runtime parent: %w", err)
	}
	staging, err := os.MkdirTemp(filepath.Dir(destDir), filepath.Base(destDir)+".staging-")
	if err != nil {
		return fmt.Errorf("agent: create runtime staging: %w", err)
	}
	defer os.RemoveAll(staging)
	if err := os.Chmod(staging, 0o755); err != nil {
		return fmt.Errorf("agent: prepare runtime staging: %w", err)
	}
	cmdTar := exec.CommandContext(ctx, tarBin, "-xz", "--no-same-owner", "--strip-components=1", "-C", staging, "-f", archivePath)
	if out, err := cmdTar.CombinedOutput(); err != nil {
		return fmt.Errorf("tar: %w (output: %s)", err, string(out))
	}
	// tar's own checks are not a substitute for verifying the tree that was
	// actually produced: reject any extracted link that resolves outside the
	// staging directory.
	if err := verifyExtractedLinks(staging); err != nil {
		return err
	}

	if entries, err := os.ReadDir(destDir); err == nil && len(entries) != 0 {
		return fmt.Errorf("agent: runtime destination %s is not empty", destDir)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("agent: inspect runtime destination: %w", err)
	}
	if err := os.RemoveAll(destDir); err != nil {
		return fmt.Errorf("agent: clear runtime destination: %w", err)
	}
	if err := os.Rename(staging, destDir); err != nil {
		return fmt.Errorf("agent: activate runtime: %w", err)
	}
	return nil
}

func hashFileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

const (
	maxRuntimeArchiveEntries = 200_000
	maxRuntimeArchiveBytes   = int64(2) << 30
)

// validateRuntimeArchive walks every member before extraction. It rejects
// absolute or escaping names, device/FIFO/hardlink members, and symlinks whose
// lexical target leaves the extraction root.
func validateRuntimeArchive(archivePath string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("agent: open runtime archive: %w", err)
	}
	defer func() { _ = file.Close() }()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("agent: open runtime archive gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()
	reader := tar.NewReader(io.LimitReader(gz, maxRuntimeArchiveBytes+1<<20))

	entries := 0
	var total int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("agent: read runtime archive: %w", err)
		}
		entries++
		if entries > maxRuntimeArchiveEntries {
			return fmt.Errorf("agent: runtime archive has more than %d members", maxRuntimeArchiveEntries)
		}
		if header.Size < 0 || total > maxRuntimeArchiveBytes-header.Size {
			return fmt.Errorf("agent: runtime archive expands beyond %d bytes", maxRuntimeArchiveBytes)
		}
		total += header.Size

		name, err := cleanRuntimeArchivePath(header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeReg, tar.TypeRegA, tar.TypeDir:
		case tar.TypeSymlink:
			if err := validateRuntimeArchiveLink(name, header.Linkname); err != nil {
				return err
			}
		default:
			return fmt.Errorf("agent: runtime archive member %q has unsupported type %q", header.Name, header.Typeflag)
		}
	}
}

func cleanRuntimeArchivePath(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, 0) || strings.Contains(name, `\`) {
		return "", fmt.Errorf("agent: runtime archive member %q has an invalid name", name)
	}
	if path.IsAbs(name) || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("agent: runtime archive member %q is absolute", name)
	}
	clean := path.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("agent: runtime archive member %q escapes the extraction root", name)
	}
	return clean, nil
}

func validateRuntimeArchiveLink(name, target string) error {
	if target == "" || strings.ContainsRune(target, 0) || strings.Contains(target, `\`) ||
		path.IsAbs(target) || strings.HasPrefix(target, "/") {
		return fmt.Errorf("agent: runtime archive symlink %q has an invalid target", name)
	}
	// Resolve the target against the link's directory. The first component is
	// stripped from the extracted tree, so evaluate the lexically joined path
	// below the archive root as well.
	joined := path.Clean(path.Join(path.Dir(name), target))
	if joined == ".." || strings.HasPrefix(joined, "../") {
		return fmt.Errorf("agent: runtime archive symlink %q points outside the extraction root", name)
	}
	return nil
}

// verifyExtractedLinks rejects any symlink in the staged tree whose resolved
// target leaves the tree.
func verifyExtractedLinks(root string) error {
	return filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink == 0 {
			return nil
		}
		target, err := os.Readlink(current)
		if err != nil {
			return fmt.Errorf("agent: read extracted symlink %s: %w", current, err)
		}
		if path.IsAbs(target) || strings.HasPrefix(target, "/") {
			return fmt.Errorf("agent: extracted symlink %s is absolute", current)
		}
		resolved := filepath.Clean(filepath.Join(filepath.Dir(current), filepath.FromSlash(target)))
		rel, err := filepath.Rel(root, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return fmt.Errorf("agent: extracted symlink %s points outside the runtime directory", current)
		}
		return nil
	})
}
