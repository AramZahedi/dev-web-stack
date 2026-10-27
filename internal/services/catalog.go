package services

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	validIDRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	// validNetworkAliasRegex accepts Docker DNS labels used as Compose network aliases.
	validNetworkAliasRegex = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
	// validImageRegex accepts an optional tag followed by a mandatory sha256
	// digest. A floating tag without a digest is still rejected.
	validImageRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]*(?::[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?@sha256:[0-9a-f]{64}$`)
)

// ID identifies a managed infrastructure service.
type ID string

const (
	IDNginx      ID = "nginx"
	IDApache     ID = "apache"
	IDMySQL      ID = "mysql"
	IDMariaDB    ID = "mariadb"
	IDPostgreSQL ID = "postgresql"
	IDRedis      ID = "redis"
	IDPHPMyAdmin ID = "phpmyadmin"
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
	ID             ID
	DisplayName    string
	Group          Group
	Image          string
	NetworkMode    string // "host", "bridge", ""
	NetworkAliases []string
	Ports          []Port
	Config         []Mount
	Data           []Mount
	DependsOn      []ID

	Environment     map[string]string
	ExtraHosts      []string
	Healthcheck     *Healthcheck
	Entrypoint      []string
	Command         []string
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
	known := make(map[ID]bool, len(c.Services))
	for _, s := range c.Services {
		if s.ID == "" {
			return fmt.Errorf("service ID cannot be empty")
		}
		if known[s.ID] {
			return fmt.Errorf("duplicate service ID %q", s.ID)
		}
		known[s.ID] = true

		if strings.TrimSpace(s.DisplayName) == "" {
			return fmt.Errorf("service %q: display name cannot be empty", s.ID)
		}
		if err := validateServiceFields(s); err != nil {
			return err
		}
		if s.Group == GroupWeb && len(s.Ports) == 0 && s.NetworkMode != "host" {
			return fmt.Errorf("web service %q must specify ports or host networking", s.ID)
		}
	}

	for _, s := range c.Services {
		if err := validateDependencies(s, known); err != nil {
			return err
		}
	}
	return nil
}

// validateServiceFields applies the rules shared by catalog validation and compose
// rendering. Callers remain responsible for duplicate IDs and dependency membership.
func validateServiceFields(s Service) error {
	if !validIDRegex.MatchString(string(s.ID)) {
		return fmt.Errorf("service ID %q is invalid", s.ID)
	}

	if strings.TrimSpace(s.Image) == "" {
		return fmt.Errorf("service %q: image cannot be empty", s.ID)
	}
	if !validImageRegex.MatchString(s.Image) {
		return fmt.Errorf("service %q: image %q must be pinned by sha256 digest", s.ID, s.Image)
	}
	for _, arg := range s.Entrypoint {
		if strings.TrimSpace(arg) == "" {
			return fmt.Errorf("service %q: entrypoint arguments cannot be empty", s.ID)
		}
	}
	for _, arg := range s.Command {
		if strings.TrimSpace(arg) == "" {
			return fmt.Errorf("service %q: command arguments cannot be empty", s.ID)
		}
	}

	switch s.Group {
	case GroupWeb, GroupMySQLFamily, GroupStandalone:
	default:
		return fmt.Errorf("service %q: unknown group %q", s.ID, s.Group)
	}

	if err := validateNetworkMode(s); err != nil {
		return err
	}
	if err := validatePublishedPorts(s); err != nil {
		return err
	}
	if err := validateExtraHosts(s); err != nil {
		return err
	}
	if err := validateNetworkAliases(s); err != nil {
		return err
	}
	for _, m := range s.Config {
		if err := validateMountFields(s, m); err != nil {
			return err
		}
	}
	for _, m := range s.Data {
		if err := validateMountFields(s, m); err != nil {
			return err
		}
	}
	if err := validateHealthcheck(s); err != nil {
		return err
	}
	return validateUlimits(s)
}

func validateNetworkMode(s Service) error {
	switch s.NetworkMode {
	case "host":
		if s.Group != GroupWeb {
			return fmt.Errorf("service %q: host networking is only allowed for web services", s.ID)
		}
	case "bridge", "":
	default:
		return fmt.Errorf("service %q: unsupported network mode %q", s.ID, s.NetworkMode)
	}
	return nil
}

func validatePublishedPorts(s Service) error {
	if s.NetworkMode == "host" {
		return nil
	}
	for _, p := range s.Ports {
		if !p.Loopback {
			return fmt.Errorf("service %q: non-loopback port %d is not allowed", s.ID, p.Host)
		}
		if p.Host < 1 || p.Host > 65535 {
			return fmt.Errorf("service %q: invalid host port %d", s.ID, p.Host)
		}
		if p.Container < 1 || p.Container > 65535 {
			return fmt.Errorf("service %q: invalid container port %d", s.ID, p.Container)
		}
		switch p.Protocol {
		case "http", "https", "tcp", "":
		default:
			return fmt.Errorf("service %q: invalid port protocol %q", s.ID, p.Protocol)
		}
	}
	return nil
}

func validateExtraHosts(s Service) error {
	for _, entry := range s.ExtraHosts {
		if strings.ContainsAny(entry, " \t\r\n") {
			return fmt.Errorf("service %q: extra host %q cannot contain whitespace", s.ID, entry)
		}
		host, target, ok := strings.Cut(entry, ":")
		if !ok || strings.TrimSpace(host) == "" || strings.TrimSpace(target) == "" {
			return fmt.Errorf("service %q: extra host %q must be host:target", s.ID, entry)
		}
	}
	return nil
}

func validateNetworkAliases(s Service) error {
	if s.NetworkMode == "host" && len(s.NetworkAliases) > 0 {
		return fmt.Errorf("service %q: network aliases are not supported with host networking", s.ID)
	}
	for _, alias := range s.NetworkAliases {
		if !validNetworkAliasRegex.MatchString(alias) {
			return fmt.Errorf("service %q: invalid network alias %q", s.ID, alias)
		}
	}
	return nil
}

func validateMountFields(s Service, m Mount) error {
	if strings.TrimSpace(m.ContainerPath) == "" {
		return fmt.Errorf("service %q: mount container path cannot be empty", s.ID)
	}
	if !strings.HasPrefix(m.ContainerPath, "/") && !filepath.IsAbs(m.ContainerPath) {
		return fmt.Errorf("service %q: mount container path %q must be absolute", s.ID, m.ContainerPath)
	}
	cleanContainer := path.Clean(m.ContainerPath)
	if isDockerSocket(cleanContainer) {
		return fmt.Errorf("service %q: mounting docker socket is forbidden", s.ID)
	}

	area := m.Area
	if area == "" {
		area = MountService
	}

	switch area {
	case MountService, MountSecrets, MountCerts, MountProjects:
		if m.HostPath != "" {
			return fmt.Errorf("service %q: host path must be empty for %s mounts", s.ID, area)
		}
		if strings.HasPrefix(m.HostSubdir, "/") || filepath.IsAbs(m.HostSubdir) {
			return fmt.Errorf("service %q: mount host subdir %q must be relative", s.ID, m.HostSubdir)
		}
		cleanSub := filepath.Clean(m.HostSubdir)
		if cleanSub == ".." || strings.HasPrefix(cleanSub, ".."+string(filepath.Separator)) {
			return fmt.Errorf("service %q: mount host subdir %q escapes %s area", s.ID, m.HostSubdir, area)
		}
		if isDockerSocket(cleanSub) {
			return fmt.Errorf("service %q: mounting docker socket is forbidden", s.ID)
		}
	case MountAbsolute:
		if m.HostSubdir != "" {
			return fmt.Errorf("service %q: host subdir must be empty for absolute mounts", s.ID)
		}
		if strings.TrimSpace(m.HostPath) == "" {
			return fmt.Errorf("service %q: absolute mount host path cannot be empty", s.ID)
		}
		if !strings.HasPrefix(m.HostPath, "/") && !filepath.IsAbs(m.HostPath) {
			return fmt.Errorf("service %q: absolute mount host path %q must be absolute", s.ID, m.HostPath)
		}
		cleanHost := filepath.ToSlash(filepath.Clean(m.HostPath))
		if isDangerousAbsoluteMount(cleanHost) {
			return fmt.Errorf("service %q: absolute mount host path %q is not allowed", s.ID, cleanHost)
		}
		if isDockerSocket(cleanHost) {
			return fmt.Errorf("service %q: mounting docker socket is forbidden", s.ID)
		}
	default:
		return fmt.Errorf("service %q: unknown mount area %q", s.ID, area)
	}
	return nil
}

func validateHealthcheck(s Service) error {
	h := s.Healthcheck
	if h == nil {
		return nil
	}
	if len(h.Test) == 0 {
		return fmt.Errorf("service %q: healthcheck test cannot be empty", s.ID)
	}
	switch h.Test[0] {
	case "NONE", "CMD", "CMD-SHELL":
	default:
		return fmt.Errorf("service %q: invalid healthcheck test prefix %q", s.ID, h.Test[0])
	}
	if h.Interval < 0 || h.Timeout < 0 || h.StartPeriod < 0 {
		return fmt.Errorf("service %q: healthcheck durations cannot be negative", s.ID)
	}
	if h.Retries < 0 {
		return fmt.Errorf("service %q: healthcheck retries cannot be negative", s.ID)
	}
	return nil
}

func validateUlimits(s Service) error {
	for _, limit := range s.Ulimits {
		if strings.TrimSpace(limit.Name) == "" {
			return fmt.Errorf("service %q: ulimit name cannot be empty", s.ID)
		}
		if limit.Soft < 0 || limit.Hard < 0 {
			return fmt.Errorf("service %q: ulimit %q values cannot be negative", s.ID, limit.Name)
		}
	}
	return nil
}

func validateDependencies(s Service, known map[ID]bool) error {
	for _, dep := range s.DependsOn {
		if dep == s.ID {
			return fmt.Errorf("service %q cannot depend on itself", s.ID)
		}
		if !known[dep] {
			return fmt.Errorf("service %q depends on unknown service %q", s.ID, dep)
		}
	}
	return nil
}

func isDockerSocket(path string) bool {
	return path == "/var/run/docker.sock" || strings.HasSuffix(path, "docker.sock")
}

func isDangerousAbsoluteMount(path string) bool {
	switch path {
	case "/", "/var", "/var/run", "/run", "/var/lib/docker":
		return true
	default:
		return false
	}
}
