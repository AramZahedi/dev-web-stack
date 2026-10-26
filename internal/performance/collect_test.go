package performance

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollectFromReadsMeasuredResources(t *testing.T) {
	proc := t.TempDir()
	sys := t.TempDir()
	writeTestFile(t, filepath.Join(proc, "meminfo"), "MemTotal:       16384000 kB\nMemAvailable:    8192000 kB\n")
	writeTestFile(t, filepath.Join(proc, "sys", "fs", "file-max"), "1048576\n")
	writeTestFile(t, filepath.Join(proc, "sys", "net", "core", "somaxconn"), "4096\n")
	writeTestFile(t, filepath.Join(proc, "sys", "net", "core", "netdev_max_backlog"), "8192\n")
	writeTestFile(t, filepath.Join(proc, "sys", "net", "ipv4", "ip_local_port_range"), "32768\t60999\n")
	writeTestFile(t, filepath.Join(sys, "fs", "cgroup", "cpu.max"), "150000 100000\n")

	resources, err := CollectFrom(CollectOptions{
		ProcRoot: proc,
		SysRoot:  sys,
		Hostname: func() (string, error) { return "devbox", nil },
		CPUCount: 8,
		PageSize: 4096,
		Getrlimit: func() (uint64, uint64, error) {
			return 1024, 1048576, nil
		},
		LookupUser: func() (string, string, error) { return "dev", "dev", nil },
	})
	if err != nil {
		t.Fatalf("CollectFrom: %v", err)
	}
	if resources.Hostname != "devbox" || resources.CPUCount != 8 || resources.CPUQuotaCPUs != 1.5 {
		t.Fatalf("cpu resources = %+v", resources)
	}
	if resources.MemoryTotalBytes != 16384000*1024 || resources.MemoryAvailableBytes != 8192000*1024 {
		t.Fatalf("memory = %d/%d", resources.MemoryTotalBytes, resources.MemoryAvailableBytes)
	}
	if resources.NoFileSoft != 1024 || resources.NoFileHard != 1048576 {
		t.Fatalf("nofile = %d/%d", resources.NoFileSoft, resources.NoFileHard)
	}
	if resources.Sysctl.FileMax != 1048576 || resources.Sysctl.Somaxconn != 4096 ||
		resources.Sysctl.NetdevMaxBacklog != 8192 ||
		resources.Sysctl.IPLocalPortRange.Min != 32768 || resources.Sysctl.IPLocalPortRange.Max != 60999 {
		t.Fatalf("sysctl = %+v", resources.Sysctl)
	}
	if resources.PageSizeBytes != 4096 || resources.User != "dev" || resources.Group != "dev" {
		t.Fatalf("identity = %+v", resources)
	}
	if err := resources.Validate(); err != nil {
		t.Fatalf("collected resources do not validate: %v", err)
	}
}

func TestCollectFromCgroupV1Quota(t *testing.T) {
	proc := t.TempDir()
	sys := t.TempDir()
	writeTestFile(t, filepath.Join(proc, "meminfo"), "MemTotal: 8192000 kB\nMemAvailable: 4096000 kB\n")
	writeTestFile(t, filepath.Join(proc, "sys", "fs", "file-max"), "1048576\n")
	writeTestFile(t, filepath.Join(proc, "sys", "net", "core", "somaxconn"), "4096\n")
	writeTestFile(t, filepath.Join(proc, "sys", "net", "core", "netdev_max_backlog"), "4096\n")
	writeTestFile(t, filepath.Join(proc, "sys", "net", "ipv4", "ip_local_port_range"), "32768 60999\n")
	writeTestFile(t, filepath.Join(sys, "fs", "cgroup", "cpu", "cpu.cfs_quota_us"), "250000\n")
	writeTestFile(t, filepath.Join(sys, "fs", "cgroup", "cpu", "cpu.cfs_period_us"), "100000\n")

	got, err := CollectFrom(CollectOptions{
		ProcRoot:  proc,
		SysRoot:   sys,
		Hostname:  func() (string, error) { return "devbox", nil },
		CPUCount:  8,
		PageSize:  4096,
		Getrlimit: func() (uint64, uint64, error) { return 1024, 1048576, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.CPUQuotaCPUs != 2.5 {
		t.Fatalf("cgroup v1 quota = %f, want 2.5", got.CPUQuotaCPUs)
	}
}

func TestCollectFromRejectsMissingMeasurements(t *testing.T) {
	_, err := CollectFrom(CollectOptions{
		ProcRoot: t.TempDir(),
		SysRoot:  t.TempDir(),
		Hostname: func() (string, error) { return "devbox", nil },
		CPUCount: 4,
		PageSize: 4096,
		Getrlimit: func() (uint64, uint64, error) {
			return 1024, 4096, nil
		},
	})
	if err == nil {
		t.Fatal("CollectFrom succeeded without memory values")
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
}

func TestCollectFromNestedCgroupV2Quota(t *testing.T) {
	proc := t.TempDir()
	sys := t.TempDir()
	writeTestFile(t, filepath.Join(proc, "meminfo"), "MemTotal: 8192000 kB\nMemAvailable: 4096000 kB\n")
	writeTestFile(t, filepath.Join(proc, "self", "cgroup"), "0::/user.slice/user-1000.slice/session-2.scope\n")
	writeTestFile(t, filepath.Join(proc, "sys", "fs", "file-max"), "1048576\n")
	writeTestFile(t, filepath.Join(proc, "sys", "net", "core", "somaxconn"), "4096\n")
	writeTestFile(t, filepath.Join(proc, "sys", "net", "core", "netdev_max_backlog"), "4096\n")
	writeTestFile(t, filepath.Join(proc, "sys", "net", "ipv4", "ip_local_port_range"), "32768 60999\n")
	writeTestFile(t, filepath.Join(sys, "fs", "cgroup", "user.slice", "user-1000.slice", "session-2.scope", "cpu.max"), "300000 100000\n")
	writeTestFile(t, filepath.Join(sys, "fs", "cgroup", "cpu.max"), "max 100000\n")

	got, err := CollectFrom(CollectOptions{
		ProcRoot:  proc,
		SysRoot:   sys,
		Hostname:  func() (string, error) { return "devbox", nil },
		CPUCount:  8,
		PageSize:  4096,
		Getrlimit: func() (uint64, uint64, error) { return 1024, 1048576, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.CPUQuotaCPUs != 3 {
		t.Fatalf("nested cgroup quota = %f, want 3", got.CPUQuotaCPUs)
	}
}
