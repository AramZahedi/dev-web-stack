package services

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

var validIDRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// RootHash returns the first 12 hex characters of the SHA-256 hash of the cleaned, absolute root path.
func RootHash(root string) string {
	clean := filepath.Clean(root)
	if abs, err := filepath.Abs(clean); err == nil {
		clean = abs
	}
	h := sha256.Sum256([]byte(clean))
	return hex.EncodeToString(h[:])[:12]
}

type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	ContainerName string                   `yaml:"container_name"`
	Image         string                   `yaml:"image"`
	Restart       string                   `yaml:"restart"`
	NetworkMode   string                   `yaml:"network_mode,omitempty"`
	Labels        map[string]string        `yaml:"labels"`
	Environment   map[string]string        `yaml:"environment,omitempty"`
	Entrypoint    []string                 `yaml:"entrypoint,omitempty"`
	Command       []string                 `yaml:"command,omitempty"`
	Ports         []string                 `yaml:"ports,omitempty"`
	Volumes       []string                 `yaml:"volumes,omitempty"`
	DependsOn     []string                 `yaml:"depends_on,omitempty"`
	Healthcheck   *composeHealthcheck      `yaml:"healthcheck,omitempty"`
	SecurityOpt   []string                 `yaml:"security_opt,omitempty"`
	CapAdd        []string                 `yaml:"cap_add,omitempty"`
	Ulimits       map[string]composeUlimit `yaml:"ulimits,omitempty"`
}

type composeHealthcheck struct {
	Test        []string `yaml:"test"`
	Interval    string   `yaml:"interval,omitempty"`
	Timeout     string   `yaml:"timeout,omitempty"`
	StartPeriod string   `yaml:"start_period,omitempty"`
	Retries     int      `yaml:"retries,omitempty"`
}

type composeUlimit struct {
	Soft int `yaml:"soft"`
	Hard int `yaml:"hard"`
}

