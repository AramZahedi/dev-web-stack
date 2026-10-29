package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/staticserve"
)

// StartSite starts a prepared static site on its loopback-only backend port.
func (r *Runtime) StartSite(ctx context.Context, id string) (agentproto.SiteActionResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return agentproto.SiteActionResponse{}, err
	}
	r.mu.Lock()
	site := r.sites[id]
	if site == nil {
		r.mu.Unlock()
		return agentproto.SiteActionResponse{}, fmt.Errorf("%w: %s", ErrSiteNotFound, id)
	}
	if site.server != nil || site.nodeCmd != nil {
		response := agentproto.SiteActionResponse{Status: "running", PID: site.nodePID}
		r.mu.Unlock()
		return response, nil
	}
	if site.spec.HTTPPort <= 0 {
		r.mu.Unlock()
		return agentproto.SiteActionResponse{}, fmt.Errorf("agent: site %q has no allocated HTTP port", id)
	}
	if site.spec.Kind == "node" {
		response, cmd, done, err := r.startNodeLocked(site)
		if err != nil {
			r.mu.Unlock()
			return agentproto.SiteActionResponse{}, err
		}
		r.mu.Unlock()
		go r.watchNode(site.spec.ID, cmd, done)
		return response, nil
	}
	handler, err := staticserve.NewStaticHandler(site.documentRoot, site.spec.SPAFallback)
	if err != nil {
		r.mu.Unlock()
		return agentproto.SiteActionResponse{}, fmt.Errorf("agent: open static document root: %w", err)
	}
	listener, err := r.listenSitePort(ctx, site.spec.HTTPPort)
	if err != nil {
		_ = handler.Close()
		r.mu.Unlock()
		return agentproto.SiteActionResponse{}, fmt.Errorf("agent: bind site %q loopback port %d: %w", id, site.spec.HTTPPort, err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	site.server = server
	site.desiredRunning = true
	site.status = "running"
	if err := r.saveLocked(); err != nil {
		site.server = nil
		site.desiredRunning = false
		site.status = "stopped"
		r.mu.Unlock()
		_ = listener.Close()
		_ = handler.Close()
		return agentproto.SiteActionResponse{}, err
	}
	r.mu.Unlock()
	r.logs.append(id, "static site started on 127.0.0.1:"+fmt.Sprintf("%d", site.spec.HTTPPort))
	go func() {
		serveErr := server.Serve(listener)
		_ = handler.Close()
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			r.mu.Lock()
			if current := r.sites[id]; current != nil && current.server == server {
				current.server = nil
				current.status = "crashed"
				_ = r.saveLocked()
			}
			r.mu.Unlock()
			r.logs.append(id, "static site crashed: "+serveErr.Error())
		}
	}()
	return agentproto.SiteActionResponse{Status: "running"}, nil
}

// StopSite stops a static site. Stopping an unknown site is an error; stopping
// an already stopped site is idempotent.
func (r *Runtime) StopSite(ctx context.Context, id string, graceful bool) error {
	_ = graceful // HTTP server shutdown always drains active requests.
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	site := r.sites[id]
	if site == nil {
		r.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrSiteNotFound, id)
	}
	if site.spec.Kind == "node" {
		cmd, pid, done := site.nodeCmd, site.nodePID, site.nodeDone
		site.desiredRunning = false
		site.status = "stopped"
		if err := r.saveLocked(); err != nil {
			r.mu.Unlock()
			return err
		}
		r.mu.Unlock()
		if cmd != nil {
			stopNodeProcess(cmd, pid, done)
			r.logs.append(id, "stopped Node site")
		}
		return nil
	}
	server := site.server
	site.desiredRunning = false
	site.status = "stopped"
	if err := r.saveLocked(); err != nil {
		r.mu.Unlock()
		return err
	}
	r.mu.Unlock()
	if server != nil {
		if err := server.Shutdown(ctx); err != nil {
			_ = server.Close()
			// Shutdown failed (usually a request that outlived the deadline).
			// Close released the listener; clear the server so a later start
			// does not report a phantom "already running" site.
			r.mu.Lock()
			if current := r.sites[id]; current == site && current.server == server {
				current.server = nil
				current.status = "stopped"
				_ = r.saveLocked()
			}
			r.mu.Unlock()
			return fmt.Errorf("agent: stop site %q: %w", id, err)
		}
	}
	r.mu.Lock()
	if current := r.sites[id]; current == site && current.server == server {
		current.server = nil
		current.status = "stopped"
		if err := r.saveLocked(); err != nil {
			r.mu.Unlock()
			return err
		}
	}
	r.mu.Unlock()
	if server != nil {
		r.logs.append(id, "static site stopped")
	}
	return nil
}

