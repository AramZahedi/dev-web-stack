// Package agentdocker implements the WSL agent's fixed Docker service API.
// Callers can select catalog services, but can never supply an image, command,
// Compose file, or project name.
package agentdocker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agentapi"
	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/credentials"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

const (
	projectName    = "devstack"
	privateWebPort = 18080
	maxRequestBody = 1 << 20
	// DockerDataRoot is the canonical data-root configured by the managed WSL
	// Docker Engine; it never points into an NTFS mount.
	DockerDataRoot = "/data/devstack/docker"
)

var serviceIDs = []string{"apache", "mariadb", "mysql", "nginx", "phpmyadmin", "postgres", "redis"}

// Runner executes a fixed docker CLI invocation. Tests inject a fake runner;
// the production runner invokes Docker Engine only through Compose.
type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

// InputRunner is an optional runner extension for commands that take input on stdin.
type InputRunner interface {
	RunInput(ctx context.Context, input []byte, name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 3 * time.Second
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func (execRunner) RunInput(ctx context.Context, input []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 3 * time.Second
	cmd.Stdin = bytes.NewReader(input)
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
		for i := range service.Config {
			mount := &service.Config[i]
			if mount.Area != services.MountSecrets {
				continue
			}
			mount.Area = services.MountService
			mount.HostSubdir = path.Join("secrets", filepath.Base(mount.HostSubdir))
		}
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

// RenderCompose returns the fixed WSL service catalog for root without writing
// files or invoking Docker. It is also used by rootfs contract tests.
func RenderCompose(root string) ([]byte, error) {
	module, err := New(Config{Root: root})
	if err != nil {
		return nil, err
	}
	return services.RenderCompose(module.catalog, module.root)
}

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
	mux.HandleFunc("GET /v1/services/{id}/config", d.handleServiceConfig)
	mux.HandleFunc("POST /v1/services/{id}/content", d.handleServiceContent)
	mux.HandleFunc("POST /v1/services/{id}/apply", d.handleServiceApply)
	mux.HandleFunc("POST /v1/services/{id}/reset", d.handleServiceReset)
	mux.HandleFunc("POST /v1/services/{id}/credential", d.handleServiceCredential)
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
	if req.Action == "rotate-credentials" {
		return d.rotateCredentials(ctx, service.ID)
	}
	d.opMu.Lock()
	defer d.opMu.Unlock()
	if err := d.ensureCompose(); err != nil {
		return agentproto.ServiceResponse{}, err
	}
	if req.Action != "activate" && req.Action != "deactivate" && req.Action != "restart" && req.Action != "uninstall" {
		return agentproto.ServiceResponse{}, fmt.Errorf("agentdocker: unsupported action %q", req.Action)
	}
	deleteData := req.Action == "uninstall" && req.Config["confirm_delete"] == "true"
	backupPath := ""
	if deleteData {
		backupPath, err = d.backupServiceData(service.ID)
		if err != nil {
			return agentproto.ServiceResponse{}, fmt.Errorf("agentdocker: back up %s before deletion: %w", service.ID, err)
		}
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
		if deleteData {
			serviceRoot := filepath.Join(d.root, "services", string(service.ID))
			if err := os.RemoveAll(serviceRoot); err != nil {
				return agentproto.ServiceResponse{}, fmt.Errorf("agentdocker: delete %s after backup: %w", service.ID, err)
			}
		}
	}
	return agentproto.ServiceResponse{ID: string(service.ID), State: state, Message: backupPath}, nil
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
			err = json.Unmarshal([]byte(trimmed), &entries)
		} else {
			var single struct {
				State  string `json:"State"`
				Health string `json:"Health"`
			}
			if err = json.Unmarshal([]byte(trimmed), &single); err == nil {
				entries = append(entries, single)
			} else {
				lines := strings.Split(trimmed, "\n")
				var lineErr error
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					var item struct {
						State  string `json:"State"`
						Health string `json:"Health"`
					}
					if lErr := json.Unmarshal([]byte(line), &item); lErr != nil {
						lineErr = lErr
						break
					}
					entries = append(entries, item)
				}
				if lineErr != nil {
					return agentproto.ServiceResponse{}, fmt.Errorf("agentdocker: decode Compose status: %w", lineErr)
				}
				err = nil
			}
		}
		if err != nil {
			return agentproto.ServiceResponse{}, fmt.Errorf("agentdocker: decode Compose status: %w", err)
		}
	}
	state := string(services.StateNotInstalled)
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
	if err := ensureRealDirectory(filepath.Dir(d.composePath), 0o755); err != nil {
		return fmt.Errorf("agentdocker: validate Compose directory: %w", err)
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
	servicesRoot := filepath.Join(d.root, "services")
	if err := ensureRealDirectory(servicesRoot, 0o755); err != nil {
		return fmt.Errorf("agentdocker: validate services root: %w", err)
	}
	serviceRoot := filepath.Join(d.root, "services", string(id))
	if err := ensureRealDirectory(serviceRoot, 0o755); err != nil {
		return fmt.Errorf("agentdocker: validate %s root: %w", id, err)
	}
	for _, name := range []string{"config", "data", "logs", "secrets"} {
		mode := os.FileMode(0o755)
		if name == "secrets" {
			mode = 0o700
		}
		if err := ensureRealDirectory(filepath.Join(serviceRoot, name), mode); err != nil {
			return fmt.Errorf("agentdocker: create or secure %s/%s: %w", id, name, err)
		}
	}
	if err := d.ensureSecrets(id); err != nil {
		return err
	}
	return d.seedConfigs(id)
}

