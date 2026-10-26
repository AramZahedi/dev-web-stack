// Package agentdocker implements the WSL agent's fixed Docker service API.
// Callers can select catalog services, but can never supply an image, command,
// Compose file, or project name.
package agentdocker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/AramZahedi/dev-web-stack/internal/agentapi"
	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

const (
	projectName    = "devstack"
	privateWebPort = 18080
	maxRequestBody = 1 << 20
)

var serviceIDs = []string{"apache", "mariadb", "mysql", "nginx", "phpmyadmin", "postgres", "redis"}

// Runner executes a fixed docker CLI invocation. Tests inject a fake runner;
// the production runner invokes Docker Engine only through Compose.
type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

// Config binds the module to the agent's WSL root. Root is supplied by the
// trusted agent configuration, never by an HTTP request.
type Config struct {
	Root    string
	Runner  Runner
	Catalog services.Catalog
}

// Docker is a fixed-catalog service module.
type Docker struct {
	root        string
	composePath string
	runner      Runner
	catalog     services.Catalog
	opMu        sync.Mutex
}

var _ agentapi.Module = (*Docker)(nil)

// New constructs the module. The empty root selects the canonical WSL root.
func New(cfg Config) (*Docker, error) {
	root := strings.TrimSpace(cfg.Root)
	if root == "" {
		root = "/data/devstack"
	}
	if !filepath.IsAbs(root) {
		return nil, errors.New("agentdocker: root must be absolute")
	}
	root = filepath.Clean(root)
	catalog := fixedCatalog(cfg.Catalog)
	// Service mounts describe Linux container paths. On the Windows build host,
	// filepath.IsAbs applies Windows path rules to those POSIX paths, so the
	// catalog validation runs in the Linux/WSL agent process only.
	if runtime.GOOS != "windows" {
		if err := catalog.Validate(); err != nil {
			return nil, fmt.Errorf("agentdocker: invalid fixed catalog: %w", err)
		}
	}
	runner := cfg.Runner
	if runner == nil {
		runner = execRunner{}
	}
	return &Docker{
		root:        root,
		composePath: filepath.Join(root, "services", "compose.yaml"),
		runner:      runner,
		catalog:     catalog,
	}, nil
}

func fixedCatalog(input services.Catalog) services.Catalog {
	if len(input.Services) == 0 {
		input = services.DefaultCatalog()
	}
	copyCatalog := services.Catalog{Services: make([]services.Service, 0, len(input.Services))}
	for _, service := range input.Services {
		service.ID = canonicalID(service.ID)
		if service.ID == services.IDNginx || service.ID == services.IDApache {
			service.Ports = []services.Port{{Host: privateWebPort, Container: privateWebPort, Protocol: "http"}}
		}
		copyCatalog.Services = append(copyCatalog.Services, service)
	}
	sort.Slice(copyCatalog.Services, func(i, j int) bool { return copyCatalog.Services[i].ID < copyCatalog.Services[j].ID })
	return copyCatalog
}

func canonicalID(id services.ID) services.ID {
	if id == services.IDPostgreSQL || id == "postgres" {
		return services.ID("postgres")
	}
	return id
}

// AllowedServiceIDs returns a copy of the only IDs accepted by this module.
func AllowedServiceIDs() []string { return append([]string(nil), serviceIDs...) }

// ProjectName is fixed for the managed WSL Engine. User input cannot select a
// project that could control unrelated containers.
func (d *Docker) ProjectName() string { return projectName }

func (*Docker) Name() string      { return "docker-services" }
func (*Docker) MountPath() string { return "/v1/services/" }

func (d *Docker) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/services/{id}/action", d.handleAction)
	mux.HandleFunc("GET /v1/services/{id}/status", d.handleStatus)
	mux.HandleFunc("POST /v1/services/{id}/logs", d.handleLogs)
	return mux
}

// Install pulls a digest-pinned image for one fixed service.
func (d *Docker) Install(ctx context.Context, id string) error {
	service, err := d.service(id)
	if err != nil {
		return err
	}
	d.opMu.Lock()
	defer d.opMu.Unlock()
	if err := d.ensureCompose(); err != nil {
		return err
	}
	if err := d.scaffold(service.ID); err != nil {
		return err
	}
	_, err = d.compose(ctx, "pull", string(service.ID))
	if err != nil {
		return fmt.Errorf("agentdocker: pull %s: %w", service.ID, err)
	}
	return nil
}

