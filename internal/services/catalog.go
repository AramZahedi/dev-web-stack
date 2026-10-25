package services

import (
	"fmt"
	"strings"
)

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

// Mount defines a host directory bind mount.
type Mount struct {
	HostSubdir    string
	ContainerPath string
	ReadOnly      bool
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
}

// Catalog holds all known service definitions.
type Catalog struct {
	Services []Service
}

// DefaultCatalog returns the default service catalog.
// It is initially empty; Wave 1 populates digest-pinned entries.
func DefaultCatalog() Catalog {
	return Catalog{Services: nil}
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
	return nil
}