func (d *Docker) seedConfigs(id services.ID) error {
	serviceRoot := filepath.Join(d.root, "services", string(id))
	configDir := filepath.Join(serviceRoot, "config")
	initDir := filepath.Join(configDir, "init")

	writeFileIfAbsent := func(path string, content string, mode os.FileMode) error {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		return os.WriteFile(path, []byte(content), mode)
	}

	switch id {
	case services.IDRedis:
		return writeFileIfAbsent(filepath.Join(configDir, "redis.conf"), `# Generated by devstack as a safe starting point.
appendonly yes
appendfilename "appendonly.aof"
dir /data
protected-mode no
`, 0o644)

	case services.IDMySQL, services.IDMariaDB:
		if err := ensureRealDirectory(initDir, 0o755); err != nil {
			return err
		}
		if err := writeFileIfAbsent(filepath.Join(configDir, "my.cnf"), `# Generated by devstack as a safe starting point.
[mysqld]
skip-name-resolve
max_connections=256
character-set-server=utf8mb4
collation-server=utf8mb4_unicode_ci
`, 0o644); err != nil {
			return err
		}
		return writeFileIfAbsent(filepath.Join(initDir, "01-admin.sql"), `-- Generated by devstack as a safe starting point.
GRANT ALL PRIVILEGES ON *.* TO 'devstack'@'%' WITH GRANT OPTION;
FLUSH PRIVILEGES;
`, 0o644)

	case services.IDPostgreSQL, "postgres":
		if err := ensureRealDirectory(initDir, 0o755); err != nil {
			return err
		}
		if err := writeFileIfAbsent(filepath.Join(configDir, "postgresql.conf"), `# Generated by devstack as a safe starting point.
listen_addresses = '*'
port = 5432
data_directory = '/var/lib/postgresql/data'
hba_file = '/etc/postgresql/pg_hba.conf'
`, 0o644); err != nil {
			return err
		}
		if err := writeFileIfAbsent(filepath.Join(configDir, "pg_hba.conf"), `# Generated by devstack as a safe starting point.
local   all             postgres                                trust
local   all             all                                     trust
host    all             all             127.0.0.1/32            scram-sha-256
host    all             all             ::1/128                 scram-sha-256
host    all             all             0.0.0.0/0               scram-sha-256
`, 0o644); err != nil {
			return err
		}
		return writeFileIfAbsent(filepath.Join(initDir, "01-init.sh"), `#!/bin/sh
set -eu
if [ -z "${POSTGRES_USER:-}" ]; then POSTGRES_USER=postgres; fi
password="$POSTGRES_DEVSTACK_PASSWORD"
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<EOSQL
\set ds_password '$password'
SELECT format('CREATE ROLE devstack LOGIN CREATEDB CREATEROLE PASSWORD %L', :'ds_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'devstack')
\gexec
SELECT format('ALTER ROLE devstack WITH LOGIN CREATEDB CREATEROLE PASSWORD %L', :'ds_password')
\gexec
SELECT format('CREATE DATABASE devstack OWNER devstack')
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'devstack')
\gexec
EOSQL
`, 0o755)

	case services.IDPHPMyAdmin:
		return writeFileIfAbsent(filepath.Join(configDir, "config.user.inc.php"), `<?php
// Generated by devstack as a safe starting point.
$cfg['UploadDir'] = '';
$cfg['SaveDir'] = '';
`, 0o644)
	}

	return nil
}