// SiteStatus reports the current runtime state.
func (r *Runtime) SiteStatus(id string) (agentproto.SiteStatusResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	site := r.sites[id]
	if site == nil {
		return agentproto.SiteStatusResponse{}, fmt.Errorf("%w: %s", ErrSiteNotFound, id)
	}
	if site.spec.Kind == "php" {
		gen := site.activeGen
		if gen == "" {
			gen = "default"
		}
		targetGen := site.phpGenerations[gen]
		if targetGen != nil && targetGen.process != nil && targetGen.process.running() {
			site.status = "running"
			return agentproto.SiteStatusResponse{
				Status:      "running",
				PID:         targetGen.process.pid,
				FastCGIPort: targetGen.port,
			}, nil
		}
		site.status = "stopped"
		return agentproto.SiteStatusResponse{
			Status: "stopped",
		}, nil
	}
	status := site.status
	running := site.server != nil || site.nodeCmd != nil
	if !running && status == "running" {
		status = "stopped"
	}
	pid := 0
	if site.spec.Kind == "node" {
		pid = site.nodePID
	}
	return agentproto.SiteStatusResponse{Status: status, PID: pid, HTTPPort: site.spec.HTTPPort}, nil
}

// SiteLogs returns a finite tail or a cancellable live stream of site logs.
func (r *Runtime) SiteLogs(ctx context.Context, id string, follow bool, tail int) (io.ReadCloser, error) {
	if _, err := r.site(id); err != nil {
		return nil, err
	}
	return r.logs.read(id, follow, tail, ctx)
}

func resolveStaticDocumentRoot(spec agentproto.SiteSpec) (string, error) {
	project := strings.TrimSpace(spec.BackendPath)
	if project == "" || !filepath.IsAbs(project) {
		return "", errors.New("agent: static site backend_path must be an absolute WSL path")
	}
	abs, err := filepath.Abs(project)
	if err != nil {
		return "", fmt.Errorf("agent: resolve backend path: %w", err)
	}
	if err := rejectSymlinkComponents(abs); err != nil {
		return "", fmt.Errorf("agent: unsafe backend path: %w", err)
	}
	return resolveDocumentRoot(abs, spec.DocumentRoot)
}

// resolveDocumentRoot resolves a site's configured document root inside an
// absolute, already-validated project root. Every existing component is checked
// for symlinks: a symlinked intermediate directory can otherwise escape the
// project while the final component is a real directory.
func resolveDocumentRoot(project, documentRoot string) (string, error) {
	root := strings.TrimSpace(documentRoot)
	var err error
	if root == "" || root == "." {
		root = project
	} else if filepath.IsAbs(root) {
		root, err = filepath.Abs(root)
		if err != nil {
			return "", err
		}
	} else {
		if !filepath.IsLocal(root) {
			return "", errors.New("agent: document root escapes backend_path")
		}
		root = filepath.Join(project, root)
	}
	if err := rejectSymlinkComponents(root); err != nil {
		return "", fmt.Errorf("agent: unsafe document root: %w", err)
	}
	rel, err := filepath.Rel(project, root)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", errors.New("agent: document root escapes backend_path")
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("agent: document root %s: %w", root, err)
	}
	if !info.IsDir() {
		return "", errors.New("agent: document root is not a directory")
	}
	return root, nil
}

// listenSitePort retries a loopback bind briefly when the port is held by a
// transient client socket. The managed backend range overlaps the Linux
// ephemeral range, so an unrelated outbound connection can occupy a port that
// was free moments earlier.
func (r *Runtime) listenSitePort(ctx context.Context, port int) (net.Listener, error) {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if err := ctxErr(ctx); err != nil {
			return nil, err
		}
		listener, err := net.Listen("tcp", addr)
		if err == nil {
			return listener, nil
		}
		lastErr = err
		if !isAddressInUse(err) {
			return nil, err
		}
		if attempt < 3 {
			time.Sleep(15 * time.Millisecond)
		}
	}
	return nil, lastErr
}

// isAddressInUse reports EADDRINUSE portably. Windows exposes WSAEADDRINUSE
// (10048) rather than the POSIX errno used by the Linux agent.
func isAddressInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	if runtime.GOOS == "windows" {
		var errno syscall.Errno
		if errors.As(err, &errno) && uintptr(errno) == 10048 {
			return true
		}
	}
	return false
}

func rejectSymlinkComponents(path string) error {
	volume := filepath.VolumeName(path)
	clean := filepath.Clean(path)
	rel := strings.TrimPrefix(clean, volume)
	cur := volume + string(filepath.Separator)
	for _, component := range strings.Split(strings.TrimPrefix(rel, string(filepath.Separator)), string(filepath.Separator)) {
		if component == "" {
			continue
		}
		cur = filepath.Join(cur, component)
		info, err := os.Lstat(cur)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink component %s is not allowed", cur)
		}
	}
	return nil
}
