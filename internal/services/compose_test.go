package services_test

import (
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/services"
)

func TestRenderComposeValidFixture(t *testing.T) {
	cat := services.Catalog{
		Services: []services.Service{
			{
				ID:          services.IDNginx,
				DisplayName: "Nginx Web Server",
				Group:       services.GroupWeb,
				Image:       "fixture-nginx@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
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
	if !strings.Contains(content, "127.0.0.1:8080:80") {
		t.Errorf("rendered yaml missing loopback port 127.0.0.1:8080:80, got:\n%s", content)
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
			name:  "host path is docker socket",
			mount: services.Mount{HostSubdir: "/var/run/docker.sock", ContainerPath: "/tmp/sock"},
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
						Image:       "fixture-nginx@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
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
		name       string
		hostSubdir string
	}{
		{name: "dotdot escape", hostSubdir: "../../escaping"},
		{name: "absolute path", hostSubdir: "/etc/shadow"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cat := services.Catalog{
				Services: []services.Service{
					{
						ID:          services.IDMySQL,
						DisplayName: "MySQL",
						Group:       services.GroupMySQLFamily,
						Image:       "fixture-mysql@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
						Data: []services.Mount{
							{HostSubdir: tt.hostSubdir, ContainerPath: "/var/lib/mysql"},
						},
					},
				},
			}
			_, err := services.RenderCompose(cat, "/data/devstack")
			if err == nil {
				t.Fatalf("expected error for escaping mount %q, got nil", tt.hostSubdir)
			}
		})
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

func TestRenderComposeRejectsDuplicateIDs(t *testing.T) {
	cat := services.Catalog{
		Services: []services.Service{
			{
				ID:          services.IDRedis,
				DisplayName: "Redis 1",
				Group:       services.GroupStandalone,
				Image:       "fixture-redis:1",
			},
			{
				ID:          services.IDRedis,
				DisplayName: "Redis 2",
				Group:       services.GroupStandalone,
				Image:       "fixture-redis:2",
			},
		},
	}
	_, err := services.RenderCompose(cat, "/data/devstack")
	if err == nil {
		t.Fatal("expected error for duplicate service IDs, got nil")
	}
}

func TestRenderComposeRejectsNonLoopbackPort(t *testing.T) {
	cat := services.Catalog{
		Services: []services.Service{
			{
				ID:          services.IDRedis,
				DisplayName: "Redis",
				Group:       services.GroupStandalone,
				Image:       "fixture-redis:latest",
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

func TestRenderComposeDeterministicOrdering(t *testing.T) {
	cat := services.Catalog{
		Services: []services.Service{
			{ID: services.IDRedis, DisplayName: "Redis", Group: services.GroupStandalone, Image: "redis:7"},
			{ID: services.IDApache, DisplayName: "Apache", Group: services.GroupWeb, Image: "apache:2", NetworkMode: "host"},
			{ID: services.IDMySQL, DisplayName: "MySQL", Group: services.GroupMySQLFamily, Image: "mysql:8"},
			{ID: services.IDImageMagick, DisplayName: "ImageMagick", Group: services.GroupStandalone, Image: "imagemagick:7"},
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
