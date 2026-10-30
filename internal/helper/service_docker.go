//go:build !windows

package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbengine"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

const servicePollInterval = 200 * time.Millisecond

// StreamCommandRunner is an optional CommandRunner extension for commands whose
// stdout is streamed to the caller (database dumps and exports).
type StreamCommandRunner interface {
	RunStream(ctx context.Context, name string, args ...string) (io.ReadCloser, error)
}

// InputStreamCommandRunner is an optional CommandRunner extension for commands
// that consume a stream on stdin (database restores and imports).
type InputStreamCommandRunner interface {
	RunInputStream(ctx context.Context, input io.Reader, name string, args ...string) error
}

// ServiceController defines the operations supported by the container control plane.
type ServiceController interface {
	Status(ctx context.Context) ([]helperproto.ServiceStatus, error)
	Install(ctx context.Context, id services.ID) error
	Activate(ctx context.Context, id services.ID) error
	Deactivate(ctx context.Context, id services.ID) error
	Restart(ctx context.Context, id services.ID) error
	RotateCredentials(ctx context.Context, id services.ID) error
	Validate(ctx context.Context, id services.ID) error
	Reload(ctx context.Context, id services.ID) error
	Uninstall(ctx context.Context, id services.ID, deleteData bool) error
	Logs(ctx context.Context, id services.ID, tail int) (string, error)
	databases.Platform
}

// DockerServiceController manages service containers via fixed Docker Compose argv calls.
type DockerServiceController struct {
	root        string
	catalog     services.Catalog
	runner      CommandRunner
	projectName string
	composePath string

	mu   sync.Mutex
	opMu sync.Mutex

	// databaseRegistry resolves SQL engines. It defaults to dbengine.DefaultRegistry
	// when nil, which the shared engine package installs at init. Tests inject a
	// registry without touching the frozen foundation.
	databaseRegistry dbengine.Registry
	databaseLocks    sync.Map

	// OwnerUID, when nonzero, owns the config and log directories created by Install.
	OwnerUID int

	// Secrets overrides the host credential store. Tests inject a failing store
	// to exercise rollback; production uses <root>/secrets.
	Secrets ServiceSecretStore

	// Now, Sleep, and Probe are seams used by health and port waits. Tests replace
	// them so they never touch Docker or sleep. Probe returns nil when the loopback
	// port is accepting connections.
	Now         func() time.Time
	Sleep       func(context.Context, time.Duration) error
	Probe       func(context.Context, int) error
	WaitTimeout time.Duration
}

// ServiceActivationError reports the phase that failed during an activation and
// whether restoring the previous exclusive-group service also failed.
type ServiceActivationError struct {
	Service     services.ID
	Phase       string
	Port        int
	Err         error
	RollbackErr error
}

func (e *ServiceActivationError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "activate %s failed during %s", e.Service, e.Phase)
	if e.Port > 0 {
		fmt.Fprintf(&b, " (port %d)", e.Port)
	}
	if e.Err != nil {
		b.WriteString(": ")
		b.WriteString(e.Err.Error())
	}
	if e.RollbackErr != nil {
		b.WriteString("; rollback failed: ")
		b.WriteString(e.RollbackErr.Error())
	}
	return b.String()
}

// Unwrap exposes the underlying activation failure.
func (e *ServiceActivationError) Unwrap() error { return e.Err }

