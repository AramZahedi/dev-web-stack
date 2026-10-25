package helper

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

// ServiceController defines the operations supported by the container control plane.
type ServiceController interface {
	Status(ctx context.Context) ([]helperproto.ServiceStatus, error)
	Install(ctx context.Context, id services.ID) error
	Activate(ctx context.Context, id services.ID) error
	Deactivate(ctx context.Context, id services.ID) error
	Uninstall(ctx context.Context, id services.ID, deleteData bool) error
	Logs(ctx context.Context, id services.ID, tail int) (string, error)
}

// DockerServiceController manages service containers via fixed Docker Compose argv calls.
type DockerServiceController struct {
	root        string
	catalog     services.Catalog
	runner      CommandRunner
	projectName string
	composePath string
	mu          sync.Mutex
}

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
	}
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

	tmpFile, err := os.CreateTemp(dir, ".compose-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp compose file: %w", err)
	}
	tmpName := tmpFile.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write temp compose file: %w", err)
	}
	if err := tmpFile.Chmod(0o644); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("chmod temp compose file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp compose file: %w", err)
	}
	if err := os.Rename(tmpName, c.composePath); err != nil {
		return fmt.Errorf("rename compose file: %w", err)
	}
	return nil
}

type psContainer struct {
	ID      string `json:"ID"`
	Name    string `json:"Name"`
	Service string `json:"Service"`
	State   string `json:"State"`
	Status  string `json:"Status"`
	Health  string `json:"Health"`
}

func parsePSOutput(data []byte) []psContainer {
	var list []psContainer
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "[") {
		_ = json.Unmarshal([]byte(trimmed), &list)
		return list
	}
	lines := strings.Split(trimmed, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item psContainer
		if err := json.Unmarshal([]byte(line), &item); err == nil && (item.Service != "" || item.Name != "") {
			list = append(list, item)
		}
	}
	return list
}

// Status inspects the docker compose project and returns status for all catalog services.
func (c *DockerServiceController) Status(ctx context.Context) ([]helperproto.ServiceStatus, error) {
	if _, err := os.Stat(c.composePath); os.IsNotExist(err) {
		if err := c.ensureComposeFile(); err != nil {
			return nil, err
		}
	}

	out, err := c.runner.Run(ctx, "docker", "compose", "-p", c.projectName, "-f", c.composePath, "ps", "--format", "json")
	if err != nil {
		// When docker or compose ps returns an error, still return catalog with inactive states
		out = nil
	}

	containers := parsePSOutput(out)
	containerMap := make(map[string]psContainer, len(containers))
	for _, cont := range containers {
		svcName := cont.Service
		if svcName == "" && strings.HasPrefix(cont.Name, "devstack-") {
			svcName = strings.TrimPrefix(cont.Name, "devstack-")
		}
		if svcName != "" {
			containerMap[svcName] = cont
		}
	}

	statuses := make([]helperproto.ServiceStatus, 0, len(c.catalog.Services))
	for _, svc := range c.catalog.Services {
		idStr := string(svc.ID)
		st := helperproto.ServiceStatus{
			ID:     idStr,
			Image:  svc.Image,
			State:  string(services.StateNotInstalled),
			Active: false,
		}

		for _, p := range svc.Ports {
			st.Ports = append(st.Ports, helperproto.ServicePortStatus{
				Host:      p.Host,
				Container: p.Container,
				Protocol:  p.Protocol,
				Loopback:  p.Loopback,
			})
		}

		if cont, ok := containerMap[idStr]; ok {
			st.ContainerID = cont.ID
			st.State = cont.State
			st.Active = strings.EqualFold(cont.State, "running")
			st.Health = cont.Health
		}

		statuses = append(statuses, st)
	}

	return statuses, nil
}

// Install pulls the container image for the specified service.
func (c *DockerServiceController) Install(ctx context.Context, id services.ID) error {
	if _, ok := c.catalog.Get(id); !ok {
		return fmt.Errorf("%w: unknown service %q", helperproto.ErrInvalidRequest, id)
	}

	if err := c.ensureComposeFile(); err != nil {
		return err
	}

	_, err := c.runner.Run(ctx, "docker", "compose", "-p", c.projectName, "-f", c.composePath, "pull", string(id))
	return err
}

// Activate starts the specified service container.
func (c *DockerServiceController) Activate(ctx context.Context, id services.ID) error {
	if _, ok := c.catalog.Get(id); !ok {
		return fmt.Errorf("%w: unknown service %q", helperproto.ErrInvalidRequest, id)
	}

	if err := c.ensureComposeFile(); err != nil {
		return err
	}

	_, err := c.runner.Run(ctx, "docker", "compose", "-p", c.projectName, "-f", c.composePath, "up", "-d", "--no-deps", string(id))
	return err
}

// Deactivate stops the specified service container.
func (c *DockerServiceController) Deactivate(ctx context.Context, id services.ID) error {
	if _, ok := c.catalog.Get(id); !ok {
		return fmt.Errorf("%w: unknown service %q", helperproto.ErrInvalidRequest, id)
	}

	_, err := c.runner.Run(ctx, "docker", "compose", "-p", c.projectName, "-f", c.composePath, "stop", string(id))
	return err
}

// Uninstall stops and removes the specified service container and optionally purges its data directory.
func (c *DockerServiceController) Uninstall(ctx context.Context, id services.ID, deleteData bool) error {
	if _, ok := c.catalog.Get(id); !ok {
		return fmt.Errorf("%w: unknown service %q", helperproto.ErrInvalidRequest, id)
	}

	if _, err := c.runner.Run(ctx, "docker", "compose", "-p", c.projectName, "-f", c.composePath, "rm", "-f", string(id)); err != nil {
		return err
	}

	if deleteData {
		dataDir := filepath.Join(c.root, "services", string(id), "data")
		_ = os.RemoveAll(dataDir)
	}

	return nil
}

// Logs returns bounded container logs for the specified service.
func (c *DockerServiceController) Logs(ctx context.Context, id services.ID, tail int) (string, error) {
	if _, ok := c.catalog.Get(id); !ok {
		return "", fmt.Errorf("%w: unknown service %q", helperproto.ErrInvalidRequest, id)
	}

	if tail <= 0 {
		tail = 100
	}

	out, err := c.runner.Run(ctx, "docker", "compose", "-p", c.projectName, "-f", c.composePath, "logs", "--tail", strconv.Itoa(tail), string(id))
	if err != nil {
		return "", err
	}
	return string(out), nil
}
