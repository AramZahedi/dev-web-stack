//go:build linux

// Command linux-performance is a root-only E2E probe for the fixed-path Linux
// host tuning managed by devstack-helper. It applies a computed profile,
// verifies the generated files and live sysctl values, then restores the
// pre-devstack state. Run it only on a disposable or supervised Linux host.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helper"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/performance"
)

func main() {
	profileName := flag.String("profile", string(performance.Throughput), "profile to apply")
	backupRoot := flag.String("backup-root", "", "devstack root for performance backups (default: a temp directory)")
	reset := flag.Bool("reset", true, "restore the pre-devstack host tuning before exit")
	flag.Parse()

	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "linux-performance: must run as root")
		os.Exit(2)
	}
	root := *backupRoot
	if root == "" {
		var err error
		root, err = os.MkdirTemp("", "devstack-performance-e2e-*")
		if err != nil {
			fmt.Fprintln(os.Stderr, "linux-performance:", err)
			os.Exit(1)
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "linux-performance:", err)
		os.Exit(1)
	}
	profile, err := performance.ParseProfile(*profileName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "linux-performance:", err)
		os.Exit(2)
	}
	res, err := performance.Collect()
	if err != nil {
		fmt.Fprintln(os.Stderr, "linux-performance: collect:", err)
		os.Exit(1)
	}
	values, err := performance.Compute(res, performance.Options{Profile: profile})
	if err != nil {
		fmt.Fprintln(os.Stderr, "linux-performance: compute:", err)
		os.Exit(1)
	}
	host := helperproto.PerformanceHostValues{
		FileMax:          values.Host.FileMax,
		Somaxconn:        values.Host.Somaxconn,
		NetdevMaxBacklog: values.Host.NetdevMaxBacklog,
		IPLocalPortMin:   values.Host.IPLocalPortRange.Min,
		IPLocalPortMax:   values.Host.IPLocalPortRange.Max,
		NoFileSoft:       values.Host.NoFileSoft,
		NoFileHard:       values.Host.NoFileHard,
		User:             values.Host.User,
		Group:            values.Host.Group,
	}
	manager := helper.NewPerformanceManager(helper.PerformanceManagerConfig{BackupRoot: root})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	applied, err := manager.Apply(ctx, helperproto.PerformanceApplyRequest{Profile: string(profile), Host: host})
	if err != nil {
		fmt.Fprintln(os.Stderr, "linux-performance: apply:", err)
		os.Exit(1)
	}
	if !applied.Applied {
		fmt.Fprintln(os.Stderr, "linux-performance: apply was not reported as applied")
		os.Exit(1)
	}
	status, err := manager.Status(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "linux-performance: status:", err)
		os.Exit(1)
	}
	if !status.Managed || !status.SysctlPresent || !status.LimitsPresent {
		fmt.Fprintf(os.Stderr, "linux-performance: incomplete status: %+v\n", status)
		os.Exit(1)
	}
	fmt.Printf("linux-performance: applied %s\n", profile)
	fmt.Printf("linux-performance: sysctl=%s limits=%s backup=%s\n", status.SysctlPath, status.LimitsPath, status.BackupDir)

	if !*reset {
		return
	}
	resetResult, err := manager.Reset(ctx, helperproto.PerformanceResetRequest{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "linux-performance: reset:", err)
		os.Exit(1)
	}
	if !resetResult.Restored {
		fmt.Fprintln(os.Stderr, "linux-performance: reset did not restore the prior state")
		os.Exit(1)
	}
	after, err := manager.Status(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "linux-performance: status after reset:", err)
		os.Exit(1)
	}
	if after.Managed || after.SysctlPresent || after.LimitsPresent {
		fmt.Fprintf(os.Stderr, "linux-performance: reset left managed state: %+v\n", after)
		os.Exit(1)
	}
	fmt.Println("linux-performance: reset restored the pre-devstack state")
}
