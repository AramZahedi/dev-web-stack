package performance

import (
	"errors"
	"math"
	"testing"
)

func TestProfileParse(t *testing.T) {
	tests := []struct {
		raw     string
		want    Profile
		wantErr bool
	}{
		{"balanced", Balanced, false},
		{" Throughput ", Throughput, false},
		{"CUSTOM", Custom, false},
		{"", "", true},
		{"max", "", true},
	}
	for _, tt := range tests {
		got, err := ParseProfile(tt.raw)
		if tt.wantErr {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("ParseProfile(%q) error = %v, want ErrInvalid", tt.raw, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseProfile(%q): %v", tt.raw, err)
		}
		if got != tt.want {
			t.Fatalf("ParseProfile(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestResourcesValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Resources)
		wantErr bool
	}{
		{"valid", func(*Resources) {}, false},
		{"zero cpu", func(r *Resources) { r.CPUCount = 0 }, true},
		{"negative cpu", func(r *Resources) { r.CPUCount = -1 }, true},
		{"negative quota", func(r *Resources) { r.CPUQuotaCPUs = -0.5 }, true},
		{"nan quota", func(r *Resources) { r.CPUQuotaCPUs = math.NaN() }, true},
		{"infinite quota", func(r *Resources) { r.CPUQuotaCPUs = math.Inf(1) }, true},
		{"zero total memory", func(r *Resources) { r.MemoryTotalBytes = 0 }, true},
		{"zero available memory", func(r *Resources) { r.MemoryAvailableBytes = 0 }, true},
		{"zero page size", func(r *Resources) { r.PageSizeBytes = 0 }, true},
		{"zero nofile soft", func(r *Resources) { r.NoFileSoft = 0 }, true},
		{"zero nofile hard", func(r *Resources) { r.NoFileHard = 0 }, true},
		{"soft above hard", func(r *Resources) { r.NoFileSoft = r.NoFileHard + 1 }, true},
		{"one-sided port range", func(r *Resources) {
			r.Sysctl.IPLocalPortRange = PortRange{Min: 0, Max: 60000}
		}, true},
		{"inverted port range", func(r *Resources) {
			r.Sysctl.IPLocalPortRange = PortRange{Min: 60000, Max: 50000}
		}, true},
		{"port out of range", func(r *Resources) {
			r.Sysctl.IPLocalPortRange = PortRange{Min: 1024, Max: 70000}
		}, true},
		{"control character in hostname", func(r *Resources) { r.Hostname = "bad\nhost" }, true},
		{"invalid limits user", func(r *Resources) { r.User = "bad user" }, true},
		{"invalid limits group", func(r *Resources) { r.Group = "bad/group" }, true},
		{"available above total is capped", func(r *Resources) { r.MemoryAvailableBytes = r.MemoryTotalBytes + 1 }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := testResources()
			tt.mutate(&res)
			err := res.Validate()
			if tt.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("Validate() error = %v, want ErrInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate(): %v", err)
			}
		})
	}
}

func TestEffectiveCPUUnits(t *testing.T) {
	tests := []struct {
		name  string
		cpus  int
		quota float64
		want  int
	}{
		{"count", 16, 0, 16},
		{"fraction floors to one", 16, 0.5, 1},
		{"one and a half floors to one", 16, 1.5, 1},
		{"two point nine floors to two", 16, 2.9, 2},
		{"eight point five floors to eight", 32, 8.5, 8},
		{"quota above count", 4, 32, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := testResources()
			res.CPUCount = tt.cpus
			res.CPUQuotaCPUs = tt.quota
			if got := res.EffectiveCPUUnits(); got != tt.want {
				t.Fatalf("EffectiveCPUUnits() = %d, want %d", got, tt.want)
			}
		})
	}
}
