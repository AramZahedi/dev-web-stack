package servicecontrol

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

type serviceAgentAPI interface {
	ServiceAction(context.Context, agentproto.ServiceRequest) (agentproto.ServiceResponse, error)
	ServiceStatus(context.Context, string) (agentproto.ServiceResponse, error)
	ServiceLogs(context.Context, string, agentproto.ServiceLogsRequest) (agentproto.ServiceLogsResponse, error)
	ServiceCredential(context.Context, agentproto.ServiceCredentialRequest) (agentproto.ServiceCredentialResponse, error)
}

// AgentDocker adapts the authenticated agent API to servicecontrol's existing
// lifecycle and preparer interfaces. It performs no filesystem operations;
// canonical service state stays in WSL ext4.
type AgentDocker struct {
	api   serviceAgentAPI
	paths *AgentPaths
}

var _ Helper = (*AgentDocker)(nil)

// NewAgentDocker creates the Windows-to-WSL service adapter.
func NewAgentDocker(api serviceAgentAPI, wslRoot string) (*AgentDocker, error) {
	if api == nil {
		return nil, errors.New("servicecontrol: agent API is required")
	}
	paths, err := NewAgentPaths(wslRoot)
	if err != nil {
		return nil, err
	}
	return &AgentDocker{api: api, paths: paths}, nil
}

// Status returns fixed-catalog service status with canonical WSL path metadata.
func (a *AgentDocker) Status(ctx context.Context) ([]helperproto.ServiceStatus, error) {
	out := make([]helperproto.ServiceStatus, 0, len(AllowedAgentServiceIDs()))
	for _, id := range AllowedAgentServiceIDs() {
		remote, err := a.api.ServiceStatus(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("servicecontrol: read %s status from agent: %w", id, err)
		}
		paths, err := a.paths.ForService(id)
		if err != nil {
			return nil, err
		}
		if remote.ID != "" && remote.ID != id {
			return nil, fmt.Errorf("servicecontrol: agent returned mismatched service ID %q for %q", remote.ID, id)
		}
		service, _ := services.DefaultCatalog().Get(agentCatalogID(id))
		state := remote.State
		if state == "" {
			state = string(services.StateNotInstalled)
		}
		status := helperproto.ServiceStatus{
			ID:         id,
			State:      state,
			Active:     state == string(services.StateActive) || state == "active",
			Installed:  state != string(services.StateNotInstalled) && state != "not-installed",
			Image:      service.Image,
			ConfigPath: paths.Config,
			DataPath:   paths.Data,
		}
		if id == "nginx" || id == "apache" {
			status.Ports = []helperproto.ServicePortStatus{{Host: 18080, Container: 18080, Protocol: "http", Loopback: true}}
		} else {
			for _, port := range service.Ports {
				status.Ports = append(status.Ports, helperproto.ServicePortStatus{
					Host: port.Host, Container: port.Container, Protocol: port.Protocol,
					Loopback: port.Loopback,
				})
			}
		}
		out = append(out, status)
	}
	return out, nil
}

// Install pulls one fixed service through the agent.
func (a *AgentDocker) Install(ctx context.Context, id string) error {
	return a.action(ctx, id, "install", nil)
}

// Activate starts one fixed service through the agent.
func (a *AgentDocker) Activate(ctx context.Context, id string) error {
	return a.action(ctx, id, "activate", nil)
}

// Deactivate stops one fixed service through the agent.
func (a *AgentDocker) Deactivate(ctx context.Context, id string) error {
	return a.action(ctx, id, "deactivate", nil)
}

// Restart restarts one fixed service through the agent.
func (a *AgentDocker) Restart(ctx context.Context, id string) error {
	return a.action(ctx, id, "restart", nil)
}

// Uninstall preserves service data unless deleteData was explicitly confirmed.
func (a *AgentDocker) Uninstall(ctx context.Context, id string, deleteData bool) error {
	config := map[string]string(nil)
	if deleteData {
		config = map[string]string{"confirm_delete": "true"}
	}
	return a.action(ctx, id, "uninstall", config)
}

