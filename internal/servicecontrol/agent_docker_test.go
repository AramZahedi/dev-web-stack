package servicecontrol_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/servicecontrol"
)

type fakeAgentServiceAPI struct {
	actions     []agentproto.ServiceRequest
	statuses    map[string]agentproto.ServiceResponse
	logs        map[string]agentproto.ServiceLogsResponse
	credentials map[string]agentproto.ServiceCredentialResponse
	err         error
}

func (f *fakeAgentServiceAPI) ServiceAction(_ context.Context, req agentproto.ServiceRequest) (agentproto.ServiceResponse, error) {
	f.actions = append(f.actions, req)
	return agentproto.ServiceResponse{ID: req.ID, State: req.Action}, f.err
}
func (f *fakeAgentServiceAPI) ServiceStatus(_ context.Context, id string) (agentproto.ServiceResponse, error) {
	if f.err != nil {
		return agentproto.ServiceResponse{}, f.err
	}
	return f.statuses[id], nil
}
func (f *fakeAgentServiceAPI) ServiceLogs(_ context.Context, id string, _ agentproto.ServiceLogsRequest) (agentproto.ServiceLogsResponse, error) {
	return f.logs[id], f.err
}
func (f *fakeAgentServiceAPI) ServiceCredential(_ context.Context, req agentproto.ServiceCredentialRequest) (agentproto.ServiceCredentialResponse, error) {
	return f.credentials[req.ID], f.err
}

func TestAgentDockerLifecycleAndLogsStayOnAgent(t *testing.T) {
	api := &fakeAgentServiceAPI{logs: map[string]agentproto.ServiceLogsResponse{"mysql": {Lines: []string{"ready"}}}}
	adapter, err := servicecontrol.NewAgentDocker(api, "/data/devstack")
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Install(context.Background(), "mysql"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Activate(context.Background(), "mysql"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Deactivate(context.Background(), "mysql"); err != nil {
		t.Fatal(err)
	}
	logs, err := adapter.Logs(context.Background(), "mysql", 25)
	if err != nil {
		t.Fatal(err)
	}
	if logs != "ready" {
		t.Fatalf("Logs() = %q, want ready", logs)
	}
	want := []agentproto.ServiceRequest{{ID: "mysql", Action: "install"}, {ID: "mysql", Action: "activate"}, {ID: "mysql", Action: "deactivate"}}
	if !reflect.DeepEqual(api.actions, want) {
		t.Fatalf("agent actions = %+v, want %+v", api.actions, want)
	}
}

func TestAgentDockerWebValidationAndReloadUseFixedAgentActions(t *testing.T) {
	api := &fakeAgentServiceAPI{}
	adapter, err := servicecontrol.NewAgentDocker(api, "/data/devstack")
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Validate(context.Background(), "nginx"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Reload(context.Background(), "apache"); err != nil {
		t.Fatal(err)
	}
	want := []agentproto.ServiceRequest{{ID: "nginx", Action: "validate"}, {ID: "apache", Action: "reload"}}
	if !reflect.DeepEqual(api.actions, want) {
		t.Fatalf("agent validation/reload actions = %+v, want %+v", api.actions, want)
	}
}

func TestAgentDockerStatusUsesExt4PathsAndFixedPorts(t *testing.T) {
	api := &fakeAgentServiceAPI{statuses: map[string]agentproto.ServiceResponse{
		"nginx":    {ID: "nginx", State: "active"},
		"postgres": {ID: "postgres", State: "installed"},
	}}
	adapter, err := servicecontrol.NewAgentDocker(api, "/data/devstack")
	if err != nil {
		t.Fatal(err)
	}
	statuses, err := adapter.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]helperproto.ServiceStatus{}
	for _, status := range statuses {
		byID[status.ID] = status
	}
	nginx := byID["nginx"]
	if !nginx.Active || nginx.ConfigPath != "/data/devstack/services/nginx/config" || nginx.DataPath != "/data/devstack/services/nginx/data" {
		t.Fatalf("nginx status = %+v, want active status backed by WSL ext4", nginx)
	}
	if len(nginx.Ports) != 1 || nginx.Ports[0].Host != 18080 || nginx.Ports[0].Container != 18080 {
		t.Fatalf("nginx ports = %+v, want private HTTP port 18080", nginx.Ports)
	}
	if got := byID["postgres"].DataPath; got != "/data/devstack/services/postgres/data" {
		t.Fatalf("postgres data path = %q", got)
	}
	for _, status := range statuses {
		if status.ConfigPath == "" || status.DataPath == "" || status.ConfigPath[0] != '/' || status.DataPath[0] != '/' {
			t.Errorf("service paths are not WSL absolute paths: %+v", status)
		}
	}
}

func TestAgentDockerCredentialsAreExplicitAgentReads(t *testing.T) {
	api := &fakeAgentServiceAPI{credentials: map[string]agentproto.ServiceCredentialResponse{
		"postgres": {ID: "postgres", User: "devstack", Value: "secret-from-agent"},
	}}
	adapter, err := servicecontrol.NewAgentDocker(api, "/data/devstack")
	if err != nil {
		t.Fatal(err)
	}
	credential, err := adapter.Credentials(context.Background(), "postgres")
	if err != nil {
		t.Fatal(err)
	}
	if credential.Password != "secret-from-agent" || credential.Host != "127.0.0.1" || credential.Port != 5432 {
		t.Fatalf("credentials = %+v", credential)
	}
	if _, err := adapter.Credentials(context.Background(), "nginx"); err == nil {
		t.Fatal("credentials for a service without an explicit database credential were accepted")
	}
}

func TestAgentDockerErrorsPreserveAgentCause(t *testing.T) {
	wantErr := errors.New("agent offline")
	adapter, err := servicecontrol.NewAgentDocker(&fakeAgentServiceAPI{err: wantErr}, "/data/devstack")
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Activate(context.Background(), "mysql"); !errors.Is(err, wantErr) {
		t.Fatalf("Activate error = %v, want wrapped agent error", err)
	}
}
