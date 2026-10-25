package services_test

import (
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/services"
	"go.yaml.in/yaml/v3"
)

func TestRenderComposePhpMyAdmin(t *testing.T) {
	data, err := services.RenderCompose(services.DefaultCatalog(), "/data/devstack")
	if err != nil {
		t.Fatalf("RenderCompose() = %v", err)
	}

	var doc struct {
		Services map[string]struct {
			Image       string            `yaml:"image"`
			NetworkMode string            `yaml:"network_mode"`
			Ports       []string          `yaml:"ports"`
			Environment map[string]string `yaml:"environment"`
			ExtraHosts  []string          `yaml:"extra_hosts"`
			Volumes     []renderedVolume  `yaml:"volumes"`
			Networks    map[string]struct {
				Aliases []string `yaml:"aliases"`
			} `yaml:"networks"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v\n%s", err, data)
	}
	svc, ok := doc.Services["phpmyadmin"]
	if !ok {
		t.Fatalf("rendered compose missing phpmyadmin:\n%s", data)
	}
	if svc.Image != "phpmyadmin:5.2.3@sha256:62a50b1419fe55b9b4c3cd6f5aed03a18f8f5b4a236f24735d76400bea75a5ff" {
		t.Errorf("image = %q", svc.Image)
	}
	if svc.NetworkMode != "" {
		t.Errorf("network_mode = %q, want the Compose default network", svc.NetworkMode)
	}
	if len(svc.Ports) != 1 || svc.Ports[0] != "127.0.0.1:8082:80" {
		t.Errorf("ports = %v, want [127.0.0.1:8082:80]", svc.Ports)
	}
	wantEnv := map[string]string{
		"PMA_HOST":         "host.docker.internal",
		"PMA_PORT":         "3306",
		"PMA_ABSOLUTE_URI": "http://127.0.0.1:8082/",
		"PMA_ARBITRARY":    "0",
	}
	for key, want := range wantEnv {
		if got := svc.Environment[key]; got != want {
			t.Errorf("environment[%s] = %q, want %q", key, got, want)
		}
	}
	if len(svc.ExtraHosts) != 1 || svc.ExtraHosts[0] != "host.docker.internal:host-gateway" {
		t.Errorf("extra_hosts = %v, want [host.docker.internal:host-gateway]", svc.ExtraHosts)
	}
	if len(svc.Volumes) != 1 || svc.Volumes[0].Source != "/data/devstack/services/phpmyadmin/config/config.user.inc.php" ||
		svc.Volumes[0].Target != "/etc/phpmyadmin/config.user.inc.php" || !svc.Volumes[0].ReadOnly {
		t.Errorf("phpmyadmin volumes = %+v, want the read-only user config", svc.Volumes)
	}
	for _, id := range []string{"mysql", "mariadb"} {
		network, ok := doc.Services[id].Networks["default"]
		if !ok || len(network.Aliases) != 1 || network.Aliases[0] != "db" {
			t.Errorf("%s networks = %+v, want default alias db", id, doc.Services[id].Networks)
		}
	}
	if strings.Contains(string(data), "/var/run/docker.sock") {
		t.Errorf("rendered phpmyadmin compose mounts the Docker socket:\n%s", data)
	}
	for key, value := range svc.Environment {
		for _, forbidden := range []string{"PASSWORD", "PASSWD", "SECRET"} {
			if strings.Contains(strings.ToUpper(key), forbidden) || strings.Contains(strings.ToUpper(value), forbidden) {
				t.Errorf("phpmyadmin environment contains credential material %s=%q", key, value)
			}
		}
	}
	requireComposeConfigValid(t, data)
}

func TestRenderComposeExtraHosts(t *testing.T) {
	cat := services.Catalog{Services: []services.Service{
		{
			ID:          services.IDPHPMyAdmin,
			DisplayName: "phpMyAdmin",
			Group:       services.GroupStandalone,
			Image:       phpmyadminFixture,
			ExtraHosts:  []string{"host.docker.internal:host-gateway", "internal.example:10.0.0.5"},
		},
	}}
	data, err := services.RenderCompose(cat, "/data/devstack")
	if err != nil {
		t.Fatalf("RenderCompose() = %v", err)
	}
	var doc struct {
		Services map[string]struct {
			ExtraHosts []string `yaml:"extra_hosts"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v\n%s", err, data)
	}
	got := doc.Services["phpmyadmin"].ExtraHosts
	want := []string{"host.docker.internal:host-gateway", "internal.example:10.0.0.5"}
	if len(got) != len(want) {
		t.Fatalf("extra_hosts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("extra_hosts = %v, want %v", got, want)
		}
	}
}
