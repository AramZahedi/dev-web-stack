package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/phpfpm"
)

type fpmProcess struct {
	siteID     string
	generation string
	port       int
	cmd        *exec.Cmd
	listener   net.Listener
	cancel     context.CancelFunc
	done       chan struct{}
	pid        int
	mu         sync.Mutex
	stopped    bool
}

// generateFPMConfig renders the canonical per-site FPM configuration for WSL.
func generateFPMConfig(root string, site agentproto.SiteSpec, docroot string) ([]byte, string, error) {
	siteDir := filepath.Clean(filepath.Join(root, "sites", site.ID))
	fpmPath := filepath.Clean(filepath.Join(siteDir, "fpm.conf"))
	pidPath := filepath.Clean(filepath.Join(root, "run", site.ID+"-fpm.pid"))
	logPath := filepath.Clean(filepath.Join(root, "logs", site.ID+".log"))

	buildRoot := site.PHPBuildPath
	if buildRoot == "" {
		if site.PHPBuild != "" {
			buildRoot = filepath.Join(root, "runtimes", site.PHPBuild)
		} else if site.PHPVersion != "" {
			buildRoot = filepath.Join(root, "runtimes", "php@"+site.PHPVersion)
		} else {
			buildRoot = filepath.Join(root, "runtimes", "php")
		}
	}
	buildRoot = filepath.Clean(buildRoot)

	phpIni := site.PHPIniPath
	if phpIni == "" {
		phpIni = filepath.Join(siteDir, "php.ini")
	}
	phpIni = filepath.Clean(phpIni)

	cfg := phpfpm.Config{
		SiteID:       site.ID,
		BuildRoot:    buildRoot,
		PHPIni:       phpIni,
		ListenHost:   "127.0.0.1",
		ListenPort:   site.FastCGIPort,
		DocumentRoot: filepath.Clean(docroot),
		ErrorLog:     logPath,
		PIDFile:      pidPath,
		Env:          site.Env,
	}

	rendered, err := phpfpm.Render(cfg)
	if err != nil {
		return nil, "", fmt.Errorf("agent: render fpm config for %q: %w", site.ID, err)
	}

	return rendered, fpmPath, nil
}

func (r *Runtime) startFPM(ctx context.Context, siteID, generation, fpmPath, iniPath, buildPath string, port int) (*fpmProcess, error) {
	if port <= 0 {
		return nil, errors.New("agent: invalid FastCGI port for FPM process")
	}

	fpmBin := filepath.Join(buildPath, "sbin", "php-fpm")
	proc := &fpmProcess{
		siteID:     siteID,
		generation: generation,
		port:       port,
		done:       make(chan struct{}),
	}

	ctx, proc.cancel = context.WithCancel(context.Background())

	// If a real php-fpm binary is available, execute it.
	if info, err := os.Stat(fpmBin); err == nil && !info.IsDir() {
		proc.cmd = exec.CommandContext(ctx, fpmBin, "--nodaemonize", "--fpm-config", fpmPath)
		if iniPath != "" {
			proc.cmd.Args = append(proc.cmd.Args, "-c", iniPath)
		}
		if err := proc.cmd.Start(); err != nil {
			proc.cancel()
			return nil, fmt.Errorf("agent: start php-fpm: %w", err)
		}
		proc.pid = proc.cmd.Process.Pid
		go func() {
			_ = proc.cmd.Wait()
			close(proc.done)
		}()
	} else {
		// Mock/simulated listener for environments without native php-fpm.
		addr := "127.0.0.1:" + strconv.Itoa(port)
		l, err := net.Listen("tcp", addr)
		if err != nil {
			proc.cancel()
			return nil, fmt.Errorf("agent: simulate php-fpm listen on %s: %w", addr, err)
		}
		proc.listener = l
		proc.pid = os.Getpid()
		go func() {
			<-ctx.Done()
			_ = l.Close()
			close(proc.done)
		}()
	}

	// Await port listening readiness
	ready := make(chan struct{})
	go func() {
		target := "127.0.0.1:" + strconv.Itoa(port)
		for {
			select {
			case <-ctx.Done():
				return
			case <-proc.done:
				return
			default:
				c, err := net.DialTimeout("tcp", target, 50*time.Millisecond)
				if err == nil {
					_ = c.Close()
					close(ready)
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
	}()

	select {
	case <-ready:
		return proc, nil
	case <-time.After(3 * time.Second):
		proc.stop()
		return nil, fmt.Errorf("agent: php-fpm timed out listening on port %d", port)
	case <-ctx.Done():
		proc.stop()
		return nil, ctx.Err()
	}
}

func (p *fpmProcess) stop() {
	if p == nil {
		return
	}
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	p.mu.Unlock()

	if p.cancel != nil {
		p.cancel()
	}
	if p.listener != nil {
		_ = p.listener.Close()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	select {
	case <-p.done:
	case <-time.After(1 * time.Second):
	}
}
