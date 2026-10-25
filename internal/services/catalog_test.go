package services

import (
	"testing"
)

func TestGroupsExclusive(t *testing.T) {
	if !GroupWeb.Exclusive() {
		t.Errorf("GroupWeb.Exclusive() = false, want true")
	}
	if !GroupMySQLFamily.Exclusive() {
		t.Errorf("GroupMySQLFamily.Exclusive() = false, want true")
	}
	if GroupStandalone.Exclusive() {
		t.Errorf("GroupStandalone.Exclusive() = true, want false")
	}
}

func TestCatalogValidateRejectsDuplicateIDs(t *testing.T) {
	c := Catalog{
		Services: []Service{
			{
				ID:          IDNginx,
				DisplayName: "nginx",
				Group:       GroupWeb,
				Image:       "docker.io/library/nginx@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				Ports:       []Port{{Host: 80, Container: 80, Protocol: "http"}},
			},
			{
				ID:          IDNginx,
				DisplayName: "nginx duplicate",
				Group:       GroupWeb,
				Image:       "docker.io/library/nginx@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				Ports:       []Port{{Host: 80, Container: 80, Protocol: "http"}},
			},
		},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error on duplicate service ID, got nil")
	}
}

func TestCatalogValidateRejectsWebServiceWithoutPort(t *testing.T) {
	c := Catalog{
		Services: []Service{
			{
				ID:          IDNginx,
				DisplayName: "nginx",
				Group:       GroupWeb,
				Image:       "docker.io/library/nginx@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				Ports:       nil,
			},
		},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for web service without ports, got nil")
	}
}

func TestDefaultCatalog(t *testing.T) {
	c := DefaultCatalog()
	for _, id := range []ID{IDMySQL, IDMariaDB, IDPostgreSQL, IDRedis} {
		if _, ok := c.Get(id); !ok {
			t.Errorf("DefaultCatalog() missing %q", id)
		}
	}
	if err := c.Validate(); err != nil {
		t.Errorf("DefaultCatalog().Validate() err = %v, want nil", err)
	}
}

func TestCatalogValidatePortAndProtocol(t *testing.T) {
	baseService := Service{
		ID:          IDRedis,
		DisplayName: "Redis",
		Group:       GroupStandalone,
		Image:       "docker.io/library/redis@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}

	// Invalid container port
	c1 := Catalog{Services: []Service{baseService}}
	c1.Services[0].Ports = []Port{{Container: 0, Host: 6379, Protocol: "tcp"}}
	if err := c1.Validate(); err == nil {
		t.Fatal("expected error for container port 0")
	}

	// Invalid host port
	c2 := Catalog{Services: []Service{baseService}}
	c2.Services[0].Ports = []Port{{Container: 6379, Host: 70000, Protocol: "tcp"}}
	if err := c2.Validate(); err == nil {
		t.Fatal("expected error for host port 70000")
	}

	// Invalid protocol
	c3 := Catalog{Services: []Service{baseService}}
	c3.Services[0].Ports = []Port{{Container: 6379, Host: 6379, Protocol: "invalid"}}
	if err := c3.Validate(); err == nil {
		t.Fatal("expected error for invalid protocol")
	}

	// Valid ports and protocols
	c4 := Catalog{Services: []Service{baseService}}
	c4.Services[0].Ports = []Port{
		{Container: 80, Host: 80, Protocol: "http"},
		{Container: 443, Host: 443, Protocol: "https"},
		{Container: 6379, Host: 6379, Protocol: "tcp"},
		{Container: 3306, Host: 0, Protocol: ""},
	}
	if err := c4.Validate(); err != nil {
		t.Fatalf("unexpected error for valid ports: %v", err)
	}
}

func TestCatalogGet(t *testing.T) {
	c := Catalog{
		Services: []Service{
			{
				ID:          IDRedis,
				DisplayName: "Redis",
				Group:       GroupStandalone,
				Image:       "docker.io/library/redis@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				Ports:       []Port{{Host: 6379, Container: 6379, Protocol: "tcp", Loopback: true}},
			},
		},
	}
	s, ok := c.Get(IDRedis)
	if !ok || s.DisplayName != "Redis" {
		t.Fatalf("c.Get(IDRedis) = (%v, %v), want Redis, true", s, ok)
	}
	_, ok = c.Get("unknown")
	if ok {
		t.Fatalf("c.Get(unknown) = true, want false")
	}
}

func TestCatalogValidateRejectsFloatingImages(t *testing.T) {
	images := []string{
		"docker.io/library/nginx:latest",
		"docker.io/library/nginx:1.27",
		"nginx",
		"nginx@sha256:abc",
		"docker.io/library/nginx@sha256:0123456789ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef",
		"docker.io/library/nginx@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde",
	}
	for _, image := range images {
		t.Run(image, func(t *testing.T) {
			c := Catalog{Services: []Service{{
				ID:          IDRedis,
				DisplayName: "Redis",
				Group:       GroupStandalone,
				Image:       image,
			}}}
			if err := c.Validate(); err == nil {
				t.Fatalf("expected error for floating image %q, got nil", image)
			}
		})
	}
}

func TestCatalogValidateAcceptsPinnedImages(t *testing.T) {
	images := []string{
		"docker.io/library/nginx@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"ghcr.io/devstack/imagemagick@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	}
	for _, image := range images {
		c := Catalog{Services: []Service{{
			ID:          IDRedis,
			DisplayName: "Redis",
			Group:       GroupStandalone,
			Image:       image,
		}}}
		if err := c.Validate(); err != nil {
			t.Fatalf("unexpected error for pinned image %q: %v", image, err)
		}
	}
}

func TestCatalogValidateRejectsUnknownDependency(t *testing.T) {
	c := Catalog{Services: []Service{{
		ID:          IDRedis,
		DisplayName: "Redis",
		Group:       GroupStandalone,
		Image:       "docker.io/library/redis@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		DependsOn:   []ID{"missing"},
	}}}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for unknown dependency, got nil")
	}
}

func TestCatalogValidateRejectsSelfDependency(t *testing.T) {
	c := Catalog{Services: []Service{{
		ID:          IDRedis,
		DisplayName: "Redis",
		Group:       GroupStandalone,
		Image:       "docker.io/library/redis@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		DependsOn:   []ID{IDRedis},
	}}}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for self dependency, got nil")
	}
}

func TestCatalogValidateRejectsInvalidGroup(t *testing.T) {
	c := Catalog{Services: []Service{{
		ID:          IDRedis,
		DisplayName: "Redis",
		Group:       Group("cache"),
		Image:       "docker.io/library/redis@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}}}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for unknown group, got nil")
	}
}
