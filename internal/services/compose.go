package services

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

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
	ContainerName string                    `yaml:"container_name"`
	Image         string                    `yaml:"image"`
	Restart       string                    `yaml:"restart"`
	NetworkMode   string                    `yaml:"network_mode,omitempty"`
	Labels        map[string]string         `yaml:"labels"`
	Environment   map[string]string         `yaml:"environment,omitempty"`
	ExtraHosts    []string                  `yaml:"extra_hosts,omitempty"`
	Entrypoint    []string                  `yaml:"entrypoint,omitempty"`
	Command       []string                  `yaml:"command,omitempty"`
	Ports         []string                  `yaml:"ports,omitempty"`
	Volumes       []composeVolume           `yaml:"volumes,omitempty"`
	Networks      map[string]composeNetwork `yaml:"networks,omitempty"`
	DependsOn     []string                  `yaml:"depends_on,omitempty"`
	Healthcheck   *composeHealthcheck       `yaml:"healthcheck,omitempty"`
	SecurityOpt   []string                  `yaml:"security_opt,omitempty"`
	CapAdd        []string                  `yaml:"cap_add,omitempty"`
	Ulimits       map[string]composeUlimit  `yaml:"ulimits,omitempty"`
}

type composeVolume struct {
	Type     string       `yaml:"type"`
	Source   string       `yaml:"source"`
	Target   string       `yaml:"target"`
	ReadOnly bool         `yaml:"read_only,omitempty"`
	Bind     *composeBind `yaml:"bind,omitempty"`
}

type composeBind struct {
	SELinux string `yaml:"selinux,omitempty"`
}

type composeNetwork struct {
	Aliases []string `yaml:"aliases,omitempty"`
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
	if strings.HasPrefix(root, "/") {
		cleanRoot = path.Clean(root)
	} else if abs, err := filepath.Abs(cleanRoot); err == nil {
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

		if err := validateServiceFields(s); err != nil {
			return nil, err
		}

		// Process ports. Host-network services own their ports directly and never
		// publish them through Compose; catalog status still reports them.
		var ports []string
		if s.NetworkMode != "host" {
			for _, p := range s.Ports {
				ports = append(ports, fmt.Sprintf("127.0.0.1:%d:%d", p.Host, p.Container))
			}
		}

		// Process mounts. Field validation is shared with Catalog.Validate; this
		// layer only resolves the area root and renders the long bind syntax.
		var volumes []composeVolume

		renderMount := func(m Mount) error {
			area := m.Area
			if area == "" {
				area = MountService
			}

			vol := composeVolume{
				Type:     "bind",
				Target:   filepath.ToSlash(filepath.Clean(m.ContainerPath)),
				ReadOnly: m.ReadOnly,
			}

			if area == MountAbsolute {
				if strings.HasPrefix(root, "/") && strings.HasPrefix(m.HostPath, "/") {
					vol.Source = path.Clean(m.HostPath)
				} else {
					vol.Source = filepath.Clean(m.HostPath)
				}
			} else {
				areaRoot := mountAreaRoot(cleanRoot, idStr, area)
				if strings.HasPrefix(root, "/") {
					cleanSub := path.Clean("/" + strings.ReplaceAll(m.HostSubdir, "\\", "/"))
					cleanSub = strings.TrimPrefix(cleanSub, "/")
					vol.Source = path.Join(areaRoot, cleanSub)
					if cleanSub == ".." || strings.HasPrefix(cleanSub, "../") {
						return fmt.Errorf("service %q: mount path %q escapes %s area", s.ID, vol.Source, area)
					}
				} else {
					vol.Source = filepath.Join(areaRoot, filepath.Clean(m.HostSubdir))
					rel, err := filepath.Rel(areaRoot, vol.Source)
					if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
						return fmt.Errorf("service %q: mount path %q escapes %s area", s.ID, vol.Source, area)
					}
				}
			}

			if m.SELinux {
				vol.Bind = &composeBind{SELinux: "z"}
			}
			volumes = append(volumes, vol)
			return nil
		}

		for _, m := range s.Config {
			if err := renderMount(m); err != nil {
				return nil, err
			}
		}
		for _, m := range s.Data {
			if err := renderMount(m); err != nil {
				return nil, err
			}
		}

		if err := validateDependencies(s, known); err != nil {
			return nil, err
		}
		var deps []string
		for _, dep := range s.DependsOn {
			deps = append(deps, string(dep))
		}

		var healthcheck *composeHealthcheck
		if s.Healthcheck != nil {
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
				ulimits[limit.Name] = composeUlimit{Soft: limit.Soft, Hard: limit.Hard}
			}
		}

		networkMode := s.NetworkMode
		if networkMode == "bridge" {
			// An explicit network_mode: bridge is Docker's default bridge and
			// cannot carry Compose network aliases. Omitting it attaches the
			// service to the project's default network instead.
			networkMode = ""
		}

		var networks map[string]composeNetwork
		if len(s.NetworkAliases) > 0 {
			networks = map[string]composeNetwork{
				"default": {Aliases: append([]string(nil), s.NetworkAliases...)},
			}
		}

		outServices[idStr] = composeService{
			ContainerName: "devstack-" + idStr,
			Image:         s.Image,
			Restart:       "unless-stopped",
			NetworkMode:   networkMode,
			Labels: map[string]string{
				"devstack.root":    rootHash,
				"devstack.service": idStr,
			},
			Environment: s.Environment,
			ExtraHosts:  append([]string(nil), s.ExtraHosts...),
			Entrypoint:  append([]string(nil), s.Entrypoint...),
			Command:     append([]string(nil), s.Command...),
			Ports:       ports,
			Volumes:     volumes,
			Networks:    networks,
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

func mountAreaRoot(root, id string, area MountArea) string {
	join := filepath.Join
	if strings.HasPrefix(root, "/") {
		join = path.Join
	}
	switch area {
	case MountService:
		return join(root, "services", id)
	case MountSecrets:
		return join(root, "secrets")
	case MountCerts:
		return join(root, "certs")
	case MountProjects:
		return join(root, "projects")
	default:
		return root
	}
}
