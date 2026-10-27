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
	"strings"
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
	if site.server != nil {
		response := agentproto.SiteActionResponse{Status: "running"}
		r.mu.Unlock()
		return response, nil
	}
	handler, err := staticserve.NewStaticHandler(site.documentRoot, site.spec.SPAFallback)
	if err != nil {
		r.mu.Unlock()
		return agentproto.SiteActionResponse{}, fmt.Errorf("agent: open static document root: %w", err)
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", site.spec.HTTPPort)))
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
	status := site.status
	if site.server == nil && status == "running" {
		status = "stopped"
	}
	return agentproto.SiteStatusResponse{Status: status, HTTPPort: site.spec.HTTPPort}, nil
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
	project, err := filepath.Abs(project)
	if err != nil {
		return "", fmt.Errorf("agent: resolve backend path: %w", err)
	}
	if err := rejectSymlinkComponents(project); err != nil {
		return "", fmt.Errorf("agent: unsafe backend path: %w", err)
	}
	root := strings.TrimSpace(spec.DocumentRoot)
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