// Action performs a closed set of lifecycle operations.
func (d *Docker) Action(ctx context.Context, req agentproto.ServiceRequest) (agentproto.ServiceResponse, error) {
	service, err := d.service(req.ID)
	if err != nil {
		return agentproto.ServiceResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return agentproto.ServiceResponse{}, err
	}
	if req.Action == "install" {
		if err := d.Install(ctx, req.ID); err != nil {
			return agentproto.ServiceResponse{}, err
		}
		return agentproto.ServiceResponse{ID: string(service.ID), State: "installed"}, nil
	}
	if req.Action == "validate" || req.Action == "reload" {
		if service.Group != services.GroupWeb {
			return agentproto.ServiceResponse{}, fmt.Errorf("agentdocker: %s is supported only for nginx and apache", req.Action)
		}
		command := service.ValidateCommand
		if req.Action == "reload" {
			command = service.ReloadCommand
		}
		if len(command) == 0 {
			return agentproto.ServiceResponse{}, fmt.Errorf("agentdocker: catalog has no %s command for %s", req.Action, service.ID)
		}
		d.opMu.Lock()
		defer d.opMu.Unlock()
		if err := d.ensureCompose(); err != nil {
			return agentproto.ServiceResponse{}, err
		}
		args := []string{"exec", "-T", string(service.ID)}
		args = append(args, command...)
		if _, err := d.compose(ctx, args...); err != nil {
			return agentproto.ServiceResponse{}, fmt.Errorf("agentdocker: %s %s: %w", req.Action, service.ID, err)
		}
		return agentproto.ServiceResponse{ID: string(service.ID), State: "active"}, nil
	}
	d.opMu.Lock()
	defer d.opMu.Unlock()
	if err := d.ensureCompose(); err != nil {
		return agentproto.ServiceResponse{}, err
	}
	var args []string
	switch req.Action {
	case "activate":
		if err := d.scaffold(service.ID); err != nil {
			return agentproto.ServiceResponse{}, err
		}
		args = []string{"up", "-d", "--no-deps", string(service.ID)}
	case "deactivate":
		args = []string{"stop", string(service.ID)}
	case "restart":
		args = []string{"restart", string(service.ID)}
	case "uninstall":
		args = []string{"stop", string(service.ID)}
	default:
		return agentproto.ServiceResponse{}, fmt.Errorf("agentdocker: unsupported action %q", req.Action)
	}
	if _, err := d.compose(ctx, args...); err != nil {
		return agentproto.ServiceResponse{}, fmt.Errorf("agentdocker: %s %s: %w", req.Action, service.ID, err)
	}
	state := map[string]string{"activate": "active", "deactivate": "stopped", "restart": "active", "uninstall": "uninstalled"}[req.Action]
	if req.Action == "uninstall" {
		if _, err := d.compose(ctx, "rm", "-f", string(service.ID)); err != nil {
			return agentproto.ServiceResponse{}, fmt.Errorf("agentdocker: remove %s: %w", service.ID, err)
		}
		if req.Config["confirm_delete"] == "true" {
			return agentproto.ServiceResponse{}, errors.New("agentdocker: data deletion requires the verified backup workflow")
		}
	}
	return agentproto.ServiceResponse{ID: string(service.ID), State: state}, nil
}