// NewDockerServiceController creates a controller scoped to the devstack root and catalog.
func NewDockerServiceController(
	root string,
	catalog services.Catalog,
	runner CommandRunner,
) *DockerServiceController {
	if runner == nil {
		runner = execRunner{}
	}
	cleanRoot := filepath.Clean(root)
	if abs, err := filepath.Abs(cleanRoot); err == nil {
		cleanRoot = abs
	}
	hash := services.RootHash(cleanRoot)
	return &DockerServiceController{
		root:        cleanRoot,
		catalog:     catalog,
		runner:      runner,
		projectName: "devstack-" + hash,
		composePath: filepath.Join(cleanRoot, "services", "compose.yaml"),
		Now:         time.Now,
		Sleep:       sleepContext,
		Probe:       probeLoopbackPort,
		WaitTimeout: 60 * time.Second,
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func probeLoopbackPort(ctx context.Context, port int) error {
	dialer := net.Dialer{Timeout: 250 * time.Millisecond}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return err
	}
	return conn.Close()
}

func (c *DockerServiceController) service(id services.ID) (services.Service, error) {
	svc, ok := c.catalog.Get(id)
	if !ok {
		return services.Service{}, fmt.Errorf("%w: unknown service %q", helperproto.ErrInvalidRequest, id)
	}
	return svc, nil
}

func (c *DockerServiceController) ensureComposeFile() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := services.RenderCompose(c.catalog, c.root)
	if err != nil {
		return fmt.Errorf("render compose: %w", err)
	}

	dir := filepath.Dir(c.composePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create services dir: %w", err)
	}

	existing, err := os.ReadFile(c.composePath)
	switch {
	case err == nil:
		if bytes.Equal(existing, data) {
			return nil
		}
		if err := writeAtomicFile(c.composePath+".bak", existing, 0o644); err != nil {
			return fmt.Errorf("backup compose file: %w", err)
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return fmt.Errorf("read compose file: %w", err)
	}

	if err := writeAtomicFile(c.composePath, data, 0o644); err != nil {
		return fmt.Errorf("write compose file: %w", err)
	}
	return nil
}

func (c *DockerServiceController) runCompose(ctx context.Context, args ...string) ([]byte, error) {
	base := []string{"compose", "-p", c.projectName, "-f", c.composePath}
	return c.runner.Run(ctx, "docker", append(base, args...)...)
}

type psContainer struct {
	ID      string `json:"ID"`
	Name    string `json:"Name"`
	Service string `json:"Service"`
	State   string `json:"State"`
	Status  string `json:"Status"`
	Health  string `json:"Health"`
}

func parsePSOutput(data []byte) ([]psContainer, error) {
	var list []psContainer
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal([]byte(trimmed), &list); err != nil {
			return nil, fmt.Errorf("parse compose ps JSON array: %w", err)
		}
		return list, nil
	}
	lines := strings.Split(trimmed, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item psContainer
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, fmt.Errorf("parse compose ps line %d: %w", i+1, err)
		}
		if item.Service != "" || item.Name != "" {
			list = append(list, item)
		}
	}
	return list, nil
}

func containerName(cont psContainer) string {
	if cont.Service != "" {
		return cont.Service
	}
	return strings.TrimPrefix(cont.Name, "devstack-")
}

func containersByService(data []byte) (map[string]psContainer, error) {
	parsed, err := parsePSOutput(data)
	if err != nil {
		return nil, err
	}
	containers := make(map[string]psContainer, len(parsed))
	for _, cont := range parsed {
		name := containerName(cont)
		if name == "" {
			continue
		}
		if previous, ok := containers[name]; ok && strings.EqualFold(previous.State, "running") {
			continue
		}
		containers[name] = cont
	}
	return containers, nil
}

func (c *DockerServiceController) containers(ctx context.Context) (map[string]psContainer, error) {
	out, err := c.runCompose(ctx, "ps", "-a", "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("docker compose ps: %w", err)
	}
	containers, err := containersByService(out)
	if err != nil {
		return nil, fmt.Errorf("docker compose ps: %w", err)
	}
	return containers, nil
}

func isAbsentContainerError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such container") ||
		strings.Contains(message, "no container found") ||
		strings.Contains(message, "no container to stop") ||
		strings.Contains(message, "container not found")
}