// RenderCompose renders the Compose specification for the given catalog and root.
func RenderCompose(catalog Catalog, root string) ([]byte, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("root path cannot be empty")
	}

	cleanRoot := filepath.Clean(root)
	if abs, err := filepath.Abs(cleanRoot); err == nil {
		cleanRoot = abs
	}

	rootHash := RootHash(cleanRoot)

	// Validate service IDs before resolving dependencies.
	known := make(map[ID]bool, len(catalog.Services))
	for _, s := range catalog.Services {
		idStr := string(s.ID)
		if !validIDRegex.MatchString(idStr) {
			return nil, fmt.Errorf("service ID %q is invalid", s.ID)
		}
		if known[s.ID] {
			return nil, fmt.Errorf("duplicate service ID %q", s.ID)
		}
		known[s.ID] = true
	}

	sortedServices := make([]Service, len(catalog.Services))
	copy(sortedServices, catalog.Services)
	sort.Slice(sortedServices, func(i, j int) bool {
		return sortedServices[i].ID < sortedServices[j].ID
	})

	outServices := make(map[string]composeService, len(sortedServices))

	for _, s := range sortedServices {
		idStr := string(s.ID)

		if strings.TrimSpace(s.Image) == "" {
			return nil, fmt.Errorf("service %q: image cannot be empty", s.ID)
		}
		if !validImageRegex.MatchString(s.Image) {
			return nil, fmt.Errorf("service %q: image %q must be pinned by sha256 digest", s.ID, s.Image)
		}
		for _, arg := range s.Entrypoint {
			if strings.TrimSpace(arg) == "" {
				return nil, fmt.Errorf("service %q: entrypoint arguments cannot be empty", s.ID)
			}
		}
		for _, arg := range s.Command {
			if strings.TrimSpace(arg) == "" {
				return nil, fmt.Errorf("service %q: command arguments cannot be empty", s.ID)
			}
		}

		switch s.NetworkMode {
		case "host", "bridge", "":
		default:
			return nil, fmt.Errorf("service %q: unsupported network mode %q", s.ID, s.NetworkMode)
		}

		// Process ports. Host-network services own their ports directly and never
		// publish them through Compose; catalog status still reports them.
		var ports []string
		if s.NetworkMode != "host" {
			for _, p := range s.Ports {
				if !p.Loopback {
					return nil, fmt.Errorf("service %q: non-loopback port %d is not allowed", s.ID, p.Host)
				}
				if p.Host < 1 || p.Host > 65535 {
					return nil, fmt.Errorf("service %q: invalid host port %d", s.ID, p.Host)
				}
				if p.Container < 1 || p.Container > 65535 {
					return nil, fmt.Errorf("service %q: invalid container port %d", s.ID, p.Container)
				}
				ports = append(ports, fmt.Sprintf("127.0.0.1:%d:%d", p.Host, p.Container))
			}
		}

		// Process mounts
		var volumes []string

		validateMount := func(m Mount) error {
			if strings.TrimSpace(m.ContainerPath) == "" {
				return fmt.Errorf("service %q: mount container path cannot be empty", s.ID)
			}
			if !filepath.IsAbs(m.ContainerPath) {
				return fmt.Errorf("service %q: mount container path %q must be absolute", s.ID, m.ContainerPath)
			}
			cleanContainer := filepath.Clean(m.ContainerPath)
			if isDockerSocket(cleanContainer) {
				return fmt.Errorf("service %q: mounting docker socket is forbidden", s.ID)
			}

			var hostPath string
			area := m.Area
			if area == "" {
				area = MountService
			}

			switch area {
			case MountService, MountSecrets, MountCerts, MountProjects:
				if filepath.IsAbs(m.HostSubdir) {
					return fmt.Errorf("service %q: mount host subdir %q must be relative", s.ID, m.HostSubdir)
				}
				cleanSub := filepath.Clean(m.HostSubdir)
				if cleanSub == ".." || strings.HasPrefix(cleanSub, ".."+string(filepath.Separator)) {
					return fmt.Errorf("service %q: mount host subdir %q escapes %s area", s.ID, m.HostSubdir, area)
				}
				if isDockerSocket(cleanSub) {
					return fmt.Errorf("service %q: mounting docker socket is forbidden", s.ID)
				}

				var areaRoot string
				switch area {
				case MountService:
					areaRoot = filepath.Join(cleanRoot, "services", idStr)
				case MountSecrets:
					areaRoot = filepath.Join(cleanRoot, "secrets")
				case MountCerts:
					areaRoot = filepath.Join(cleanRoot, "certs")
				case MountProjects:
					areaRoot = filepath.Join(cleanRoot, "projects")
				}

				hostPath = filepath.Join(areaRoot, cleanSub)
				rel, err := filepath.Rel(areaRoot, hostPath)
				if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					return fmt.Errorf("service %q: mount path %q escapes %s area", s.ID, hostPath, area)
				}
			case MountAbsolute:
				if strings.TrimSpace(m.HostPath) == "" {
					return fmt.Errorf("service %q: absolute mount host path cannot be empty", s.ID)
				}
				if !filepath.IsAbs(m.HostPath) {
					return fmt.Errorf("service %q: absolute mount host path %q must be absolute", s.ID, m.HostPath)
				}
				hostPath = filepath.Clean(m.HostPath)
				if isDockerSocket(hostPath) {
					return fmt.Errorf("service %q: mounting docker socket is forbidden", s.ID)
				}
			default:
				return fmt.Errorf("service %q: unknown mount area %q", s.ID, area)
			}

			var options []string
			if m.ReadOnly {
				options = append(options, "ro")
			}
			if m.SELinux {
				options = append(options, "z")
			}

			vol := hostPath + ":" + cleanContainer
			if len(options) > 0 {
				vol += ":" + strings.Join(options, ",")
			}
			volumes = append(volumes, vol)
			return nil
		}

		for _, m := range s.Config {
			if err := validateMount(m); err != nil {
				return nil, err
			}
		}
		for _, m := range s.Data {
			if err := validateMount(m); err != nil {
				return nil, err
			}
		}

		var deps []string
		for _, dep := range s.DependsOn {
			if dep == s.ID {
				return nil, fmt.Errorf("service %q cannot depend on itself", s.ID)
			}
			if !known[dep] {
				return nil, fmt.Errorf("service %q depends on unknown service %q", s.ID, dep)
			}
			deps = append(deps, string(dep))
		}

		var healthcheck *composeHealthcheck
		if s.Healthcheck != nil {
			if len(s.Healthcheck.Test) == 0 {
				return nil, fmt.Errorf("service %q: healthcheck test cannot be empty", s.ID)
			}
			healthcheck = &composeHealthcheck{
				Test:        append([]string(nil), s.Healthcheck.Test...),
				Interval:    durationString(s.Healthcheck.Interval),
				Timeout:     durationString(s.Healthcheck.Timeout),
				StartPeriod: durationString(s.Healthcheck.StartPeriod),
				Retries:     s.Healthcheck.Retries,
			}
		}

		var ulimits map[string]composeUlimit
		if len(s.Ulimits) > 0 {
			ulimits = make(map[string]composeUlimit, len(s.Ulimits))
			for _, limit := range s.Ulimits {
				if strings.TrimSpace(limit.Name) == "" {
					return nil, fmt.Errorf("service %q: ulimit name cannot be empty", s.ID)
				}
				ulimits[limit.Name] = composeUlimit{Soft: limit.Soft, Hard: limit.Hard}
			}
		}

		outServices[idStr] = composeService{
			ContainerName: "devstack-" + idStr,
			Image:         s.Image,
			Restart:       "unless-stopped",
			NetworkMode:   s.NetworkMode,
			Labels: map[string]string{
				"devstack.root":    rootHash,
				"devstack.service": idStr,
			},
			Environment: s.Environment,
			Entrypoint:  append([]string(nil), s.Entrypoint...),
			Command:     append([]string(nil), s.Command...),
			Ports:       ports,
			Volumes:     volumes,
			DependsOn:   deps,
			Healthcheck: healthcheck,
			SecurityOpt: s.SecurityOpts,
			CapAdd:      s.CapAdd,
			Ulimits:     ulimits,
		}
	}

	doc := composeFile{
		Services: outServices,
	}

	return yaml.Marshal(doc)
}

func durationString(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}

func isDockerSocket(path string) bool {
	return path == "/var/run/docker.sock" || strings.HasSuffix(path, "docker.sock")
}
