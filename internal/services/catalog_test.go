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
	if len(c.Services) != 0 {
		t.Errorf("DefaultCatalog().Services length = %d, want 0", len(c.Services))
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