// Status returns the fixed service's current Compose state.
func (d *Docker) Status(ctx context.Context, id string) (agentproto.ServiceResponse, error) {
	service, err := d.service(id)
	if err != nil {
		return agentproto.ServiceResponse{}, err
	}
	if err := d.ensureCompose(); err != nil {
		return agentproto.ServiceResponse{}, err
	}
	out, err := d.compose(ctx, "ps", "-a", "--format", "json", string(service.ID))
	if err != nil {
		return agentproto.ServiceResponse{}, err
	}
	var entries []struct {
		State  string `json:"State"`
		Health string `json:"Health"`
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed != "" {
		if strings.HasPrefix(trimmed, "[") {
			err = json.Unmarshal(out, &entries)
		} else {
			err = json.Unmarshal(out, &entries)
		}
		if err != nil {
			return agentproto.ServiceResponse{}, fmt.Errorf("agentdocker: decode Compose status: %w", err)
		}
	}
	state := "not-installed"
	if d.scaffoldPresent(service.ID) {
		state = "installed"
	}
	if len(entries) > 0 && strings.EqualFold(entries[0].State, "running") {
		state = "active"
	} else if len(entries) > 0 {
		state = "stopped"
	}
	return agentproto.ServiceResponse{ID: string(service.ID), State: state, Ports: map[string]int{"private_http": privateWebPort}}, nil
}

func (d *Docker) handleAction(w http.ResponseWriter, r *http.Request) {
	var request agentproto.ServiceRequest
	if err := decodeRequest(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.ID != r.PathValue("id") {
		writeError(w, http.StatusBadRequest, errors.New("service ID does not match URL"))
		return
	}
	response, err := d.Action(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (d *Docker) handleStatus(w http.ResponseWriter, r *http.Request) {
	response, err := d.Status(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (d *Docker) handleLogs(w http.ResponseWriter, r *http.Request) {
	service, err := d.service(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	tail := 100
	if raw := r.URL.Query().Get("tail"); raw != "" {
		tail, err = strconv.Atoi(raw)
		if err != nil || tail < 1 || tail > 10000 {
			writeError(w, http.StatusBadRequest, errors.New("tail must be between 1 and 10000"))
			return
		}
	}
	if err := d.ensureCompose(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out, err := d.compose(r.Context(), "logs", "--tail", strconv.Itoa(tail), string(service.ID))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, agentproto.ServiceLogsResponse{Lines: strings.Split(strings.TrimRight(string(out), "\r\n"), "\n")})
}

func decodeRequest(w http.ResponseWriter, r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return errors.New("request contains trailing JSON")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, agentproto.ErrorResponse{Code: agentproto.CodeInvalidRequest, Message: err.Error()})
}

func (d *Docker) service(id string) (services.Service, error) {
	if !contains(serviceIDs, id) {
		return services.Service{}, fmt.Errorf("agentdocker: unsupported service ID %q", id)
	}
	service, ok := d.catalog.Get(services.ID(id))
	if !ok {
		return services.Service{}, fmt.Errorf("agentdocker: service %q is not in the fixed catalog", id)
	}
	if !strings.Contains(service.Image, "@sha256:") {
		return services.Service{}, fmt.Errorf("agentdocker: service %q image is not digest pinned", id)
	}
	return service, nil
}

func (d *Docker) ensureCompose() error {
	data, err := services.RenderCompose(d.catalog, d.root)
	if err != nil {
		return fmt.Errorf("agentdocker: render fixed Compose project: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(d.composePath), 0o755); err != nil {
		return fmt.Errorf("agentdocker: create Compose directory: %w", err)
	}
	if existing, err := os.ReadFile(d.composePath); err == nil && string(existing) == string(data) {
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(d.composePath), "compose-*.tmp")
	if err != nil {
		return fmt.Errorf("agentdocker: create Compose temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("agentdocker: write Compose file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("agentdocker: set Compose permissions: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("agentdocker: sync Compose file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("agentdocker: close Compose file: %w", err)
	}
	if err := os.Rename(tmpName, d.composePath); err != nil {
		return fmt.Errorf("agentdocker: publish Compose file: %w", err)
	}
	return nil
}

func (d *Docker) scaffold(id services.ID) error {
	serviceRoot := filepath.Join(d.root, "services", string(id))
	for _, name := range []string{"config", "data", "logs", "secrets"} {
		mode := os.FileMode(0o755)
		if name == "secrets" {
			mode = 0o700
		}
		if err := os.MkdirAll(filepath.Join(serviceRoot, name), mode); err != nil {
			return fmt.Errorf("agentdocker: create %s/%s: %w", id, name, err)
		}
		if err := os.Chmod(filepath.Join(serviceRoot, name), mode); err != nil {
			return fmt.Errorf("agentdocker: set %s/%s permissions: %w", id, name, err)
		}
	}
	return nil
}

func (d *Docker) scaffoldPresent(id services.ID) bool {
	_, err := os.Stat(filepath.Join(d.root, "services", string(id), "config"))
	return err == nil
}

func (d *Docker) compose(ctx context.Context, args ...string) ([]byte, error) {
	all := []string{"compose", "--project-name", projectName, "--file", d.composePath}
	all = append(all, args...)
	out, err := d.runner.Run(ctx, "docker", all...)
	if err != nil {
		return out, err
	}
	return out, nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
