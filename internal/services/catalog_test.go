package services

import (
	"testing"
	"time"
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
	for _, id := range []ID{IDNginx, IDApache, IDMySQL, IDMariaDB, IDPostgreSQL, IDRedis, IDPHPMyAdmin} {
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
	c1.Services[0].Ports = []Port{{Container: 0, Host: 6379, Protocol: "tcp", Loopback: true}}
	if err := c1.Validate(); err == nil {
		t.Fatal("expected error for container port 0")
	}

	// Invalid host port
	c2 := Catalog{Services: []Service{baseService}}
	c2.Services[0].Ports = []Port{{Container: 6379, Host: 70000, Protocol: "tcp", Loopback: true}}
	if err := c2.Validate(); err == nil {
		t.Fatal("expected error for host port 70000")
	}

	// Zero host port is not a valid published port
	cZero := Catalog{Services: []Service{baseService}}
	cZero.Services[0].Ports = []Port{{Container: 6379, Host: 0, Protocol: "tcp", Loopback: true}}
	if err := cZero.Validate(); err == nil {
		t.Fatal("expected error for host port 0")
	}

	// Invalid protocol
	c3 := Catalog{Services: []Service{baseService}}
	c3.Services[0].Ports = []Port{{Container: 6379, Host: 6379, Protocol: "invalid", Loopback: true}}
	if err := c3.Validate(); err == nil {
		t.Fatal("expected error for invalid protocol")
	}

	// Valid ports and protocols
	c4 := Catalog{Services: []Service{baseService}}
	c4.Services[0].Ports = []Port{
		{Container: 80, Host: 8080, Protocol: "http", Loopback: true},
		{Container: 443, Host: 8443, Protocol: "https", Loopback: true},
		{Container: 6379, Host: 6379, Protocol: "tcp", Loopback: true},
		{Container: 3306, Host: 3306, Protocol: "", Loopback: true},
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
		"nginx:1.30.6@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"mysql:8.4.13@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"ghcr.io/devstack/imagemagick:1.0.0@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
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

func TestCatalogValidateRejectsInvalidImageTags(t *testing.T) {
	images := []string{
		"nginx:@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"nginx:bad tag@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"nginx:-bad@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"nginx:latest@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde",
	}
	for _, image := range images {
		c := Catalog{Services: []Service{{
			ID:          IDRedis,
			DisplayName: "Redis",
			Group:       GroupStandalone,
			Image:       image,
		}}}
		if err := c.Validate(); err == nil {
			t.Fatalf("expected error for invalid tag/digest image %q", image)
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

func validCatalogService() Service {
	return Service{
		ID:          IDMySQL,
		DisplayName: "MySQL",
		Group:       GroupMySQLFamily,
		Image:       "docker.io/library/mysql@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Ports:       []Port{{Host: 3306, Container: 3306, Protocol: "tcp", Loopback: true}},
	}
}

func TestCatalogValidateRejectsHostNetworkNonWeb(t *testing.T) {
	for _, group := range []Group{GroupMySQLFamily, GroupStandalone} {
		s := validCatalogService()
		s.Group = group
		s.NetworkMode = "host"
		c := Catalog{Services: []Service{s}}
		if err := c.Validate(); err == nil {
			t.Fatalf("expected error for host networking on group %q, got nil", group)
		}
	}
}

func TestCatalogValidateAcceptsHostNetworkWeb(t *testing.T) {
	c := Catalog{Services: []Service{{
		ID:          IDNginx,
		DisplayName: "Nginx",
		Group:       GroupWeb,
		Image:       "docker.io/library/nginx@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		NetworkMode: "host",
		Ports: []Port{
			{Host: 80, Container: 80, Protocol: "http"},
			{Host: 443, Container: 443, Protocol: "https"},
		},
	}}}
	if err := c.Validate(); err != nil {
		t.Fatalf("unexpected error for host-network web service: %v", err)
	}
}

func TestCatalogValidateAcceptsValidMountsAndRuntime(t *testing.T) {
	c := Catalog{Services: []Service{
		{
			ID:          IDRedis,
			DisplayName: "Redis",
			Group:       GroupStandalone,
			Image:       "docker.io/library/redis@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Ports:       []Port{{Host: 6379, Container: 6379, Protocol: "tcp", Loopback: true}},
		},
		{
			ID:          IDPHPMyAdmin,
			DisplayName: "phpMyAdmin",
			Group:       GroupStandalone,
			Image:       "docker.io/library/phpmyadmin@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Ports:       []Port{{Host: 8082, Container: 80, Protocol: "http", Loopback: true}},
			DependsOn:   []ID{IDRedis},
			Config: []Mount{
				{HostSubdir: "config", ContainerPath: "/etc/phpmyadmin"},
				{Area: MountSecrets, HostSubdir: "db", ContainerPath: "/run/secrets/db", ReadOnly: true, SELinux: true},
				{Area: MountCerts, HostSubdir: "leaf", ContainerPath: "/certs", ReadOnly: true},
				{Area: MountProjects, HostSubdir: "app", ContainerPath: "/sites/app", ReadOnly: true},
				{Area: MountAbsolute, HostPath: "/srv/shared", ContainerPath: "/srv/shared", ReadOnly: true},
				{Area: MountAbsolute, HostPath: "/srv/shared:with:colons", ContainerPath: "/srv/colon", ReadOnly: true, SELinux: true},
			},
			Healthcheck: &Healthcheck{
				Test:        []string{"CMD-SHELL", "php -v"},
				Interval:    10 * time.Second,
				Timeout:     5 * time.Second,
				StartPeriod: 30 * time.Second,
				Retries:     3,
			},
			SecurityOpts: []string{"no-new-privileges:true"},
			CapAdd:       []string{"NET_BIND_SERVICE"},
			Ulimits:      []Ulimit{{Name: "nofile", Soft: 20000, Hard: 40000}},
		},
	}}
	if err := c.Validate(); err != nil {
		t.Fatalf("unexpected error for valid catalog: %v", err)
	}
}

func TestCatalogValidateRejectsInvalidServices(t *testing.T) {
	validHealthcheck := func() *Healthcheck {
		return &Healthcheck{Test: []string{"CMD", "true"}, Interval: time.Second}
	}
	tests := []struct {
		name   string
		mutate func(*Service)
	}{
		{name: "invalid service ID", mutate: func(s *Service) { s.ID = "MySQL!" }},
		{name: "empty service ID", mutate: func(s *Service) { s.ID = "" }},
		{name: "empty display name", mutate: func(s *Service) { s.DisplayName = " " }},
		{name: "floating image", mutate: func(s *Service) { s.Image = "mysql:latest" }},
		{name: "unknown group", mutate: func(s *Service) { s.Group = Group("cache") }},
		{name: "host network on non-web", mutate: func(s *Service) { s.NetworkMode = "host" }},
		{name: "unsupported network mode", mutate: func(s *Service) { s.NetworkMode = "overlay" }},
		{name: "non-loopback port", mutate: func(s *Service) {
			s.Ports = []Port{{Host: 3306, Container: 3306, Protocol: "tcp"}}
		}},
		{name: "zero host port", mutate: func(s *Service) {
			s.Ports = []Port{{Host: 0, Container: 3306, Protocol: "tcp", Loopback: true}}
		}},
		{name: "invalid container port", mutate: func(s *Service) {
			s.Ports = []Port{{Host: 3306, Container: 0, Protocol: "tcp", Loopback: true}}
		}},
		{name: "invalid port protocol", mutate: func(s *Service) {
			s.Ports = []Port{{Host: 3306, Container: 3306, Protocol: "udp", Loopback: true}}
		}},
		{name: "empty container path", mutate: func(s *Service) {
			s.Config = []Mount{{HostSubdir: "config"}}
		}},
		{name: "relative container path", mutate: func(s *Service) {
			s.Config = []Mount{{HostSubdir: "config", ContainerPath: "config"}}
		}},
		{name: "docker socket container path", mutate: func(s *Service) {
			s.Config = []Mount{{HostSubdir: "config", ContainerPath: "/var/run/docker.sock"}}
		}},
		{name: "unknown mount area", mutate: func(s *Service) {
			s.Config = []Mount{{Area: MountArea("home"), HostSubdir: "config", ContainerPath: "/config"}}
		}},
		{name: "host path on service mount", mutate: func(s *Service) {
			s.Config = []Mount{{HostSubdir: "config", HostPath: "/srv/shared", ContainerPath: "/config"}}
		}},
		{name: "host subdir on absolute mount", mutate: func(s *Service) {
			s.Config = []Mount{{Area: MountAbsolute, HostSubdir: "config", HostPath: "/srv/shared", ContainerPath: "/config"}}
		}},
		{name: "absolute host subdir", mutate: func(s *Service) {
			s.Config = []Mount{{HostSubdir: "/etc", ContainerPath: "/config"}}
		}},
		{name: "escaping host subdir", mutate: func(s *Service) {
			s.Config = []Mount{{HostSubdir: "../escape", ContainerPath: "/config"}}
		}},
		{name: "docker socket host subdir", mutate: func(s *Service) {
			s.Config = []Mount{{HostSubdir: "config/docker.sock", ContainerPath: "/config"}}
		}},
		{name: "empty absolute host path", mutate: func(s *Service) {
			s.Config = []Mount{{Area: MountAbsolute, ContainerPath: "/config"}}
		}},
		{name: "relative absolute host path", mutate: func(s *Service) {
			s.Config = []Mount{{Area: MountAbsolute, HostPath: "srv/shared", ContainerPath: "/config"}}
		}},
		{name: "dangerous absolute root", mutate: func(s *Service) {
			s.Config = []Mount{{Area: MountAbsolute, HostPath: "/", ContainerPath: "/config"}}
		}},
		{name: "dangerous absolute var", mutate: func(s *Service) {
			s.Config = []Mount{{Area: MountAbsolute, HostPath: "/var", ContainerPath: "/config"}}
		}},
		{name: "dangerous absolute var run", mutate: func(s *Service) {
			s.Config = []Mount{{Area: MountAbsolute, HostPath: "/var/run", ContainerPath: "/config"}}
		}},
		{name: "dangerous absolute run", mutate: func(s *Service) {
			s.Config = []Mount{{Area: MountAbsolute, HostPath: "/run", ContainerPath: "/config"}}
		}},
		{name: "dangerous absolute docker data", mutate: func(s *Service) {
			s.Config = []Mount{{Area: MountAbsolute, HostPath: "/var/lib/docker", ContainerPath: "/config"}}
		}},
		{name: "dangerous absolute docker socket", mutate: func(s *Service) {
			s.Config = []Mount{{Area: MountAbsolute, HostPath: "/srv/app/docker.sock", ContainerPath: "/config"}}
		}},
		{name: "empty healthcheck test", mutate: func(s *Service) {
			s.Healthcheck = &Healthcheck{}
		}},
		{name: "invalid healthcheck prefix", mutate: func(s *Service) {
			s.Healthcheck = &Healthcheck{Test: []string{"RUN", "true"}}
		}},
		{name: "negative healthcheck interval", mutate: func(s *Service) {
			h := validHealthcheck()
			h.Interval = -time.Second
			s.Healthcheck = h
		}},
		{name: "negative healthcheck timeout", mutate: func(s *Service) {
			h := validHealthcheck()
			h.Timeout = -time.Second
			s.Healthcheck = h
		}},
		{name: "negative healthcheck start period", mutate: func(s *Service) {
			h := validHealthcheck()
			h.StartPeriod = -time.Second
			s.Healthcheck = h
		}},
		{name: "negative healthcheck retries", mutate: func(s *Service) {
			h := validHealthcheck()
			h.Retries = -1
			s.Healthcheck = h
		}},
		{name: "empty ulimit name", mutate: func(s *Service) {
			s.Ulimits = []Ulimit{{Name: " ", Soft: 1, Hard: 2}}
		}},
		{name: "negative ulimit soft", mutate: func(s *Service) {
			s.Ulimits = []Ulimit{{Name: "nofile", Soft: -1, Hard: 2}}
		}},
		{name: "negative ulimit hard", mutate: func(s *Service) {
			s.Ulimits = []Ulimit{{Name: "nofile", Soft: 1, Hard: -2}}
		}},
		{name: "self dependency", mutate: func(s *Service) { s.DependsOn = []ID{IDMySQL} }},
		{name: "unknown dependency", mutate: func(s *Service) { s.DependsOn = []ID{"missing"} }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := validCatalogService()
			tt.mutate(&s)
			c := Catalog{Services: []Service{s}}
			if err := c.Validate(); err == nil {
				t.Fatalf("Catalog.Validate accepted invalid service: %+v", s)
			}
		})
	}
}
