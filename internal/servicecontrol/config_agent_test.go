package servicecontrol_test

import (
	"context"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/servicecontrol"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

func TestConfigServiceWithAgent(t *testing.T) {
	root := t.TempDir()
	api := &fakeAgentConfigAPI{
		listing: agentproto.ServiceConfigResponse{
			Files: []agentproto.ServiceConfigFile{
				{Path: "my.cnf", Class: "custom", Hash: "current-hash"},
			},
		},
		content: agentproto.ServiceContentResponse{
			Path:    "my.cnf",
			Content: "[mysqld]\nport=3306\n",
			Hash:    "current-hash",
		},
	}

	svc, err := servicecontrol.NewConfigService(servicecontrol.ConfigDeps{
		Root:    root,
		Catalog: services.DefaultCatalog(),
		Agent:   api,
	})
	if err != nil {
		t.Fatalf("NewConfigService: %v", err)
	}

	ctx := context.Background()

	// 1. List
	listing, err := svc.List(ctx, services.IDMySQL)
	if err != nil {
		t.Fatalf("svc.List: %v", err)
	}
	if len(listing.Files) != 1 || listing.Files[0].Path != "my.cnf" {
		t.Fatalf("listing = %+v, want 1 file 'my.cnf'", listing)
	}

	// 2. Content
	content, err := svc.Content(ctx, services.IDMySQL, "my.cnf")
	if err != nil {
		t.Fatalf("svc.Content: %v", err)
	}
	if content.Content != "[mysqld]\nport=3306\n" {
		t.Fatalf("content = %q, want [mysqld]\nport=3306\n", content.Content)
	}

	// 3. Diff
	diff, err := svc.Diff(ctx, services.IDMySQL, "my.cnf", "[mysqld]\nport=3307\n")
	if err != nil {
		t.Fatalf("svc.Diff: %v", err)
	}
	if diff.Path != "my.cnf" {
		t.Errorf("diff.Path = %q, want 'my.cnf'", diff.Path)
	}
	if diff.Diff == "" {
		t.Errorf("diff.Diff is empty, want unified diff")
	}

	// 4. Apply
	applyRes, err := svc.Apply(ctx, services.IDMySQL, servicecontrol.ConfigApplyRequest{
		Path:    "my.cnf",
		Content: "[mysqld]\nport=3307\n",
	})
	if err != nil {
		t.Fatalf("svc.Apply: %v", err)
	}
	if applyRes.Path != "my.cnf" || !applyRes.Changed {
		t.Fatalf("applyRes = %+v, want changed: true", applyRes)
	}
	if len(api.writes) != 1 || api.writes[0].Content != "[mysqld]\nport=3307\n" {
		t.Fatalf("api.writes = %+v, want apply with port=3307", api.writes)
	}

	// 5. Reset
	resetRes, err := svc.Reset(ctx, services.IDMySQL, "my.cnf")
	if err != nil {
		t.Fatalf("svc.Reset: %v", err)
	}
	if resetRes.Path != "my.cnf" || !resetRes.Changed {
		t.Fatalf("resetRes = %+v, want changed: true", resetRes)
	}
	if len(api.resets) != 1 || api.resets[0].Rel != "my.cnf" {
		t.Fatalf("api.resets = %+v, want reset for 'my.cnf'", api.resets)
	}
}
