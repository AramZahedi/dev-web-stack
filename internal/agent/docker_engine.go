package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var (
	ErrSystemdNotReady          = errors.New("agent: systemd Docker service is not ready")
	ErrDockerEngineUnavailable  = errors.New("agent: Docker Engine is unavailable")
	ErrDockerDataRootMismatch   = errors.New("agent: Docker data-root does not match managed WSL storage")
	ErrDockerComposeUnavailable = errors.New("agent: Docker Compose plugin is unavailable")
	ErrDockerBootstrapFailed    = errors.New("agent: Docker bootstrap failed")
)

// DockerCommandRunner isolates Docker/systemctl invocations for tests.
type DockerCommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecDockerCommandRunner executes fixed local programs. Callers never pass
// credentials or daemon-supplied shell commands through this boundary.
type ExecDockerCommandRunner struct{}

func (ExecDockerCommandRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

// DockerReadiness describes the parts needed before Docker-backed services may
// be managed by the agent.
type DockerReadiness struct {
	SystemdReady bool
	EngineReady  bool
	ComposeReady bool
	DataRoot     string
	Compose      string
}

// DockerEngine manages the dedicated Docker daemon inside WSL. Data is always
// rooted beneath the managed distro's ext4 /data/devstack tree.
type DockerEngine struct {
	root     string
	dataRoot string
	runner   DockerCommandRunner
}

// NewDockerEngine returns an engine whose canonical storage is root/docker.
func NewDockerEngine(root string, runner DockerCommandRunner) (*DockerEngine, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("agent: Docker root must be absolute")
	}
	if runner == nil {
		runner = ExecDockerCommandRunner{}
	}
	return &DockerEngine{root: filepath.Clean(root), dataRoot: filepath.Join(filepath.Clean(root), "docker"), runner: runner}, nil
}

// Health verifies systemd has the Docker service active, the engine reports the
// fixed data-root, and the Compose v2 plugin is installed.
func (d *DockerEngine) Health(ctx context.Context) (DockerReadiness, error) {
	readiness := DockerReadiness{DataRoot: d.dataRoot}
	if _, err := d.runner.Run(ctx, "systemctl", "is-active", "--quiet", "docker.service"); err != nil {
		return readiness, fmt.Errorf("%w: %v", ErrSystemdNotReady, err)
	}
	readiness.SystemdReady = true
	root, err := d.runner.Run(ctx, "docker", "info", "--format", "{{.DockerRootDir}}")
	if err != nil {
		return readiness, fmt.Errorf("%w: %v", ErrDockerEngineUnavailable, err)
	}
	actual := filepath.Clean(strings.TrimSpace(string(root)))
	if actual != d.dataRoot {
		return readiness, fmt.Errorf("%w: got %q want %q", ErrDockerDataRootMismatch, actual, d.dataRoot)
	}
	readiness.EngineReady = true
	compose, err := d.runner.Run(ctx, "docker", "compose", "version", "--short")
	if err != nil || strings.TrimSpace(string(compose)) == "" {
		if err != nil {
			return readiness, fmt.Errorf("%w: %v", ErrDockerComposeUnavailable, err)
		}
		return readiness, ErrDockerComposeUnavailable
	}
	readiness.ComposeReady = true
	readiness.Compose = strings.TrimSpace(string(compose))
	return readiness, nil
}

// Bootstrap installs the pinned engine from the packaged fixed script. It is
// explicit and is never invoked by request handlers.
func (d *DockerEngine) Bootstrap(ctx context.Context) error {
	if err := os.MkdirAll(d.dataRoot, 0o711); err != nil {
		return fmt.Errorf("%w: create data-root: %v", ErrDockerBootstrapFailed, err)
	}
	if err := os.Chmod(d.dataRoot, 0o711); err != nil {
		return fmt.Errorf("%w: protect data-root: %v", ErrDockerBootstrapFailed, err)
	}
	if _, err := d.runner.Run(ctx, "/usr/bin/env", "bash", "/data/devstack/bin/install-docker.sh"); err != nil {
		return fmt.Errorf("%w: %v", ErrDockerBootstrapFailed, err)
	}
	if _, err := d.Health(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrDockerBootstrapFailed, err)
	}
	return nil
}
