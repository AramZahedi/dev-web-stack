package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/services"
)

func TestServiceListTable(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{
		servicesList: []services.View{
			{
				ID:          services.IDNginx,
				DisplayName: "Nginx Web Server",
				Group:       services.GroupWeb,
				State:       services.StateActive,
				Active:      true,
				Ports: []services.Port{
					{Host: 80, Container: 80, Protocol: "http", Loopback: true},
				},
			},
		},
	}

	out := runCLI(t, fake, "service", "list")
	for _, heading := range []string{"NAME", "GROUP", "STATE", "ACTIVE", "PORT"} {
		if !strings.Contains(out, heading) {
			t.Errorf("output missing heading %q, got:\n%s", heading, out)
		}
	}
	if !strings.Contains(out, "Nginx Web Server") || !strings.Contains(out, "web") || !strings.Contains(out, "active") {
		t.Errorf("output missing service values, got:\n%s", out)
	}
}

func TestServiceListEmpty(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{
		servicesList: []services.View{},
	}

	out := runCLI(t, fake, "service", "list")
	for _, heading := range []string{"NAME", "GROUP", "STATE", "ACTIVE", "PORT"} {
		if !strings.Contains(out, heading) {
			t.Errorf("output missing heading %q, got:\n%s", heading, out)
		}
	}

	jsonOut := runCLI(t, fake, "service", "list", "--json")
	var views []services.View
	if err := json.Unmarshal([]byte(jsonOut), &views); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if len(views) != 0 {
		t.Fatalf("expected 0 views, got %d", len(views))
	}
}

func TestServiceListJSON(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{
		servicesList: []services.View{
			{
				ID:          services.IDNginx,
				DisplayName: "Nginx Web Server",
				Group:       services.GroupWeb,
				State:       services.StateActive,
				Active:      true,
			},
		},
	}

	out := runCLI(t, fake, "service", "list", "--json")
	var views []services.View
	if err := json.Unmarshal([]byte(out), &views); err != nil {
		t.Fatalf("json.Unmarshal failed: %v\noutput: %s", err, out)
	}
	if len(views) != 1 || views[0].ID != services.IDNginx {
		t.Fatalf("unexpected views: %+v", views)
	}
}

func TestServiceStatus(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{
		serviceMap: map[string]services.View{
			"nginx": {
				ID:          services.IDNginx,
				DisplayName: "Nginx Web Server",
				Group:       services.GroupWeb,
				State:       services.StateActive,
				Active:      true,
				Image:       "fixture-nginx:latest",
				ConfigPath:  "/data/devstack/services/nginx/config",
				DataPath:    "/data/devstack/services/nginx/data",
			},
		},
	}

	out := runCLI(t, fake, "service", "status", "nginx")
	if !strings.Contains(out, "Nginx Web Server") || !strings.Contains(out, "web") || !strings.Contains(out, "active") {
		t.Errorf("status output missing expected fields, got:\n%s", out)
	}
}

func TestServiceStatusJSON(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{
		serviceMap: map[string]services.View{
			"nginx": {
				ID:          services.IDNginx,
				DisplayName: "Nginx Web Server",
				Group:       services.GroupWeb,
				State:       services.StateActive,
				Active:      true,
			},
		},
	}

	out := runCLI(t, fake, "service", "status", "nginx", "--json")
	var v services.View
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("json.Unmarshal failed: %v\noutput: %s", err, out)
	}
	if v.ID != services.IDNginx {
		t.Fatalf("unexpected view: %+v", v)
	}
}

func TestServiceStatusNotFound(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{
		serviceMap: map[string]services.View{},
	}

	code, _, _ := runCLIExit(t, fake, "service", "status", "unknown")
	if code == 0 {
		t.Fatalf("expected non-zero exit code for unknown service status, got %d", code)
	}
}
