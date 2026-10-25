package helper

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

type performanceRunnerCall struct {
	name string
	args []string
}

type performanceTestRunner struct {
	mu    sync.Mutex
	calls []performanceRunnerCall
	err   error
}

func (r *performanceTestRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, performanceRunnerCall{name: name, args: append([]string(nil), args...)})
	return nil, r.err
}

func (r *performanceTestRunner) sawSysctlSystem() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, call := range r.calls {
		if call.name == "sysctl" && len(call.args) == 1 && call.args[0] == "--system" {
			return true
		}
	}
	return false
}

func testPerformanceHost() helperproto.PerformanceHostValues {
	return helperproto.PerformanceHostValues{
		FileMax:          1048576,
		Somaxconn:        4096,
		NetdevMaxBacklog: 4096,
		IPLocalPortMin:   32768,
		IPLocalPortMax:   60999,
		NoFileSoft:       65536,
		NoFileHard:       262144,
		User:             "dev",
		Group:            "dev",
	}
}

func newPerformanceManagerForTest(t *testing.T) (*PerformanceManager, string, string, *performanceTestRunner) {
	t.Helper()
	root := t.TempDir()
	dest := t.TempDir()
	runner := &performanceTestRunner{}
	manager := NewPerformanceManager(PerformanceManagerConfig{
		BackupRoot:      root,
		DestinationRoot: dest,
		Runner:          runner,
		Now: func() time.Time {
			return time.Date(2026, 10, 26, 12, 34, 56, 123456789, time.UTC)
		},
		ReadSysctl: func(key string) (string, error) {
			switch key {
			case "fs.file-max":
				return "1048576", nil
			case "net.core.somaxconn":
				return "4096", nil
			case "net.core.netdev_max_backlog":
				return "4096", nil
			case "net.ipv4.ip_local_port_range":
				return "32768\t60999", nil
			default:
				return "", errors.New("unknown key")
			}
		},
	})
	return manager, root, dest, runner
}

