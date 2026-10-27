package servicecontrol_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/servicecontrol"
)

type fakeAgentConfigAPI struct {
	reads   []string
	writes  []agentproto.ServiceConfigRequest
	resets  []agentproto.ServiceConfigRequest
	listing agentproto.ServiceConfigResponse
	content agentproto.ServiceContentResponse
}

func (f *fakeAgentConfigAPI) ServiceConfig(_ context.Context, id string) (agentproto.ServiceConfigResponse, error) {
	f.reads = append(f.reads, id)
	return f.listing, nil
}
func (f *fakeAgentConfigAPI) ServiceContent(_ context.Context, req agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error) {
	f.reads = append(f.reads, req.ID+":"+req.Rel)
	return f.content, nil
}
func (f *fakeAgentConfigAPI) ServiceApply(_ context.Context, req agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error) {
	f.writes = append(f.writes, req)
	return f.content, nil
}
func (f *fakeAgentConfigAPI) ServiceReset(_ context.Context, req agentproto.ServiceConfigRequest) error {
	f.resets = append(f.resets, req)
	return nil
}

func TestAgentServiceConfigUsesOnlyTypedAgentRequests(t *testing.T) {
	api := &fakeAgentConfigAPI{
		listing: agentproto.ServiceConfigResponse{Files: []agentproto.ServiceConfigFile{{Path: "my.cnf", Class: "custom"}}},
		content: agentproto.ServiceContentResponse{Path: "my.cnf", Content: "[mysqld]\n", Hash: "hash"},
	}
	config, err := servicecontrol.NewAgentServiceConfig(api, "/data/devstack")
	if err != nil {
		t.Fatal(err)
	}
	listing, err := config.List(context.Background(), "mysql")
	if err != nil || len(listing.Files) != 1 {
		t.Fatalf("List() = %+v, %v", listing, err)
	}
	content, err := config.Content(context.Background(), agentproto.ServiceConfigRequest{ID: "mysql", Rel: "my.cnf"})
	if err != nil || content.Content != "[mysqld]\n" {
		t.Fatalf("Content() = %+v, %v", content, err)
	}
	request := agentproto.ServiceConfigRequest{ID: "mysql", Rel: "my.cnf", Content: "next\n"}
	if _, err := config.Apply(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if err := config.Reset(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(api.writes) != 1 || !reflect.DeepEqual(api.writes[0], request) || len(api.resets) != 1 || !reflect.DeepEqual(api.resets[0], request) {
		t.Fatalf("agent config writes/resets = %+v/%+v", api.writes, api.resets)
	}
}

func TestAgentServiceConfigRejectsNTFSAndSensitivePaths(t *testing.T) {
	config, err := servicecontrol.NewAgentServiceConfig(&fakeAgentConfigAPI{}, "/data/devstack")
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []agentproto.ServiceConfigRequest{
		{ID: "mysql", Rel: `C:\devstack\services\mysql\config\my.cnf`},
		{ID: "mysql", Rel: "../secrets/root_password"},
		{ID: "mysql", Rel: "password.cnf"},
		{ID: "postgresql", Rel: "postgresql.conf"},
	} {
		if _, err := config.Content(context.Background(), request); err == nil {
			t.Errorf("Content(%+v) accepted an unsafe config request", request)
		}
	}
}
