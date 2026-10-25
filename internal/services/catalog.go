package services

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var validImageRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]*@sha256:[0-9a-f]{64}$`)

// ID identifies a managed infrastructure service.
type ID string

const (
	IDNginx       ID = "nginx"
	IDApache      ID = "apache"
	IDMySQL       ID = "mysql"
	IDMariaDB     ID = "mariadb"
	IDPostgreSQL  ID = "postgresql"
	IDRedis       ID = "redis"
	IDPHPMyAdmin  ID = "phpmyadmin"
	IDImageMagick ID = "imagemagick"
)

// Group defines exclusive or standalone groupings.
type Group string

const (
	GroupWeb         Group = "web"
	GroupMySQLFamily Group = "mysql-family"
	GroupStandalone  Group = "standalone"
)

// Exclusive returns true if only one member of the group may be active at once.
func (g Group) Exclusive() bool {
	switch g {
	case GroupWeb, GroupMySQLFamily:
		return true
	default:
		return false
	}
}

// Port defines a port exposure rule.
type Port struct {
	Host      int    `json:"host"`
	Container int    `json:"container"`
	Protocol  string `json:"protocol,omitempty"`
	Loopback  bool   `json:"loopback,omitempty"`
}

// MountArea identifies the root that a bind mount host path is resolved against.
type MountArea string

const (
	// MountService resolves HostSubdir under <root>/services/<id>.
	MountService MountArea = "service"
	// MountSecrets resolves HostSubdir under <root>/secrets.
	MountSecrets MountArea = "secrets"
	// MountCerts resolves HostSubdir under <root>/certs.
	MountCerts MountArea = "certs"
	// MountProjects resolves HostSubdir under <root>/projects.
	MountProjects MountArea = "projects"
	// MountAbsolute uses the validated absolute HostPath.
	MountAbsolute MountArea = "absolute"
)

// Mount defines a host directory bind mount.
type Mount struct {
	HostSubdir    string
	ContainerPath string
	ReadOnly      bool
	Area          MountArea
	HostPath      string
	SELinux       bool
}

// Healthcheck defines a container health probe.
type Healthcheck struct {
	Test        []string
	Interval    time.Duration
	Timeout     time.Duration
	StartPeriod time.Duration
	Retries     int
}

// Ulimit defines a container resource limit.
type Ulimit struct {
	Name string
	Soft int
	Hard int
}

// Service is the immutable catalog entry describing an infrastructure service.
type Service struct {
	ID          ID
	DisplayName string
	Group       Group
	Image       string
	NetworkMode string // "host", "bridge", ""
	Ports       []Port
	Config      []Mount
	Data        []Mount
	DependsOn   []ID

	Environment     map[string]string
	Healthcheck     *Healthcheck
	ValidateCommand []string
	ReloadCommand   []string
	SecurityOpts    []string
	CapAdd          []string
	Ulimits         []Ulimit
}

// Catalog holds all known service definitions.
type Catalog struct {
	Services []Service
}

// DefaultCatalog returns the default service catalog composed from the lane-owned
// catalog files. The lanes populate web, database, and standalone entries.
func DefaultCatalog() Catalog {
	web := webServices()
	databases := databaseServices()
	standalone := standaloneServices()

	out := make([]Service, 0, len(web)+len(databases)+len(standalone))
	out = append(out, web...)
	out = append(out, databases...)
	out = append(out, standalone...)
	return Catalog{Services: out}
}

// Get looks up a service by ID.
func (c Catalog) Get(id ID) (Service, bool) {
	for _, s := range c.Services {
		if s.ID == id {
			return s, true
		}
	}
	return Service{}, false
}

// Validate checks the catalog for internal consistency.
func (c Catalog) Validate() error {
	seen := make(map[ID]bool, len(c.Services))
	for _, s := range c.Services {
		if s.ID == "" {
			return fmt.Errorf("service ID cannot be empty")
		}
		if seen[s.ID] {
			return fmt.Errorf("duplicate service ID %q", s.ID)
		}
		seen[s.ID] = true

		if strings.TrimSpace(s.DisplayName) == "" {
			return fmt.Errorf("service %q: display name cannot be empty", s.ID)
		}

		switch s.Group {
		case GroupWeb, GroupMySQLFamily, GroupStandalone:
		default:
			return fmt.Errorf("service %q: unknown group %q", s.ID, s.Group)
		}

		if strings.TrimSpace(s.Image) == "" {
			return fmt.Errorf("service %q: image cannot be empty", s.ID)
		}
		if !validImageRegex.MatchString(s.Image) {
			return fmt.Errorf("service %q: image %q must be pinned by sha256 digest", s.ID, s.Image)
		}

		if s.Group == GroupWeb && len(s.Ports) == 0 && s.NetworkMode != "host" {
			return fmt.Errorf("web service %q must specify ports or host networking", s.ID)
		}

		for _, p := range s.Ports {
			if p.Container < 1 || p.Container > 65535 {
				return fmt.Errorf("service %q: invalid container port %d", s.ID, p.Container)
			}
			if p.Host < 0 || p.Host > 65535 {
				return fmt.Errorf("service %q: invalid host port %d", s.ID, p.Host)
			}
			switch p.Protocol {
			case "http", "https", "tcp", "":
			default:
				return fmt.Errorf("service %q: invalid port protocol %q", s.ID, p.Protocol)
			}
		}
	}

	for _, s := range c.Services {
		for _, dep := range s.DependsOn {
			if dep == s.ID {
				return fmt.Errorf("service %q cannot depend on itself", s.ID)
			}
			if !seen[dep] {
				return fmt.Errorf("service %q depends on unknown service %q", s.ID, dep)
			}
		}
	}
	return nil
}
