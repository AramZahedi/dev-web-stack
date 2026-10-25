package services

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

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
	ContainerName string            `yaml:"container_name"`
	Image         string            `yaml:"image"`
	Restart       string            `yaml:"restart"`
	NetworkMode   string            `yaml:"network_mode,omitempty"`
	Labels        map[string]string `yaml:"labels"`
	Ports         []string          `yaml:"ports,omitempty"`
	Volumes       []string          `yaml:"volumes,omitempty"`
	DependsOn     []string          `yaml:"depends_on,omitempty"`
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

	// Validate services
	seen := make(map[ID]bool, len(catalog.Services))
	sortedServices := make([]Service, len(catalog.Services))
	copy(sortedServices, catalog.Services)

	sort.Slice(sortedServices, func(i, j int) bool {
		return sortedServices[i].ID < sortedServices[j].ID
	})

	outServices := make(map[string]composeService, len(sortedServices))

	for _, s := range sortedServices {
		idStr := string(s.ID)
		if !validIDRegex.MatchString(idStr) {
			return nil, fmt.Errorf("service ID %q is invalid", s.ID)
		}
		if seen[s.ID] {
			return nil, fmt.Errorf("duplicate service ID %q", s.ID)
		}
		seen[s.ID] = true

		if strings.TrimSpace(s.Image) == "" {
			return nil, fmt.Errorf("service %q: image cannot be empty", s.ID)
		}

		switch s.NetworkMode {
		case "host", "bridge", "":
		default:
			return nil, fmt.Errorf("service %q: unsupported network mode %q", s.ID, s.NetworkMode)
		}

		// Process ports
		var ports []string
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

		// Process mounts
		var volumes []string
		serviceDir := filepath.Join(cleanRoot, "services", idStr)

		validateMount := func(m Mount) error {
			if strings.TrimSpace(m.ContainerPath) == "" {
				return fmt.Errorf("service %q: mount container path cannot be empty", s.ID)
			}
			cleanContainer := filepath.Clean(m.ContainerPath)
			if cleanContainer == "/var/run/docker.sock" || strings.HasSuffix(cleanContainer, "docker.sock") {
				return fmt.Errorf("service %q: mounting docker socket is forbidden", s.ID)
			}
			if !filepath.IsAbs(m.ContainerPath) {
				return fmt.Errorf("service %q: mount container path %q must be absolute", s.ID, m.ContainerPath)
			}

			if filepath.IsAbs(m.HostSubdir) {
				return fmt.Errorf("service %q: mount host subdir %q must be relative", s.ID, m.HostSubdir)
			}
			cleanSub := filepath.Clean(m.HostSubdir)
			if cleanSub == ".." || strings.HasPrefix(cleanSub, ".."+string(filepath.Separator)) {
				return fmt.Errorf("service %q: mount host subdir %q escapes service directory", s.ID, m.HostSubdir)
			}
			if cleanSub == "/var/run/docker.sock" || strings.HasSuffix(cleanSub, "docker.sock") {
				return fmt.Errorf("service %q: mounting docker socket is forbidden", s.ID)
			}

			hostPath := filepath.Join(serviceDir, cleanSub)
			rel, err := filepath.Rel(cleanRoot, hostPath)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("service %q: mount path %q escapes root %q", s.ID, hostPath, cleanRoot)
			}

			vol := hostPath + ":" + cleanContainer
			if m.ReadOnly {
				vol += ":ro"
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
			deps = append(deps, string(dep))
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
			Ports:     ports,
			Volumes:   volumes,
			DependsOn: deps,
		}
	}

	doc := composeFile{
		Services: outServices,
	}

	return yaml.Marshal(doc)
}