func TestPerformanceManagerApplyWritesFixedPathsAndResetRestores(t *testing.T) {
	ctx := context.Background()
	manager, root, dest, runner := newPerformanceManagerForTest(t)

	originalSysctl := []byte("fs.file-max = 1000\n")
	originalLimits := []byte("dev soft nofile 1024\ndev hard nofile 4096\n")
	sysctlPath := filepath.Join(dest, strings.TrimPrefix(performanceSysctlTarget, "/"))
	limitsPath := filepath.Join(dest, strings.TrimPrefix(performanceLimitsTarget, "/"))
	if err := os.MkdirAll(filepath.Dir(sysctlPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(limitsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sysctlPath, originalSysctl, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(limitsPath, originalLimits, 0o644); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(dest, "etc", "sysctl.d", "99-unrelated.conf")
	if err := os.WriteFile(unrelated, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp, err := manager.Apply(ctx, helperproto.PerformanceApplyRequest{Profile: "balanced", Host: testPerformanceHost()})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !resp.Applied || resp.BackupDir == "" {
		t.Fatalf("apply response = %+v", resp)
	}
	if !strings.HasPrefix(resp.BackupDir, filepath.Join(root, "backups", "performance", "20261026T123456.123456789Z")) {
		t.Fatalf("backup dir = %q", resp.BackupDir)
	}
	if len(resp.Files) != 2 || resp.Files[0].Path != performanceSysctlTarget || resp.Files[1].Path != performanceLimitsTarget {
		t.Fatalf("fixed file paths = %+v", resp.Files)
	}
	data, err := os.ReadFile(sysctlPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"fs.file-max = 1048576", "net.core.somaxconn = 4096", "net.ipv4.ip_local_port_range = 32768 60999"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("sysctl content missing %q:\n%s", want, data)
		}
	}
	limits, err := os.ReadFile(limitsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(limits), "dev soft nofile 65536") || !strings.Contains(string(limits), "dev hard nofile 262144") {
		t.Fatalf("limits content:\n%s", limits)
	}
	if !runner.sawSysctlSystem() {
		t.Fatal("sysctl --system was not run")
	}
	backupSysctl := filepath.Join(resp.BackupDir, "etc", "sysctl.d", "99-devstack-performance.conf")
	backup, err := os.ReadFile(backupSysctl)
	if err != nil || string(backup) != string(originalSysctl) {
		t.Fatalf("backup sysctl = %q, %v", backup, err)
	}

	status, err := manager.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Managed || status.Profile != "balanced" || status.BackupDir != resp.BackupDir {
		t.Fatalf("status = %+v", status)
	}
	if !strings.Contains(string(status.SysctlContent), "fs.file-max = 1048576") {
		t.Fatalf("status sysctl content = %q", status.SysctlContent)
	}

	resetResp, err := manager.Reset(ctx, helperproto.PerformanceResetRequest{})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if !resetResp.Restored {
		t.Fatalf("reset response = %+v", resetResp)
	}
	data, err = os.ReadFile(sysctlPath)
	if err != nil || string(data) != string(originalSysctl) {
		t.Fatalf("restored sysctl = %q, %v", data, err)
	}
	data, err = os.ReadFile(limitsPath)
	if err != nil || string(data) != string(originalLimits) {
		t.Fatalf("restored limits = %q, %v", data, err)
	}
	kept, err := os.ReadFile(unrelated)
	if err != nil || string(kept) != "keep\n" {
		t.Fatalf("unrelated sysctl file changed: %q, %v", kept, err)
	}

	status, err = manager.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Managed {
		t.Fatal("status still reports managed after reset")
	}
}

func TestPerformanceManagerApplyRollsBackOnVerificationFailure(t *testing.T) {
	ctx := context.Background()
	manager, _, dest, runner := newPerformanceManagerForTest(t)
	manager.readSysctl = func(key string) (string, error) {
		return "1", nil
	}
	sysctlPath := filepath.Join(dest, strings.TrimPrefix(performanceSysctlTarget, "/"))
	if err := os.MkdirAll(filepath.Dir(sysctlPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sysctlPath, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := manager.Apply(ctx, helperproto.PerformanceApplyRequest{Profile: "balanced", Host: testPerformanceHost()})
	if err == nil {
		t.Fatal("apply succeeded with mismatched sysctl values")
	}
	var applyErr *PerformanceApplyError
	if !errors.As(err, &applyErr) {
		t.Fatalf("error = %T, want *PerformanceApplyError", err)
	}
	if applyErr.Response.BackupDir == "" {
		t.Fatal("failure response has no backup dir")
	}
	data, readErr := os.ReadFile(sysctlPath)
	if readErr != nil || string(data) != "original\n" {
		t.Fatalf("sysctl after rollback = %q, %v", data, readErr)
	}
	if !runner.sawSysctlSystem() {
		t.Fatal("sysctl --system was not run for apply/rollback")
	}
	status, err := manager.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Managed {
		t.Fatal("failed apply left the managed marker")
	}
}

func TestPerformanceManagerRejectsInvalidHostValues(t *testing.T) {
	ctx := context.Background()
	manager, _, _, runner := newPerformanceManagerForTest(t)

	tests := []struct {
		name string
		host helperproto.PerformanceHostValues
	}{
		{"soft above hard", func() helperproto.PerformanceHostValues {
			h := testPerformanceHost()
			h.NoFileSoft = h.NoFileHard + 1
			return h
		}()},
		{"unsafe user", func() helperproto.PerformanceHostValues {
			h := testPerformanceHost()
			h.User = "dev\nroot"
			return h
		}()},
		{"negative backlog", func() helperproto.PerformanceHostValues {
			h := testPerformanceHost()
			h.Somaxconn = -1
			return h
		}()},
		{"bad port range", func() helperproto.PerformanceHostValues {
			h := testPerformanceHost()
			h.IPLocalPortMin = 70000
			return h
		}()},
		{"absurd file max", func() helperproto.PerformanceHostValues {
			h := testPerformanceHost()
			h.FileMax = 1 << 60
			return h
		}()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := manager.Apply(ctx, helperproto.PerformanceApplyRequest{Profile: "balanced", Host: tt.host}); !errors.Is(err, helperproto.ErrInvalidRequest) {
				t.Fatalf("error = %v, want invalid request", err)
			}
		})
	}
	if runner.sawSysctlSystem() {
		t.Fatal("invalid request ran sysctl")
	}
}

func TestPerformanceManagerResetWithoutApplyIsIdempotent(t *testing.T) {
	ctx := context.Background()
	manager, _, dest, _ := newPerformanceManagerForTest(t)
	sysctlPath := filepath.Join(dest, strings.TrimPrefix(performanceSysctlTarget, "/"))
	if err := os.MkdirAll(filepath.Dir(sysctlPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sysctlPath, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resp, err := manager.Reset(ctx, helperproto.PerformanceResetRequest{})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if resp.Restored {
		t.Fatal("reset without a marker reported restored")
	}
	if _, err := os.Stat(sysctlPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale managed file remains: %v", err)
	}
	// A second reset must not fail.
	if _, err := manager.Reset(ctx, helperproto.PerformanceResetRequest{}); err != nil {
		t.Fatalf("second reset: %v", err)
	}
}

func TestPerformanceManagerRejectsTamperedBackupPath(t *testing.T) {
	ctx := context.Background()
	manager, _, _, _ := newPerformanceManagerForTest(t)
	if _, err := manager.Apply(ctx, helperproto.PerformanceApplyRequest{Profile: "balanced", Host: testPerformanceHost()}); err != nil {
		t.Fatal(err)
	}
	markerPath := manager.markerPath()
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	var marker map[string]any
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatal(err)
	}
	marker["backup_dir"] = "/etc"
	updated, _ := json.Marshal(marker)
	if err := os.WriteFile(markerPath, updated, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Reset(ctx, helperproto.PerformanceResetRequest{}); err == nil {
		t.Fatal("reset accepted a backup directory outside backups/performance")
	}
}

func TestPerformanceManagerRejectsUnmanagedRestoreContent(t *testing.T) {
	ctx := context.Background()
	manager, _, dest, _ := newPerformanceManagerForTest(t)
	sysctlPath := filepath.Join(dest, strings.TrimPrefix(performanceSysctlTarget, "/"))
	if err := os.MkdirAll(filepath.Dir(sysctlPath), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("fs.file-max = 1000\n")
	if err := os.WriteFile(sysctlPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	resp, err := manager.Apply(ctx, helperproto.PerformanceApplyRequest{Profile: "balanced", Host: testPerformanceHost()})
	if err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(resp.BackupDir, "etc", "sysctl.d", "99-devstack-performance.conf")
	if err := os.WriteFile(backupPath, []byte("kernel.unprivileged_bpf_disabled = 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Reset(ctx, helperproto.PerformanceResetRequest{}); err == nil {
		t.Fatal("reset restored non-allowlisted sysctl content")
	}
	if _, err := os.Stat(manager.markerPath()); err != nil {
		t.Fatalf("failed reset removed the marker: %v", err)
	}
}

func TestPerformanceManagerResetRestoresSavedKernelValues(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dest := t.TempDir()
	runner := &performanceTestRunner{}
	baseline := map[string]string{
		"fs.file-max":                  "1000",
		"net.core.somaxconn":           "128",
		"net.core.netdev_max_backlog":  "256",
		"net.ipv4.ip_local_port_range": "32768 60999",
	}
	expected := map[string]string{
		"fs.file-max":                  "1048576",
		"net.core.somaxconn":           "4096",
		"net.core.netdev_max_backlog":  "4096",
		"net.ipv4.ip_local_port_range": "32768 60999",
	}
	var mu sync.Mutex
	reads := 0
	manager := NewPerformanceManager(PerformanceManagerConfig{
		BackupRoot:      root,
		DestinationRoot: dest,
		Runner:          runner,
		ReadSysctl: func(key string) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			reads++
			if reads <= len(baseline) {
				return baseline[key], nil
			}
			return expected[key], nil
		},
	})
	if _, err := manager.Apply(ctx, helperproto.PerformanceApplyRequest{Profile: "balanced", Host: testPerformanceHost()}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := manager.Reset(ctx, helperproto.PerformanceResetRequest{}); err != nil {
		t.Fatalf("reset: %v", err)
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	found := map[string]bool{}
	for _, call := range runner.calls {
		if call.name != "sysctl" || len(call.args) != 2 || call.args[0] != "-w" {
			continue
		}
		for key, value := range baseline {
			if call.args[1] == key+"="+value {
				found[key] = true
			}
		}
	}
	for key := range baseline {
		if !found[key] {
			t.Fatalf("reset did not restore saved %s; calls=%+v", key, runner.calls)
		}
	}
}