func ensureRealDirectory(name string, mode os.FileMode) error {
	return ensureDirectoryTree(filepath.Clean(name), mode, true)
}

func ensureDirectoryTree(name string, mode os.FileMode, setMode bool) error {
	info, err := os.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		parent := filepath.Dir(name)
		if parent != name {
			if err := ensureDirectoryTree(parent, 0o755, false); err != nil {
				return err
			}
		}
		if err := os.Mkdir(name, mode); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err = os.Lstat(name)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("path is not a real directory")
	}
	if setMode {
		return os.Chmod(name, mode)
	}
	return nil
}

func (d *Docker) ensureSecrets(id services.ID) error {
	names := map[services.ID][]string{
		services.IDMySQL:   {"root_password", "devstack_password"},
		services.IDMariaDB: {"root_password", "devstack_password"},
		"postgres":         {"postgres_password", "devstack_password"},
	}
	secretNames := names[id]
	if len(secretNames) == 0 {
		return nil
	}
	dir := filepath.Join(d.root, "services", string(id), "secrets")
	if err := ensureRealDirectory(dir, 0o700); err != nil {
		return fmt.Errorf("agentdocker: secure %s secrets: %w", id, err)
	}
	for _, name := range secretNames {
		file := filepath.Join(dir, name)
		info, err := os.Lstat(file)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return fmt.Errorf("agentdocker: refusing unsafe secret file %s", name)
			}
			if err := os.Chmod(file, 0o600); err != nil {
				return fmt.Errorf("agentdocker: secure secret %s: %w", name, err)
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("agentdocker: inspect secret %s: %w", name, err)
		}
		secretVal, err := credentials.NewSecret()
		if err != nil {
			return fmt.Errorf("agentdocker: generate secret %s: %w", name, err)
		}
		value := []byte(secretVal + "\n")
		secret, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("agentdocker: create secret %s: %w", name, err)
		}
		if _, err := secret.Write(value); err != nil {
			_ = secret.Close()
			_ = os.Remove(file)
			return fmt.Errorf("agentdocker: write secret %s: %w", name, err)
		}
		if err := secret.Chmod(0o600); err != nil {
			_ = secret.Close()
			_ = os.Remove(file)
			return fmt.Errorf("agentdocker: set secret %s mode: %w", name, err)
		}
		if err := secret.Sync(); err != nil {
			_ = secret.Close()
			_ = os.Remove(file)
			return fmt.Errorf("agentdocker: sync secret %s: %w", name, err)
		}
		if err := secret.Close(); err != nil {
			return fmt.Errorf("agentdocker: close secret %s: %w", name, err)
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

func (d *Docker) composeInput(ctx context.Context, input []byte, args ...string) ([]byte, error) {
	all := []string{"compose", "--project-name", projectName, "--file", d.composePath}
	all = append(all, args...)
	if inRunner, ok := d.runner.(InputRunner); ok {
		return inRunner.RunInput(ctx, input, "docker", all...)
	}
	return d.runner.Run(ctx, "docker", all...)
}

func (d *Docker) rotateCredentials(ctx context.Context, id services.ID) (agentproto.ServiceResponse, error) {
	if id != services.IDMySQL && id != services.IDMariaDB && id != services.IDPostgreSQL && id != "postgres" {
		return agentproto.ServiceResponse{}, fmt.Errorf("agentdocker: service %q does not support credential rotation", id)
	}
	status, err := d.Status(ctx, string(id))
	if err != nil {
		return agentproto.ServiceResponse{}, fmt.Errorf("agentdocker: check %s status: %w", id, err)
	}
	if status.State != "active" {
		return agentproto.ServiceResponse{}, fmt.Errorf("agentdocker: service %q must be active to rotate credentials", id)
	}

	secretDir := filepath.Join(d.root, "services", string(id), "secrets")
	secretFile := filepath.Join(secretDir, "devstack_password")
	data, err := os.ReadFile(secretFile)
	if err != nil {
		return agentproto.ServiceResponse{}, fmt.Errorf("agentdocker: read %s old credential: %w", id, err)
	}
	oldPassword := strings.TrimSpace(string(data))

	newPassword, err := credentials.NewSecret()
	if err != nil {
		return agentproto.ServiceResponse{}, fmt.Errorf("agentdocker: generate new %s secret: %w", id, err)
	}

	newSQL, err := rotationSQL(id, newPassword)
	if err != nil {
		return agentproto.ServiceResponse{}, err
	}
	oldSQL, err := rotationSQL(id, oldPassword)
	if err != nil {
		return agentproto.ServiceResponse{}, err
	}
	args, err := rotationCommand(id)
	if err != nil {
		return agentproto.ServiceResponse{}, err
	}

	d.opMu.Lock()
	defer d.opMu.Unlock()

	if _, err := d.composeInput(ctx, []byte(newSQL), args...); err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = d.composeInput(rollbackCtx, []byte(oldSQL), args...)
		return agentproto.ServiceResponse{}, fmt.Errorf("agentdocker: rotate %s account: %w", id, err)
	}

	if err := writeAtomic(secretFile, []byte(newPassword+"\n"), 0o600); err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = d.composeInput(rollbackCtx, []byte(oldSQL), args...)
		return agentproto.ServiceResponse{}, fmt.Errorf("agentdocker: write %s new secret: %w", id, err)
	}

	return agentproto.ServiceResponse{ID: string(id), State: "active"}, nil
}

