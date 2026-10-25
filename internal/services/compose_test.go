package services_test

import (
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/services"
	"go.yaml.in/yaml/v3"
)

const (
	nginxFixture       = "docker.io/library/nginx@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	apacheFixture      = "docker.io/library/httpd@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	mysqlFixture       = "docker.io/library/mysql@sha256:1111111111111111111111111111111111111111111111111111111111111111"
	redisFixture       = "docker.io/library/redis@sha256:2222222222222222222222222222222222222222222222222222222222222222"
	imagemagickFixture = "ghcr.io/devstack/imagemagick@sha256:3333333333333333333333333333333333333333333333333333333333333333"
	phpmyadminFixture  = "docker.io/library/phpmyadmin@sha256:4444444444444444444444444444444444444444444444444444444444444444"
)

func TestRenderComposeValidFixture(t *testing.T) {
	cat := services.Catalog{
		Services: []services.Service{
			{
				ID:          services.IDNginx,
				DisplayName: "Nginx Web Server",
				Group:       services.GroupWeb,
				Image:       nginxFixture,
				NetworkMode: "host",
				Ports: []services.Port{
					{Host: 8080, Container: 80, Protocol: "http", Loopback: true},
				},
				Config: []services.Mount{
					{HostSubdir: "config", ContainerPath: "/etc/nginx/conf.d", ReadOnly: true},
				},
			},
		},
	}
	root := "/data/devstack"
	data, err := services.RenderCompose(cat, root)
	if err != nil {
		t.Fatalf("RenderCompose unexpected error: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "container_name: devstack-nginx") {
		t.Errorf("rendered yaml missing container_name: devstack-nginx, got:\n%s", content)
	}
	if strings.Contains(content, "127.0.0.1:8080:80") {
		t.Errorf("host-network service must not publish ports, got:\n%s", content)
	}
	if strings.Contains(content, "ports:") {
		t.Errorf("host-network service must omit ports key, got:\n%s", content)
	}
	if !strings.Contains(content, "/data/devstack/services/nginx/config:/etc/nginx/conf.d:ro") {
		t.Errorf("rendered yaml missing mount /data/devstack/services/nginx/config:/etc/nginx/conf.d:ro, got:\n%s", content)
	}
	if strings.Contains(content, "/var/run/docker.sock") {
		t.Errorf("rendered yaml should not contain docker socket, got:\n%s", content)
	}
	if !strings.Contains(content, "restart: unless-stopped") {
		t.Errorf("rendered yaml missing restart: unless-stopped, got:\n%s", content)
	}
	if !strings.Contains(content, "network_mode: host") {
		t.Errorf("rendered yaml missing network_mode: host, got:\n%s", content)
	}

	expectedHash := services.RootHash(root)
	if !strings.Contains(content, "devstack.root: "+expectedHash) && !strings.Contains(content, "devstack.root: \""+expectedHash+"\"") {
		t.Errorf("rendered yaml missing devstack.root label with hash %q, got:\n%s", expectedHash, content)
	}
	if !strings.Contains(content, "devstack.service: nginx") && !strings.Contains(content, "devstack.service: \"nginx\"") {
		t.Errorf("rendered yaml missing devstack.service label, got:\n%s", content)
	}
}

func TestRenderComposeLoopbackPorts(t *testing.T) {
	cat := services.Catalog{
		Services: []services.Service{
			{
				ID:          services.IDMySQL,
				DisplayName: "MySQL",
				Group:       services.GroupMySQLFamily,
				Image:       mysqlFixture,
				Ports: []services.Port{
					{Host: 3306, Container: 3306, Protocol: "tcp", Loopback: true},
				},
			},
		},
	}
	data, err := services.RenderCompose(cat, "/data/devstack")
	if err != nil {
		t.Fatalf("RenderCompose unexpected error: %v", err)
	}
	if !strings.Contains(string(data), "127.0.0.1:3306:3306") {
		t.Errorf("rendered yaml missing loopback port 127.0.0.1:3306:3306, got:\n%s", data)
	}
}

func TestRenderComposeHostNetworkSkipsPorts(t *testing.T) {
	cat := services.Catalog{
		Services: []services.Service{
			{
				ID:          services.IDApache,
				DisplayName: "Apache",
				Group:       services.GroupWeb,
				Image:       apacheFixture,
				NetworkMode: "host",
				Ports: []services.Port{
					{Host: 8080, Container: 80, Protocol: "http"},
				},
			},
		},
	}
	data, err := services.RenderCompose(cat, "/data/devstack")
	if err != nil {
		t.Fatalf("RenderCompose unexpected error: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "127.0.0.1:8080:80") || strings.Contains(content, "ports:") {
		t.Errorf("host-network service must not publish ports, got:\n%s", content)
	}
}

func TestRenderComposeMountAreas(t *testing.T) {
	cat := services.Catalog{
		Services: []services.Service{
			{
				ID:          services.IDPHPMyAdmin,
				DisplayName: "phpMyAdmin",
				Group:       services.GroupStandalone,
				Image:       phpmyadminFixture,
				Config: []services.Mount{
					{HostSubdir: "config", ContainerPath: "/etc/phpmyadmin"},
					{Area: services.MountSecrets, HostSubdir: "db", ContainerPath: "/run/secrets/db", ReadOnly: true},
					{Area: services.MountCerts, HostSubdir: "leaf", ContainerPath: "/certs", ReadOnly: true},
					{Area: services.MountProjects, HostSubdir: "app", ContainerPath: "/sites/app", ReadOnly: true},
					{Area: services.MountAbsolute, HostPath: "/srv/shared", ContainerPath: "/srv/shared", ReadOnly: true},
				},
			},
		},
	}
	data, err := services.RenderCompose(cat, "/data/devstack")
	if err != nil {
		t.Fatalf("RenderCompose unexpected error: %v", err)
	}
	content := string(data)
	wantVolumes := []string{
		"/data/devstack/services/phpmyadmin/config:/etc/phpmyadmin",
		"/data/devstack/secrets/db:/run/secrets/db:ro",
		"/data/devstack/certs/leaf:/certs:ro",
		"/data/devstack/projects/app:/sites/app:ro",
		"/srv/shared:/srv/shared:ro",
	}
	for _, want := range wantVolumes {
		if !strings.Contains(content, want) {
			t.Errorf("rendered yaml missing volume %q, got:\n%s", want, content)
		}
	}
}

func TestRenderComposeSELinuxMounts(t *testing.T) {
	cat := services.Catalog{
		Services: []services.Service{
			{
				ID:          services.IDNginx,
				DisplayName: "Nginx",
				Group:       services.GroupWeb,
				Image:       nginxFixture,
				NetworkMode: "host",
				Config: []services.Mount{
					{Area: services.MountSecrets, HostSubdir: "db", ContainerPath: "/run/secrets/db", ReadOnly: true, SELinux: true},
				},
				Data: []services.Mount{
					{HostSubdir: "logs", ContainerPath: "/var/log/nginx", SELinux: true},
				},
			},
		},
	}
	data, err := services.RenderCompose(cat, "/data/devstack")
	if err != nil {
		t.Fatalf("RenderCompose unexpected error: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "/data/devstack/secrets/db:/run/secrets/db:ro,z") {
		t.Errorf("rendered yaml missing SELinux read-only mount, got:\n%s", content)
	}
	if !strings.Contains(content, "/data/devstack/services/nginx/logs:/var/log/nginx:z") {
		t.Errorf("rendered yaml missing SELinux writable mount, got:\n%s", content)
	}
}

func TestRenderComposeRejectsDockerSocket(t *testing.T) {
	tests := []struct {
		name  string
		mount services.Mount
	}{
		{
			name:  "container path is docker socket",
			mount: services.Mount{HostSubdir: "config", ContainerPath: "/var/run/docker.sock"},
		},
		{
			name:  "host subdir is docker socket",
			mount: services.Mount{HostSubdir: "/var/run/docker.sock", ContainerPath: "/tmp/sock"},
		},
		{
			name:  "absolute host path is docker socket",
			mount: services.Mount{Area: services.MountAbsolute, HostPath: "/var/run/docker.sock", ContainerPath: "/tmp/sock"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cat := services.Catalog{
				Services: []services.Service{
					{
						ID:          services.IDNginx,
						DisplayName: "Nginx",
						Group:       services.GroupWeb,
						Image:       nginxFixture,
						Config:      []services.Mount{tt.mount},
					},
				},
			}
			_, err := services.RenderCompose(cat, "/data/devstack")
			if err == nil {
				t.Fatalf("expected error mounting docker socket, got nil")
			}
		})
	}
}

func TestRenderComposeRejectsEscapingMount(t *testing.T) {
	tests := []struct {
		name  string
		mount services.Mount
	}{
		{name: "service dotdot escape", mount: services.Mount{HostSubdir: "../../escaping", ContainerPath: "/data"}},
		{name: "service absolute path", mount: services.Mount{HostSubdir: "/etc/shadow", ContainerPath: "/data"}},
		{name: "secrets dotdot escape", mount: services.Mount{Area: services.MountSecrets, HostSubdir: "../../config", ContainerPath: "/data"}},
		{name: "certs absolute path", mount: services.Mount{Area: services.MountCerts, HostSubdir: "/etc/shadow", ContainerPath: "/data"}},
		{name: "projects dotdot escape", mount: services.Mount{Area: services.MountProjects, HostSubdir: "../sibling", ContainerPath: "/data"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cat := services.Catalog{
				Services: []services.Service{
					{
						ID:          services.IDMySQL,
						DisplayName: "MySQL",
						Group:       services.GroupMySQLFamily,
						Image:       mysqlFixture,
						Data:        []services.Mount{tt.mount},
					},
				},
			}
			_, err := services.RenderCompose(cat, "/data/devstack")
			if err == nil {
				t.Fatalf("expected error for escaping mount %q, got nil", tt.mount.HostSubdir)
			}
		})
	}
}

func TestRenderComposeRejectsInvalidAbsoluteMounts(t *testing.T) {
	tests := []services.Mount{
		{Area: services.MountAbsolute, HostPath: "", ContainerPath: "/data"},
		{Area: services.MountAbsolute, HostPath: "srv/shared", ContainerPath: "/data"},
	}
	for _, m := range tests {
		cat := services.Catalog{
			Services: []services.Service{
				{
					ID:          services.IDMySQL,
					DisplayName: "MySQL",
					Group:       services.GroupMySQLFamily,
					Image:       mysqlFixture,
					Data:        []services.Mount{m},
				},
			},
		}
		if _, err := services.RenderCompose(cat, "/data/devstack"); err == nil {
			t.Fatalf("expected error for absolute mount HostPath %q, got nil", m.HostPath)
		}
	}
}

func TestRenderComposeRejectsUnknownMountArea(t *testing.T) {
	cat := services.Catalog{
		Services: []services.Service{
			{
				ID:          services.IDMySQL,
				DisplayName: "MySQL",
				Group:       services.GroupMySQLFamily,
				Image:       mysqlFixture,
				Data: []services.Mount{
					{Area: services.MountArea("home"), HostSubdir: "data", ContainerPath: "/var/lib/mysql"},
				},
			},
		},
	}
	if _, err := services.RenderCompose(cat, "/data/devstack"); err == nil {
		t.Fatal("expected error for unknown mount area, got nil")
	}
}

func TestRenderComposeRejectsEmptyImage(t *testing.T) {
	cat := services.Catalog{
		Services: []services.Service{
			{
				ID:          services.IDRedis,
				DisplayName: "Redis",
				Group:       services.GroupStandalone,
				Image:       "",
			},
		},
	}
	_, err := services.RenderCompose(cat, "/data/devstack")
	if err == nil {
		t.Fatal("expected error for empty image, got nil")
	}
}

func TestRenderComposeRejectsFloatingImage(t *testing.T) {
	images := []string{
		"fixture-redis:latest",
		"redis:7",
		"redis",
		"redis@sha256:abc",
	}
	for _, image := range images {
		cat := services.Catalog{
			Services: []services.Service{
				{
					ID:          services.IDRedis,
					DisplayName: "Redis",
					Group:       services.GroupStandalone,
					Image:       image,
				},
			},
		}
		if _, err := services.RenderCompose(cat, "/data/devstack"); err == nil {
			t.Fatalf("expected error for floating image %q, got nil", image)
		}
	}
}

func TestRenderComposeRejectsDuplicateIDs(t *testing.T) {
	cat := services.Catalog{
		Services: []services.Service{
			{
				ID:          services.IDRedis,
				DisplayName: "Redis 1",
				Group:       services.GroupStandalone,
				Image:       redisFixture,
			},
			{
				ID:          services.IDRedis,
				DisplayName: "Redis 2",
				Group:       services.GroupStandalone,
				Image:       redisFixture,
			},
		},
	}
	_, err := services.RenderCompose(cat, "/data/devstack")
	if err == nil {
		t.Fatal("expected error for duplicate service IDs, got nil")
	}
}

func TestRenderComposeRejectsUnknownDependency(t *testing.T) {
	cat := services.Catalog{
		Services: []services.Service{
			{
				ID:          services.IDPHPMyAdmin,
				DisplayName: "phpMyAdmin",
				Group:       services.GroupStandalone,
				Image:       phpmyadminFixture,
				DependsOn:   []services.ID{services.IDMySQL},
			},
		},
	}
	if _, err := services.RenderCompose(cat, "/data/devstack"); err == nil {
		t.Fatal("expected error for unknown dependency, got nil")
	}
}

func TestRenderComposeRejectsSelfDependency(t *testing.T) {
	cat := services.Catalog{
		Services: []services.Service{
			{
				ID:          services.IDPHPMyAdmin,
				DisplayName: "phpMyAdmin",
				Group:       services.GroupStandalone,
				Image:       phpmyadminFixture,
				DependsOn:   []services.ID{services.IDPHPMyAdmin},
			},
		},
	}
	if _, err := services.RenderCompose(cat, "/data/devstack"); err == nil {
		t.Fatal("expected error for self dependency, got nil")
	}
}

func TestRenderComposeRejectsNonLoopbackPort(t *testing.T) {
	cat := services.Catalog{
		Services: []services.Service{
			{
				ID:          services.IDRedis,
				DisplayName: "Redis",
				Group:       services.GroupStandalone,
				Image:       redisFixture,
				Ports: []services.Port{
					{Host: 6379, Container: 6379, Protocol: "tcp", Loopback: false},
				},
			},
		},
	}
	_, err := services.RenderCompose(cat, "/data/devstack")
	if err == nil {
		t.Fatal("expected error for non-loopback port, got nil")
	}
}

func TestRenderComposeRendersRuntimeSettings(t *testing.T) {
	cat := services.Catalog{
		Services: []services.Service{
			{
				ID:          services.IDMySQL,
				DisplayName: "MySQL",
				Group:       services.GroupMySQLFamily,
				Image:       mysqlFixture,
				Ports: []services.Port{
					{Host: 3306, Container: 3306, Protocol: "tcp", Loopback: true},
				},
				Environment: map[string]string{
					"MYSQL_USER":     "dev",
					"MYSQL_PASSWORD": "secret",
				},
				Healthcheck: &services.Healthcheck{
					Test:        []string{"CMD-SHELL", "mysqladmin ping -h 127.0.0.1"},
					Interval:    10 * time.Second,
					Timeout:     5 * time.Second,
					StartPeriod: 30 * time.Second,
					Retries:     5,
				},
				SecurityOpts: []string{"no-new-privileges:true"},
				CapAdd:       []string{"NET_BIND_SERVICE"},
				Ulimits:      []services.Ulimit{{Name: "nofile", Soft: 20000, Hard: 40000}},
			},
		},
	}
	data, err := services.RenderCompose(cat, "/data/devstack")
	if err != nil {
		t.Fatalf("RenderCompose unexpected error: %v", err)
	}

	var doc struct {
		Services map[string]struct {
			Environment map[string]string `yaml:"environment"`
			Healthcheck struct {
				Test        []string `yaml:"test"`
				Interval    string   `yaml:"interval"`
				Timeout     string   `yaml:"timeout"`
				StartPeriod string   `yaml:"start_period"`
				Retries     int      `yaml:"retries"`
			} `yaml:"healthcheck"`
			SecurityOpt []string `yaml:"security_opt"`
			CapAdd      []string `yaml:"cap_add"`
			Ulimits     map[string]struct {
				Soft int `yaml:"soft"`
				Hard int `yaml:"hard"`
			} `yaml:"ulimits"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v\n%s", err, data)
	}
	svc, ok := doc.Services["mysql"]
	if !ok {
		t.Fatalf("rendered yaml missing mysql service:\n%s", data)
	}
	if svc.Environment["MYSQL_USER"] != "dev" || svc.Environment["MYSQL_PASSWORD"] != "secret" {
		t.Errorf("environment = %v, want MYSQL_USER=dev MYSQL_PASSWORD=secret", svc.Environment)
	}
	if len(svc.Healthcheck.Test) != 2 || svc.Healthcheck.Test[0] != "CMD-SHELL" {
		t.Errorf("healthcheck test = %v, want CMD-SHELL probe", svc.Healthcheck.Test)
	}
	if svc.Healthcheck.Interval != "10s" || svc.Healthcheck.Timeout != "5s" || svc.Healthcheck.StartPeriod != "30s" || svc.Healthcheck.Retries != 5 {
		t.Errorf("healthcheck = %+v, want interval=10s timeout=5s start_period=30s retries=5", svc.Healthcheck)
	}
	if len(svc.SecurityOpt) != 1 || svc.SecurityOpt[0] != "no-new-privileges:true" {
		t.Errorf("security_opt = %v, want [no-new-privileges:true]", svc.SecurityOpt)
	}
	if len(svc.CapAdd) != 1 || svc.CapAdd[0] != "NET_BIND_SERVICE" {
		t.Errorf("cap_add = %v, want [NET_BIND_SERVICE]", svc.CapAdd)
	}
	if limit, ok := svc.Ulimits["nofile"]; !ok || limit.Soft != 20000 || limit.Hard != 40000 {
		t.Errorf("ulimits = %v, want nofile soft=20000 hard=40000", svc.Ulimits)
	}
}

func TestRenderComposeDeterministicOrdering(t *testing.T) {
	cat := services.Catalog{
		Services: []services.Service{
			{ID: services.IDRedis, DisplayName: "Redis", Group: services.GroupStandalone, Image: redisFixture},
			{ID: services.IDApache, DisplayName: "Apache", Group: services.GroupWeb, Image: apacheFixture, NetworkMode: "host"},
			{ID: services.IDMySQL, DisplayName: "MySQL", Group: services.GroupMySQLFamily, Image: mysqlFixture},
			{ID: services.IDImageMagick, DisplayName: "ImageMagick", Group: services.GroupStandalone, Image: imagemagickFixture},
		},
	}
	data, err := services.RenderCompose(cat, "/data/devstack")
	if err != nil {
		t.Fatalf("RenderCompose error: %v", err)
	}

	content := string(data)
	apacheIdx := strings.Index(content, "devstack-apache")
	imIdx := strings.Index(content, "devstack-imagemagick")
	mysqlIdx := strings.Index(content, "devstack-mysql")
	redisIdx := strings.Index(content, "devstack-redis")

	if apacheIdx == -1 || imIdx == -1 || mysqlIdx == -1 || redisIdx == -1 {
		t.Fatalf("missing service in content:\n%s", content)
	}
	if !(apacheIdx < imIdx && imIdx < mysqlIdx && mysqlIdx < redisIdx) {
		t.Fatalf("services not ordered deterministically: apache=%d, imagemagick=%d, mysql=%d, redis=%d",
			apacheIdx, imIdx, mysqlIdx, redisIdx)
	}
}

func TestRenderComposeEmptyCatalog(t *testing.T) {
	cat := services.Catalog{Services: nil}
	data, err := services.RenderCompose(cat, "/data/devstack")
	if err != nil {
		t.Fatalf("unexpected error for empty catalog: %v", err)
	}
	if !strings.Contains(string(data), "services:") {
		t.Errorf("expected services: in empty compose, got:\n%s", string(data))
	}
}
