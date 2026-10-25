package helper_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helper"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

func TestPerformanceHelperRPCFixedPaths(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dest := t.TempDir()
	runner := &performanceRPCFakeRunner{}
	manager := helper.NewPerformanceManager(helper.PerformanceManagerConfig{
		BackupRoot:      root,
		DestinationRoot: dest,
		Runner:          runner,
		Now: func() time.Time {
			return time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
		},
		ReadSysctl: func(key string) (string, error) {
			values := map[string]string{
				"fs.file-max":                  "1048576",
				"net.core.somaxconn":           "4096",
				"net.core.netdev_max_backlog":  "4096",
				"net.ipv4.ip_local_port_range": "32768\t60999",
			}
			return values[key], nil
		},
	})
	socketPath, _ := startTestServer(t, helper.Config{
		AllowUID:           os.Getuid(),
		PerformanceManager: manager,
	})
	client := dialClient(t, socketPath)

	host := helperproto.PerformanceHostValues{
		FileMax:          1048576,
		Somaxconn:        4096,
		NetdevMaxBacklog: 4096,
		IPLocalPortMin:   32768,
		IPLocalPortMax:   60999,
		NoFileSoft:       65536,
		NoFileHard:       262144,
		User:             "dev",
	}
	resp, err := client.PerformanceApply(ctx, helperproto.PerformanceApplyRequest{Profile: "balanced", Host: host})
	if err != nil {
		t.Fatalf("PerformanceApply: %v", err)
	}
	if !resp.Applied {
		t.Fatalf("apply response = %+v", resp)
	}
	for _, file := range resp.Files {
		if file.Path != performanceTargetSysctl() && file.Path != performanceTargetLimits() {
			t.Fatalf("helper accepted or returned an unexpected path %q", file.Path)
		}
	}
	sysctlPath := filepath.Join(dest, strings.TrimPrefix(performanceTargetSysctl(), "/"))
	if _, err := os.Stat(sysctlPath); err != nil {
		t.Fatalf("fixed sysctl path not written: %v", err)
	}

	status, err := client.PerformanceStatus(ctx)
	if err != nil {
		t.Fatalf("PerformanceStatus: %v", err)
	}
	if !status.Managed || status.SysctlPath != performanceTargetSysctl() || status.LimitsPath != performanceTargetLimits() {
		t.Fatalf("status = %+v", status)
	}

	reset, err := client.PerformanceReset(ctx, helperproto.PerformanceResetRequest{})
	if err != nil {
		t.Fatalf("PerformanceReset: %v", err)
	}
	if !reset.Restored {
		t.Fatalf("reset response = %+v", reset)
	}
}

func performanceTargetSysctl() string { return "/etc/sysctl.d/99-devstack-performance.conf" }
func performanceTargetLimits() string { return "/etc/security/limits.d/99-devstack-performance.conf" }

type performanceRPCFakeRunner struct {
	mu    sync.Mutex
	calls [][]string
}

func (r *performanceRPCFakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string{name}, args...))
	return nil, nil
}