func rotationSQL(id services.ID, password string) (string, error) {
	switch id {
	case services.IDMySQL, services.IDMariaDB:
		return fmt.Sprintf("ALTER USER 'devstack'@'%%' IDENTIFIED BY '%s';\n", password), nil
	case services.IDPostgreSQL, "postgres":
		return fmt.Sprintf("ALTER ROLE devstack WITH PASSWORD '%s';\n", password), nil
	default:
		return "", fmt.Errorf("agentdocker: service %q does not support credential rotation", id)
	}
}

func rotationCommand(id services.ID) ([]string, error) {
	switch id {
	case services.IDMySQL:
		return []string{
			"exec", "-T", string(id), "sh", "-c",
			`set -eu; MYSQL_PWD="$(cat /run/secrets/mysql_root_password)"; export MYSQL_PWD; exec mysql --user=root --batch --skip-column-names`,
		}, nil
	case services.IDMariaDB:
		return []string{
			"exec", "-T", string(id), "sh", "-c",
			`set -eu; MYSQL_PWD="$(cat /run/secrets/mariadb_root_password)"; export MYSQL_PWD; exec mariadb --user=root --batch --skip-column-names`,
		}, nil
	case services.IDPostgreSQL, "postgres":
		return []string{
			"exec", "-T", string(id), "psql", "--no-psqlrc", "--set=ON_ERROR_STOP=1",
			"--username=postgres", "--dbname=postgres",
		}, nil
	default:
		return nil, fmt.Errorf("agentdocker: service %q does not support credential rotation", id)
	}
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
