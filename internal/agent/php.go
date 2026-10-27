package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/catalog"
)

type phpGeneration struct {
	spec         agentproto.SiteSpec
	documentRoot string
	fpmPath      string
	port         int
	process      *fpmProcess
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

// InstallRuntime downloads/registers an installed runtime build from the catalog.
func (r *Runtime) InstallRuntime(ctx context.Context, req agentproto.RuntimeRequest) (agentproto.RuntimeResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return agentproto.RuntimeResponse{}, err
	}
	cat, err := catalog.Load()
	if err != nil {
		return agentproto.RuntimeResponse{}, fmt.Errorf("agent: load catalog: %w", err)
	}

	var match *catalog.Entry
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

	targetDir := filepath.Join(r.root, "runtimes", match.ID)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return agentproto.RuntimeResponse{}, fmt.Errorf("agent: stage runtime directory: %w", err)
	}

	// Gate PHP on Debian 12 compatibility check if binaries are present
	if match.Kind == "php" {
		phpBin := filepath.Join(targetDir, "bin", "php")
		if runtime.GOOS != "windows" && match.URL != "" && os.Getenv("DEVSTACK_NO_DOWNLOAD") == "" {
			if _, err := os.Stat(phpBin); errors.Is(err, os.ErrNotExist) {
				_ = downloadAndExtractArchive(ctx, match.URL, match.SHA256, targetDir)
			}
		}
		if _, err := os.Stat(phpBin); err == nil {
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

// RemoveRuntime deletes a registered runtime.
func (r *Runtime) RemoveRuntime(ctx context.Context, req agentproto.RuntimeRequest) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	id := strings.TrimSpace(req.BuildID)
	if id == "" {
		return errors.New("agent: build id is required")
	}

	r.mu.Lock()
	delete(r.runtimes, id)
	_ = r.saveRuntimesLocked()
	r.mu.Unlock()

	targetDir := filepath.Join(r.root, "runtimes", id)
	_ = os.RemoveAll(targetDir)
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

	site.phpGenerations[gen] = &phpGeneration{
		spec:         cloneAgentSiteSpec(req.Site),
		documentRoot: docroot,
		fpmPath:      fpmPath,
		port:         port,
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
		r.mu.Unlock()
		return agentproto.SiteActionResponse{}, fmt.Errorf("agent: generation %q not prepared for site %q", gen, id)
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

// StopPHP stops the running PHP-FPM process for a site.
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
	if gen != "" && site.phpGenerations[gen] != nil {
		if proc := site.phpGenerations[gen].process; proc != nil {
			processesToStop = append(processesToStop, proc)
			site.phpGenerations[gen].process = nil
		}
	} else {
		for _, g := range site.phpGenerations {
			if g.process != nil {
				processesToStop = append(processesToStop, g.process)
				g.process = nil
			}
		}
	}
	site.status = "stopped"
	site.desiredRunning = false
	_ = r.saveLocked()
	r.mu.Unlock()

	for _, p := range processesToStop {
		p.stop()
	}

	r.logs.append(id, "stopped PHP-FPM processes")
	return agentproto.SiteActionResponse{Status: "stopped"}, nil
}

func resolvePHPDocumentRoot(spec agentproto.SiteSpec) (string, error) {
	projectRoot, err := filepath.Abs(spec.BackendPath)
	if err != nil {
		return "", fmt.Errorf("agent: invalid project root: %w", err)
	}
	doc := strings.TrimSpace(spec.DocumentRoot)
	if doc == "" || doc == "." {
		return projectRoot, nil
	}
	resolved := ""
	if filepath.IsAbs(doc) {
		resolved = filepath.Clean(doc)
	} else {
		resolved = filepath.Clean(filepath.Join(projectRoot, doc))
	}
	rel, err := filepath.Rel(projectRoot, resolved)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", errors.New("agent: document root escapes project root")
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", fmt.Errorf("agent: document root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("agent: document root is a symlink")
	}
	return resolved, nil
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

func downloadAndExtractArchive(ctx context.Context, url, expectedSHA256, destDir string) error {
	tmpFile := filepath.Join(os.TempDir(), "devstack-runtime-"+filepath.Base(url))
	defer os.Remove(tmpFile)

	curlBin, err := exec.LookPath("curl")
	if err != nil {
		return err
	}
	tarBin, err := exec.LookPath("tar")
	if err != nil {
		return err
	}

	cmdCurl := exec.CommandContext(ctx, curlBin, "--http1.1", "-fsSL", "-o", tmpFile, url)
	if out, err := cmdCurl.CombinedOutput(); err != nil {
		return fmt.Errorf("curl: %w (output: %s)", err, string(out))
	}

	if expectedSHA256 != "" {
		data, err := os.ReadFile(tmpFile)
		if err == nil {
			sum := sha256.Sum256(data)
			hexSum := hex.EncodeToString(sum[:])
			if !strings.EqualFold(hexSum, expectedSHA256) {
				return fmt.Errorf("checksum mismatch: got %s, want %s", hexSum, expectedSHA256)
			}
		}
	}

	cmdTar := exec.CommandContext(ctx, tarBin, "-xz", "--strip-components=1", "-C", destDir, "-f", tmpFile)
	if out, err := cmdTar.CombinedOutput(); err != nil {
		return fmt.Errorf("tar: %w (output: %s)", err, string(out))
	}

	return nil
}