// Logs reads a bounded log snapshot from the agent.
func (a *AgentDocker) Logs(ctx context.Context, id string, tail int) (string, error) {
	if err := validateAgentServiceID(id); err != nil {
		return "", err
	}
	if tail <= 0 {
		tail = 100
	}
	response, err := a.api.ServiceLogs(ctx, id, agentproto.ServiceLogsRequest{Tail: tail})
	if err != nil {
		return "", fmt.Errorf("servicecontrol: read %s logs from agent: %w", id, err)
	}
	return strings.Join(response.Lines, "\n"), nil
}

// Credentials explicitly reveals the agent-owned credential for supported
// database services. It never reads a Windows file.
func (a *AgentDocker) Credentials(ctx context.Context, id services.ID) (Credentials, error) {
	name := string(id)
	if err := validateAgentServiceID(name); err != nil {
		return Credentials{}, err
	}
	username, port, ok := databaseCredential(name)
	if !ok {
		return Credentials{}, fmt.Errorf("%w: service %q", ErrCredentialsUnsupported, name)
	}
	field := "devstack_password"
	response, err := a.api.ServiceCredential(ctx, agentproto.ServiceCredentialRequest{ID: name, Field: field})
	if err != nil {
		return Credentials{}, fmt.Errorf("servicecontrol: read %s credential from agent: %w", name, err)
	}
	if response.Value == "" {
		return Credentials{}, fmt.Errorf("servicecontrol: agent returned an empty %s credential", name)
	}
	if response.User != "" {
		username = response.User
	}
	database := "devstack"
	if name == "postgres" {
		database = "devstack"
	}
	return Credentials{Engine: name, Username: username, Password: response.Value, Database: database, Host: "127.0.0.1", Port: port}, nil
}

// RotateCredentials delegates rotation to the agent and reveals the result.
func (a *AgentDocker) RotateCredentials(ctx context.Context, id services.ID) (Credentials, error) {
	name := string(id)
	if _, _, ok := databaseCredential(name); !ok {
		return Credentials{}, fmt.Errorf("%w: service %q", ErrCredentialsUnsupported, name)
	}
	if err := a.action(ctx, name, "rotate-credentials", nil); err != nil {
		return Credentials{}, err
	}
	return a.Credentials(ctx, id)
}

// Preparer methods are intentionally no-ops. The agent scaffolds config, data,
// and secret trees as part of its install operation; the Windows daemon never
// creates a local duplicate.
func (*AgentDocker) PrepareInstall(context.Context, services.Service) error         { return nil }
func (*AgentDocker) PrepareActivate(context.Context, services.Service) error        { return nil }
func (*AgentDocker) CommitActivate(context.Context, services.Service) error         { return nil }
func (*AgentDocker) CleanupActivate(context.Context, services.Service, error) error { return nil }

func (a *AgentDocker) action(ctx context.Context, id, action string, config map[string]string) error {
	if err := validateAgentServiceID(id); err != nil {
		return err
	}
	_, err := a.api.ServiceAction(ctx, agentproto.ServiceRequest{ID: id, Action: action, Config: config})
	if err != nil {
		return fmt.Errorf("servicecontrol: %s %s through agent: %w", action, id, err)
	}
	return nil
}

// AllowedAgentServiceIDs returns all fixed service identifiers accepted by
// the Windows service adapter.
func AllowedAgentServiceIDs() []string {
	return []string{"nginx", "apache", "mysql", "mariadb", "postgres", "redis", "phpmyadmin"}
}

func validateAgentServiceID(id string) error {
	if _, ok := fixedAgentServiceIDs[id]; !ok {
		return fmt.Errorf("servicecontrol: unsupported service ID %q", id)
	}
	return nil
}

func agentCatalogID(id string) services.ID {
	if id == "postgres" {
		return services.IDPostgreSQL
	}
	return services.ID(id)
}

func databaseCredential(id string) (username string, port int, ok bool) {
	switch id {
	case "mysql", "mariadb":
		return "devstack", 3306, true
	case "postgres":
		return "devstack", 5432, true
	default:
		return "", 0, false
	}
}
