package services

import (
	"reflect"
	"strings"
	"testing"
)

func TestDefaultCatalogIncludesPhpMyAdmin(t *testing.T) {
	catalog := DefaultCatalog()
	svc, ok := catalog.Get(IDPHPMyAdmin)
	if !ok {
		t.Fatal("DefaultCatalog() missing phpmyadmin")
	}

	if svc.DisplayName == "" {
		t.Error("phpmyadmin display name is empty")
	}
	if svc.Group != GroupStandalone {
		t.Errorf("phpmyadmin group = %q, want %q", svc.Group, GroupStandalone)
	}
	const wantImage = "phpmyadmin:5.2.3@sha256:62a50b1419fe55b9b4c3cd6f5aed03a18f8f5b4a236f24735d76400bea75a5ff"
	if svc.Image != wantImage {
		t.Errorf("phpmyadmin image = %q, want %q", svc.Image, wantImage)
	}
	if svc.NetworkMode != "bridge" {
		t.Errorf("phpmyadmin network mode = %q, want bridge", svc.NetworkMode)
	}

	wantPorts := []Port{{Host: 8082, Container: 80, Protocol: "http", Loopback: true}}
	if !reflect.DeepEqual(svc.Ports, wantPorts) {
		t.Errorf("phpmyadmin ports = %+v, want %+v", svc.Ports, wantPorts)
	}

	wantEnv := map[string]string{
		"PMA_HOST":         "host.docker.internal",
		"PMA_PORT":         "3306",
		"PMA_ABSOLUTE_URI": "http://127.0.0.1:8082/",
		"PMA_ARBITRARY":    "0",
	}
	if !reflect.DeepEqual(svc.Environment, wantEnv) {
		t.Errorf("phpmyadmin environment = %+v, want %+v", svc.Environment, wantEnv)
	}
	for key := range svc.Environment {
		if strings.Contains(strings.ToUpper(key), "PASSWORD") || strings.Contains(strings.ToUpper(key), "PASSWD") {
			t.Errorf("phpmyadmin environment contains a credential key %q", key)
		}
	}

	wantExtraHosts := []string{"host.docker.internal:host-gateway"}
	if !reflect.DeepEqual(svc.ExtraHosts, wantExtraHosts) {
		t.Errorf("phpmyadmin extra hosts = %+v, want %+v", svc.ExtraHosts, wantExtraHosts)
	}

	if len(svc.DependsOn) != 0 {
		t.Errorf("phpmyadmin must not have a static Compose dependency: %v", svc.DependsOn)
	}

	if svc.Healthcheck == nil {
		t.Fatal("phpmyadmin healthcheck is nil")
	}
	if len(svc.Healthcheck.Test) < 2 {
		t.Fatalf("phpmyadmin healthcheck test = %v, want an HTTP probe", svc.Healthcheck.Test)
	}
	if prefix := svc.Healthcheck.Test[0]; prefix != "CMD" && prefix != "CMD-SHELL" {
		t.Errorf("phpmyadmin healthcheck prefix = %q", prefix)
	}
	if probe := strings.Join(svc.Healthcheck.Test, " "); !strings.Contains(probe, "http://127.0.0.1/") {
		t.Errorf("phpmyadmin healthcheck probe = %q, want the image HTTP endpoint", probe)
	}

	for _, mounts := range [][]Mount{svc.Config, svc.Data} {
		for _, mount := range mounts {
			if strings.Contains(mount.ContainerPath, "docker.sock") {
				t.Errorf("phpmyadmin mounts the Docker socket: %+v", mount)
			}
		}
	}
	if len(svc.Config) != 1 || svc.Config[0].ContainerPath != "/etc/phpmyadmin/config.user.inc.php" || !svc.Config[0].ReadOnly {
		t.Errorf("phpmyadmin config mounts = %+v, want the read-only user config file", svc.Config)
	}
	if len(svc.Data) != 0 {
		t.Errorf("phpmyadmin data mounts = %+v, want none", svc.Data)
	}

	if err := catalog.Validate(); err != nil {
		t.Errorf("DefaultCatalog().Validate() = %v", err)
	}
}

func TestMySQLFamilyUsesDatabaseNetworkAlias(t *testing.T) {
	catalog := DefaultCatalog()
	for _, id := range []ID{IDMySQL, IDMariaDB} {
		svc, ok := catalog.Get(id)
		if !ok {
			t.Fatalf("catalog missing %q", id)
		}
		if len(svc.NetworkAliases) != 1 || svc.NetworkAliases[0] != "db" {
			t.Errorf("%s network aliases = %v, want [db]", id, svc.NetworkAliases)
		}
	}
}

func TestCatalogValidateNetworkAliases(t *testing.T) {
	base := Service{
		ID:          IDMySQL,
		DisplayName: "MySQL",
		Group:       GroupMySQLFamily,
		Image:       "mysql:8.4.13@sha256:76f0358884cb92d9a086d24e5671c38048d3fef40f2588703d73eb55392d6b69",
	}
	for _, alias := range []string{"db", "db-primary"} {
		svc := base
		svc.NetworkAliases = []string{alias}
		if err := (Catalog{Services: []Service{svc}}).Validate(); err != nil {
			t.Errorf("Validate() rejected alias %q: %v", alias, err)
		}
	}
	for _, alias := range []string{"", " ", "DB", "db.internal", "db_alias", "-db"} {
		svc := base
		svc.NetworkAliases = []string{alias}
		if err := (Catalog{Services: []Service{svc}}).Validate(); err == nil {
			t.Errorf("Validate() accepted invalid alias %q", alias)
		}
	}
}

func TestCatalogValidateExtraHosts(t *testing.T) {
	base := Service{
		ID:          IDPHPMyAdmin,
		DisplayName: "phpMyAdmin",
		Group:       GroupStandalone,
		Image:       "phpmyadmin:5.2.3@sha256:62a50b1419fe55b9b4c3cd6f5aed03a18f8f5b4a236f24735d76400bea75a5ff",
		Ports:       []Port{{Host: 8082, Container: 80, Protocol: "http", Loopback: true}},
	}

	valid := []string{"host.docker.internal:host-gateway", "internal.example:10.0.0.1"}
	for _, host := range valid {
		svc := base
		svc.ExtraHosts = []string{host}
		if err := (Catalog{Services: []Service{svc}}).Validate(); err != nil {
			t.Errorf("Validate() rejected extra host %q: %v", host, err)
		}
	}

	invalid := []string{"", " ", "host.docker.internal", "host.docker.internal:", ":host-gateway", "host name:host-gateway", "host:gate way"}
	for _, host := range invalid {
		svc := base
		svc.ExtraHosts = []string{host}
		if err := (Catalog{Services: []Service{svc}}).Validate(); err == nil {
			t.Errorf("Validate() accepted invalid extra host %q", host)
		}
	}
}
