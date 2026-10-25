package performance

import (
	"math"
	"path/filepath"
	"strings"
)

// PortRange is the current ephemeral port range.
type PortRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// Sysctl holds the existing host limits used as floors. Generated values never
// lower an existing limit. Zero means the value was unavailable; computed
// tuning is used instead.
type Sysctl struct {
	FileMax          uint64    `json:"file_max"`
	Somaxconn        int       `json:"somaxconn"`
	NetdevMaxBacklog int       `json:"netdev_max_backlog"`
	IPLocalPortRange PortRange `json:"ip_local_port_range"`
}

// Resources is the measured host input to the engine. CPUQuotaCPUs is the
// cgroup CPU quota expressed in CPUs (for example 1.5); zero means no quota.
type Resources struct {
	Hostname             string  `json:"hostname"`
	Arch                 string  `json:"arch"`
	CPUCount             int     `json:"cpu_count"`
	CPUQuotaCPUs         float64 `json:"cpu_quota_cpus"`
	MemoryTotalBytes     uint64  `json:"memory_total_bytes"`
	MemoryAvailableBytes uint64  `json:"memory_available_bytes"`
	PageSizeBytes        uint64  `json:"page_size_bytes"`
	NoFileSoft           uint64  `json:"nofile_soft"`
	NoFileHard           uint64  `json:"nofile_hard"`
	User                 string  `json:"user,omitempty"`
	Group                string  `json:"group,omitempty"`
	Sysctl               Sysctl  `json:"sysctl"`
}

// Validate rejects incomplete or inconsistent host measurements. Memory,
// CPU, and page size are required so the engine never invents a resource
// class from a failed read.
func (r Resources) Validate() error {
	if r.CPUCount <= 0 {
		return invalid("cpu count", "must be greater than zero")
	}
	if math.IsNaN(r.CPUQuotaCPUs) || math.IsInf(r.CPUQuotaCPUs, 0) {
		return invalid("cpu quota", "must be a finite number of CPUs")
	}
	if r.CPUQuotaCPUs < 0 {
		return invalid("cpu quota", "must not be negative")
	}
	if r.MemoryTotalBytes == 0 {
		return invalid("memory total", "must be greater than zero")
	}
	if r.MemoryAvailableBytes == 0 {
		return invalid("memory available", "must be greater than zero")
	}
	if r.PageSizeBytes == 0 {
		return invalid("page size", "must be greater than zero")
	}
	if r.NoFileSoft == 0 {
		return invalid("nofile soft", "must be greater than zero")
	}
	if r.NoFileHard == 0 {
		return invalid("nofile hard", "must be greater than zero")
	}
	if r.NoFileSoft > r.NoFileHard {
		return invalid("nofile", "soft limit %d exceeds hard limit %d", r.NoFileSoft, r.NoFileHard)
	}
	if err := validateAccount("user", r.User); err != nil {
		return err
	}
	if err := validateAccount("group", r.Group); err != nil {
		return err
	}
	if err := validateLabel("hostname", r.Hostname); err != nil {
		return err
	}
	if err := validateLabel("arch", r.Arch); err != nil {
		return err
	}
	return r.Sysctl.IPLocalPortRange.validate("ip_local_port_range")
}

// EffectiveCPUUnits returns the CPU count bounded by the cgroup quota. A
// fractional quota is floored so generated worker counts never exceed the
// quota, with a floor of one CPU (quota 1.5 allocates one worker; quota 0.5
// also allocates one).
func (r Resources) EffectiveCPUUnits() int {
	cpus := float64(r.CPUCount)
	if r.CPUQuotaCPUs > 0 && r.CPUQuotaCPUs < cpus {
		cpus = r.CPUQuotaCPUs
	}
	units := int(math.Floor(cpus))
	if units < 1 {
		return 1
	}
	return units
}

// EffectiveMemoryBytes returns available memory, capped by total memory. The
// kernel can report available memory slightly above total after accounting
// changes; the cap keeps budgets internally consistent.
func (r Resources) EffectiveMemoryBytes() uint64 {
	if r.MemoryAvailableBytes > r.MemoryTotalBytes {
		return r.MemoryTotalBytes
	}
	return r.MemoryAvailableBytes
}

func (p PortRange) validate(field string) error {
	if p.Min == 0 && p.Max == 0 {
		return nil
	}
	if p.Min == 0 || p.Max == 0 {
		return invalid(field, "must have both a minimum and a maximum")
	}
	if p.Min < 1 || p.Max > 65535 {
		return invalid(field, "%d-%d must be inside 1-65535", p.Min, p.Max)
	}
	if p.Min >= p.Max {
		return invalid(field, "minimum %d must be below maximum %d", p.Min, p.Max)
	}
	return nil
}

func validateAccount(field, value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 255 {
		return invalid(field, "is longer than 255 bytes")
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_', r == '.', r == '-':
		default:
			return invalid(field, "%q contains an invalid character", value)
		}
	}
	return nil
}

func validateLabel(field, value string) error {
	if len(value) > 255 {
		return invalid(field, "is longer than 255 bytes")
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return invalid(field, "contains a control character")
		}
	}
	return nil
}

func validateSiteID(id string) error {
	if id == "" {
		return invalid("php site", "id is required")
	}
	if id == "." || id == ".." || strings.ContainsAny(id, `/\`) ||
		!filepath.IsLocal(id) || filepath.Clean(id) != id {
		return invalid("php site", "%q is not a safe site id", id)
	}
	if strings.TrimSpace(id) != id {
		return invalid("php site", "%q has leading or trailing whitespace", id)
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return invalid("php site", "%q contains a control character", id)
		}
	}
	return nil
}