func (c *DockerServiceController) serviceDirsPresent(id services.ID) bool {
	for _, sub := range []string{"config", "data", "logs"} {
		info, err := os.Stat(filepath.Join(c.root, "services", string(id), sub))
		if err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

// serviceInstalled means this product actually installed the service: its
// scaffold directories exist. A cached Docker image alone is not an
// installation; Install pulls first and only then scaffolds.
func (c *DockerServiceController) serviceInstalled(svc services.Service) bool {
	return c.serviceDirsPresent(svc.ID)
}

// Status inspects the docker compose project and returns status for all catalog services.
func (c *DockerServiceController) Status(ctx context.Context) ([]helperproto.ServiceStatus, error) {
	containers := map[string]psContainer{}
	if _, err := os.Stat(c.composePath); err == nil {
		var err error
		containers, err = c.containers(ctx)
		if err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("stat compose file: %w", err)
	}

	statuses := make([]helperproto.ServiceStatus, 0, len(c.catalog.Services))
	for _, svc := range c.catalog.Services {
		idStr := string(svc.ID)
		st := helperproto.ServiceStatus{
			ID:         idStr,
			Image:      svc.Image,
			State:      string(services.StateNotInstalled),
			Active:     false,
			Installed:  false,
			ConfigPath: filepath.Join(c.root, "services", idStr, "config"),
			DataPath:   filepath.Join(c.root, "services", idStr, "data"),
		}

		for _, p := range svc.Ports {
			st.Ports = append(st.Ports, helperproto.ServicePortStatus{
				Host:      p.Host,
				Container: p.Container,
				Protocol:  p.Protocol,
				Loopback:  p.Loopback,
			})
		}

		if cont, ok := containers[idStr]; ok {
			st.Installed = true
			st.ContainerID = cont.ID
			st.Health = cont.Health
			state := strings.ToLower(cont.State)
			health := strings.ToLower(cont.Health)
			switch state {
			case "running":
				switch {
				case health == "unhealthy":
					st.State = string(services.StateError)
					st.LastError = "container is unhealthy"
				case health == "starting":
					st.State = string(services.StateStarting)
				case health == "" && svc.Healthcheck != nil:
					st.State = string(services.StateStarting)
				default:
					st.State = string(services.StateActive)
					st.Active = true
				}
			case "restarting":
				st.State = string(services.StateStarting)
			default:
				st.State = string(services.StateInstalledButStopped)
			}
		} else {
			if c.serviceInstalled(svc) {
				st.Installed = true
				st.State = string(services.StateInstalled)
			}
		}

		statuses = append(statuses, st)
	}

	return statuses, nil
}

func (c *DockerServiceController) scaffoldService(id services.ID) error {
	for _, sub := range []string{"config", "data", "logs"} {
		dir := filepath.Join(c.root, "services", string(id), sub)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create service directory %s: %w", dir, err)
		}
	}
	if c.OwnerUID != 0 {
		for _, sub := range []string{"config", "logs"} {
			dir := filepath.Join(c.root, "services", string(id), sub)
			if err := os.Chown(dir, c.OwnerUID, -1); err != nil {
				return fmt.Errorf("chown service directory %s: %w", dir, err)
			}
		}
	}
	return nil
}

// Install pulls the container image and scaffolds catalog-owned directories. It
// never starts the service.
func (c *DockerServiceController) Install(ctx context.Context, id services.ID) error {
	if _, err := c.service(id); err != nil {
		return err
	}

	c.opMu.Lock()
	defer c.opMu.Unlock()

	if err := c.ensureComposeFile(); err != nil {
		return err
	}
	if _, err := c.runCompose(ctx, "pull", string(id)); err != nil {
		return fmt.Errorf("docker compose pull %s: %w", id, err)
	}
	if err := c.scaffoldService(id); err != nil {
		return err
	}
	return nil
}

func (c *DockerServiceController) waitTimeout() time.Duration {
	if c.WaitTimeout <= 0 {
		return 60 * time.Second
	}
	return c.WaitTimeout
}

func (c *DockerServiceController) restoreServices(ctx context.Context, ids []services.ID) error {
	var restoreErrs []error
	for _, id := range ids {
		svc, ok := c.catalog.Get(id)
		if !ok {
			continue
		}
		if _, err := c.runCompose(ctx, "restart", string(id)); err != nil {
			restoreErrs = append(restoreErrs, fmt.Errorf("restart %s: %w", id, err))
			continue
		}
		if err := c.waitReady(ctx, svc); err != nil {
			restoreErrs = append(restoreErrs, fmt.Errorf("wait for %s after restart: %w", id, err))
		}
	}
	return errors.Join(restoreErrs...)
}

// Activate stops active exclusive-group competitors, starts the target, waits for
// health and port ownership, and restores the previous service on failure.
func (c *DockerServiceController) Activate(ctx context.Context, id services.ID) error {
	svc, err := c.service(id)
	if err != nil {
		return err
	}

	c.opMu.Lock()
	defer c.opMu.Unlock()

	if !c.serviceInstalled(svc) {
		return fmt.Errorf("%w: service %s is not installed; run install first", helperproto.ErrInvalidRequest, id)
	}

	if err := c.ensureComposeFile(); err != nil {
		return err
	}

	containers, err := c.containers(ctx)
	if err != nil {
		return &ServiceActivationError{Service: id, Phase: "prepare", Err: err}
	}
	if err := c.checkActivationDependency(id, containers); err != nil {
		return err
	}

	var stopped []services.ID
	if svc.Group.Exclusive() {
		for _, other := range c.catalog.Services {
			if other.ID == id || other.Group != svc.Group {
				continue
			}
			cont, ok := containers[string(other.ID)]
			if !ok || !strings.EqualFold(cont.State, "running") {
				continue
			}
			// Record the competitor before stopping it: Docker may have stopped
			// the container even when the command reports a failure.
			stopped = append(stopped, other.ID)
			if _, err := c.runCompose(ctx, "stop", string(other.ID)); err != nil {
				return &ServiceActivationError{
					Service:     id,
					Phase:       "stop-competitor",
					Err:         fmt.Errorf("stop %s: %w", other.ID, err),
					RollbackErr: c.restoreServices(ctx, stopped),
				}
			}
		}
	}

	rollback := func(activationErr *ServiceActivationError, targetStarted bool) error {
		if targetStarted {
			if _, err := c.runCompose(ctx, "stop", string(id)); err != nil {
				activationErr.RollbackErr = errors.Join(activationErr.RollbackErr, fmt.Errorf("stop target: %w", err))
			}
		}
		activationErr.RollbackErr = errors.Join(activationErr.RollbackErr, c.restoreServices(ctx, stopped))
		return activationErr
	}

	if cont, ok := containers[string(id)]; ok && strings.EqualFold(cont.State, "running") {
		if err := c.waitReady(ctx, svc); err != nil {
			return rollback(&ServiceActivationError{Service: id, Phase: "health", Err: err, RollbackErr: nil}, true)
		}
		busy, err := c.waitPortsReachable(ctx, svc)
		if err != nil {
			return rollback(&ServiceActivationError{Service: id, Phase: "ports", Err: err}, true)
		}
		if busy != 0 {
			return rollback(&ServiceActivationError{
				Service: id,
				Phase:   "ports",
				Port:    busy,
				Err:     fmt.Errorf("host port %d did not become reachable", busy),
			}, true)
		}
		return nil
	}

	busy, err := c.waitPortsFree(ctx, svc)
	if err != nil {
		return rollback(&ServiceActivationError{Service: id, Phase: "port-check", Err: err}, false)
	}
	if busy != 0 {
		return rollback(&ServiceActivationError{
			Service: id,
			Phase:   "port-check",
			Port:    busy,
			Err:     fmt.Errorf("host port %d is already in use; stop the process using it or choose a different service", busy),
		}, false)
	}

	if _, err := c.runCompose(ctx, "up", "-d", "--no-deps", string(id)); err != nil {
		// The container may have been created before Compose reported the
		// failure, so stop it as part of rollback.
		return rollback(&ServiceActivationError{Service: id, Phase: "start", Err: err}, true)
	}
	if err := c.waitReady(ctx, svc); err != nil {
		return rollback(&ServiceActivationError{Service: id, Phase: "health", Err: err}, true)
	}
	busy, err = c.waitPortsReachable(ctx, svc)
	if err != nil {
		return rollback(&ServiceActivationError{Service: id, Phase: "ports", Err: err}, true)
	}
	if busy != 0 {
		return rollback(&ServiceActivationError{
			Service: id,
			Phase:   "ports",
			Port:    busy,
			Err:     fmt.Errorf("host port %d did not become reachable", busy),
		}, true)
	}
	return nil
}

// checkActivationDependency enforces the dynamic phpMyAdmin dependency on an
// active MySQL-family container. The dependency is not a Compose depends_on
// edge because only one of MySQL and MariaDB may be active at a time.
func (c *DockerServiceController) checkActivationDependency(id services.ID, containers map[string]psContainer) error {
	if id != services.IDPHPMyAdmin {
		return nil
	}
	for _, candidate := range []services.ID{services.IDMySQL, services.IDMariaDB} {
		if cont, ok := containers[string(candidate)]; ok && strings.EqualFold(cont.State, "running") {
			return nil
		}
	}
	return &ServiceActivationError{
		Service: id,
		Phase:   "dependency",
		Err:     fmt.Errorf("%w: phpMyAdmin requires an active MySQL or MariaDB service", helperproto.ErrServiceConflict),
	}
}

func (c *DockerServiceController) waitReady(ctx context.Context, svc services.Service) error {
	timeout := c.waitTimeout()
	deadline := c.Now().Add(timeout)
	lastState := "container not created"
	for {
		out, err := c.runCompose(ctx, "ps", "-a", "--format", "json")
		if err != nil {
			return fmt.Errorf("docker compose ps: %w", err)
		}
		byService, err := containersByService(out)
		if err != nil {
			return fmt.Errorf("docker compose ps: %w", err)
		}
		cont, ok := byService[string(svc.ID)]
		if ok {
			lastState = fmt.Sprintf("state=%s health=%s", cont.State, cont.Health)
			switch strings.ToLower(cont.State) {
			case "running":
				health := strings.ToLower(cont.Health)
				if health == "unhealthy" {
					return fmt.Errorf("service %q container is unhealthy", svc.ID)
				}
				if health != "starting" && (svc.Healthcheck == nil || health == "healthy") {
					return nil
				}
			case "exited", "dead":
				return fmt.Errorf("service %q container is %s", svc.ID, cont.State)
			}
		}

		if !c.Now().Before(deadline) {
			return fmt.Errorf("timed out after %s waiting for service %q to become healthy (last state: %s)",
				timeout, svc.ID, lastState)
		}
		if err := c.Sleep(ctx, servicePollInterval); err != nil {
			return err
		}
	}
}

func (c *DockerServiceController) waitPortsFree(ctx context.Context, svc services.Service) (int, error) {
	deadline := c.Now().Add(c.waitTimeout())
	for {
		busy := 0
		for _, port := range svc.Ports {
			if port.Host <= 0 {
				continue
			}
			if err := c.Probe(ctx, port.Host); err == nil {
				busy = port.Host
				break
			}
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
		}
		if busy == 0 {
			return 0, nil
		}
		if !c.Now().Before(deadline) {
			return busy, nil
		}
		if err := c.Sleep(ctx, servicePollInterval); err != nil {
			return 0, err
		}
	}
}

func (c *DockerServiceController) waitPortsReachable(ctx context.Context, svc services.Service) (int, error) {
	deadline := c.Now().Add(c.waitTimeout())
	for {
		unreachable := 0
		for _, port := range svc.Ports {
			if port.Host <= 0 {
				continue
			}
			if err := c.Probe(ctx, port.Host); err != nil {
				unreachable = port.Host
				break
			}
		}
		if unreachable == 0 {
			return 0, nil
		}
		if !c.Now().Before(deadline) {
			return unreachable, nil
		}
		if err := c.Sleep(ctx, servicePollInterval); err != nil {
			return 0, err
		}
	}
}

// Deactivate stops only the specified service container.
func (c *DockerServiceController) Deactivate(ctx context.Context, id services.ID) error {
	if _, err := c.service(id); err != nil {
		return err
	}

	c.opMu.Lock()
	defer c.opMu.Unlock()

	if _, err := os.Stat(c.composePath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat compose file: %w", err)
	}

	if _, err := c.runCompose(ctx, "stop", string(id)); err != nil {
		return fmt.Errorf("docker compose stop %s: %w", id, err)
	}
	return nil
}

// Restart restarts the specified service container and waits for health.
func (c *DockerServiceController) Restart(ctx context.Context, id services.ID) error {
	svc, err := c.service(id)
	if err != nil {
		return err
	}

	c.opMu.Lock()
	defer c.opMu.Unlock()

	if err := c.ensureComposeFile(); err != nil {
		return err
	}
	if _, err := c.runCompose(ctx, "restart", string(id)); err != nil {
		return fmt.Errorf("docker compose restart %s: %w", id, err)
	}
	if err := c.waitReady(ctx, svc); err != nil {
		return fmt.Errorf("restart %s: %w", id, err)
	}
	return nil
}

func (c *DockerServiceController) execCatalogCommand(ctx context.Context, id services.ID, kind string, command []string) error {
	if _, err := c.service(id); err != nil {
		return err
	}
	if len(command) == 0 {
		return fmt.Errorf("%w: service %q has no %s command", helperproto.ErrInvalidRequest, id, kind)
	}

	c.opMu.Lock()
	defer c.opMu.Unlock()

	if err := c.ensureComposeFile(); err != nil {
		return err
	}
	args := []string{"exec", "-T", string(id)}
	args = append(args, command...)
	if _, err := c.runCompose(ctx, args...); err != nil {
		return fmt.Errorf("docker compose exec %s (%s): %w", id, kind, err)
	}
	return nil
}

// Validate runs the catalog-owned validation command inside the service container.
func (c *DockerServiceController) Validate(ctx context.Context, id services.ID) error {
	svc, ok := c.catalog.Get(id)
	if !ok {
		return fmt.Errorf("%w: unknown service %q", helperproto.ErrInvalidRequest, id)
	}
	return c.execCatalogCommand(ctx, id, "validate", svc.ValidateCommand)
}

// Reload runs the catalog-owned reload command inside the service container.
func (c *DockerServiceController) Reload(ctx context.Context, id services.ID) error {
	svc, ok := c.catalog.Get(id)
	if !ok {
		return fmt.Errorf("%w: unknown service %q", helperproto.ErrInvalidRequest, id)
	}
	return c.execCatalogCommand(ctx, id, "reload", svc.ReloadCommand)
}

// Uninstall stops and removes the specified service container. With deleteData it
// first writes and verifies a backup; a backup failure aborts without deleting data.
func (c *DockerServiceController) Uninstall(ctx context.Context, id services.ID, deleteData bool) error {
	if _, err := c.service(id); err != nil {
		return err
	}

	c.opMu.Lock()
	defer c.opMu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}

	composeExists := true
	if _, err := os.Stat(c.composePath); errors.Is(err, os.ErrNotExist) {
		composeExists = false
	} else if err != nil {
		return fmt.Errorf("stat compose file: %w", err)
	}

	if composeExists {
		if _, err := c.runCompose(ctx, "stop", string(id)); err != nil && !isAbsentContainerError(err) {
			return fmt.Errorf("docker compose stop %s: %w", id, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	if deleteData {
		if _, err := c.backupService(id); err != nil {
			return fmt.Errorf("backup service %q: %w", id, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	if composeExists {
		if _, err := c.runCompose(ctx, "rm", "-f", string(id)); err != nil {
			return fmt.Errorf("docker compose rm %s: %w", id, err)
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	if deleteData {
		for _, path := range []string{
			filepath.Join(c.root, "services", string(id)),
			filepath.Join(c.root, "secrets", string(id)),
		} {
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("remove %s: %w", path, err)
			}
		}
	}
	return nil
}

// RunStream starts a command and returns its stdout as a stream. The returned
// reader reports the command's exit error after the final bytes are read.
func (execRunner) RunStream(ctx context.Context, name string, args ...string) (io.ReadCloser, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stream := &commandStream{cmd: cmd, stdout: stdout, stderr: &bytes.Buffer{}}
	cmd.Stderr = stream.stderr
	cmd.WaitDelay = 3 * time.Second
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		return nil, err
	}
	return stream, nil
}

// RunInputStream runs a command with input streamed on stdin. The command's
// combined output is retained for error reporting; the input is never buffered
// by this runner.
func (execRunner) RunInputStream(ctx context.Context, input io.Reader, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = input
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	cmd.WaitDelay = 3 * time.Second
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(output.String())
		if message != "" {
			return fmt.Errorf("%s: %w: %s", cmd.Path, err, message)
		}
		return fmt.Errorf("%s: %w", cmd.Path, err)
	}
	return nil
}

type commandStream struct {
	cmd     *exec.Cmd
	stdout  io.ReadCloser
	stderr  *bytes.Buffer
	once    sync.Once
	waitErr error
}

func (s *commandStream) Read(p []byte) (int, error) {
	n, err := s.stdout.Read(p)
	if errors.Is(err, io.EOF) {
		s.finish()
		if s.waitErr != nil {
			return n, s.waitErr
		}
	}
	return n, err
}

func (s *commandStream) Close() error {
	_ = s.stdout.Close()
	s.finish()
	return s.waitErr
}

func (s *commandStream) finish() {
	s.once.Do(func() {
		s.waitErr = s.cmd.Wait()
		if s.waitErr != nil {
			message := strings.TrimSpace(s.stderr.String())
			if message != "" {
				s.waitErr = fmt.Errorf("%s: %w: %s", s.cmd.Path, s.waitErr, message)
			}
		}
	})
}

// Logs returns bounded container logs for the specified service.
func (c *DockerServiceController) Logs(ctx context.Context, id services.ID, tail int) (string, error) {
	if _, err := c.service(id); err != nil {
		return "", err
	}

	if _, err := os.Stat(c.composePath); errors.Is(err, os.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", fmt.Errorf("stat compose file: %w", err)
	}

	if tail <= 0 {
		tail = 100
	}

	out, err := c.runCompose(ctx, "logs", "--tail", strconv.Itoa(tail), string(id))
	if err != nil {
		return "", fmt.Errorf("docker compose logs %s: %w", id, err)
	}
	return string(out), nil
}
